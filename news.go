package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: daily news — one English business-news story per day on the
// main page. Currently sourced from the public BBC Business RSS feed and
// cached per-day in phraseup.daily_news, so the feed is fetched at most once
// per day no matter how many people open the app. If/when the app gets Slack
// Data API access, this can be re-pointed at the team's news channel by
// swapping fetchNewsFromFeed for a Slack fetch — the storage/endpoint shape
// stays the same.

const newsFeedURL = "https://feeds.bbci.co.uk/news/business/rss.xml"

var newsSchemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS phraseup.daily_news (
		news_date  DATE PRIMARY KEY,
		title      TEXT NOT NULL,
		summary    TEXT NOT NULL,
		url        TEXT NOT NULL,
		image_url  TEXT NOT NULL DEFAULT '',
		source     TEXT NOT NULL DEFAULT 'BBC Business',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
}

type NewsStory struct {
	Date     string `json:"date"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	URL      string `json:"url"`
	ImageURL string `json:"image_url"`
	Source   string `json:"source"`
}

type rssFeed struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Description string `xml:"description"`
			Link        string `xml:"link"`
			Thumbnail   struct {
				URL string `xml:"url,attr"`
			} `xml:"thumbnail"`
		} `xml:"item"`
	} `xml:"channel"`
}

// fetchNewsFromFeed pulls the top story from the RSS feed.
func fetchNewsFromFeed(ctx context.Context) (NewsStory, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, newsFeedURL, nil)
	if err != nil {
		return NewsStory{}, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return NewsStory{}, fmt.Errorf("news feed fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return NewsStory{}, fmt.Errorf("news feed: HTTP %d", resp.StatusCode)
	}

	var feed rssFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return NewsStory{}, fmt.Errorf("news feed parse: %w", err)
	}
	for _, item := range feed.Channel.Items {
		title := strings.TrimSpace(item.Title)
		summary := strings.TrimSpace(item.Description)
		link := strings.TrimSpace(item.Link)
		if title == "" || link == "" {
			continue
		}
		return NewsStory{
			Title:    title,
			Summary:  summary,
			URL:      link,
			ImageURL: strings.TrimSpace(item.Thumbnail.URL),
			Source:   "BBC Business",
		}, nil
	}
	return NewsStory{}, fmt.Errorf("news feed: no usable items")
}

// ensureTodayNews returns today's cached story, fetching and caching it on
// the first request of the day (Seoul time). On fetch failure it falls back
// to the most recent cached story rather than erroring the widget.
func ensureTodayNews(ctx context.Context) (NewsStory, error) {
	today := time.Now().In(seoulTZ).Format("2006-01-02")

	var s NewsStory
	var d time.Time
	err := db.QueryRow(ctx, `
		SELECT news_date, title, summary, url, image_url, source
		FROM phraseup.daily_news WHERE news_date = $1
	`, today).Scan(&d, &s.Title, &s.Summary, &s.URL, &s.ImageURL, &s.Source)
	if err == nil {
		s.Date = d.Format("2006-01-02")
		return s, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NewsStory{}, err
	}

	fetched, fetchErr := fetchNewsFromFeed(ctx)
	if fetchErr != nil {
		log.Printf("news: %v", fetchErr)
		// Fall back to the newest cached story from a previous day.
		err = db.QueryRow(ctx, `
			SELECT news_date, title, summary, url, image_url, source
			FROM phraseup.daily_news ORDER BY news_date DESC LIMIT 1
		`).Scan(&d, &s.Title, &s.Summary, &s.URL, &s.ImageURL, &s.Source)
		if err != nil {
			return NewsStory{}, fetchErr
		}
		s.Date = d.Format("2006-01-02")
		return s, nil
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO phraseup.daily_news (news_date, title, summary, url, image_url, source)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (news_date) DO NOTHING
	`, today, fetched.Title, fetched.Summary, fetched.URL, fetched.ImageURL, fetched.Source); err != nil {
		log.Printf("news: cache save: %v", err)
	}
	fetched.Date = today
	return fetched, nil
}

func registerNewsRoutes(r *gin.Engine) {
	r.GET("/api/news/today", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		story, err := ensureTodayNews(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "news unavailable"})
			return
		}
		c.JSON(http.StatusOK, story)
	})
}
