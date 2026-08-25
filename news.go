package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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
		summary_ko TEXT NOT NULL DEFAULT '',
		url        TEXT NOT NULL,
		image_url  TEXT NOT NULL DEFAULT '',
		source     TEXT NOT NULL DEFAULT 'BBC Business',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`ALTER TABLE phraseup.daily_news ADD COLUMN IF NOT EXISTS summary_ko TEXT NOT NULL DEFAULT ''`,
}

type NewsStory struct {
	Date      string `json:"date"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	SummaryKo string `json:"summary_ko"`
	URL       string `json:"url"`
	ImageURL  string `json:"image_url"`
	Source    string `json:"source"`
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

var (
	reHTMLParagraph = regexp.MustCompile(`(?s)<p[^>]*>(.*?)</p>`)
	reHTMLTag       = regexp.MustCompile(`<[^>]+>`)
)

// fetchArticleText pulls the article page and crudely extracts paragraph
// text — enough raw material for the summarizer, not a rendering-grade
// parse. Returns "" on any failure (the RSS description alone still works).
func fetchArticleText(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		log.Printf("news: article fetch: %v", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 300_000))
	if err != nil || len(body) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, m := range reHTMLParagraph.FindAllStringSubmatch(string(body), -1) {
		text := strings.TrimSpace(reHTMLTag.ReplaceAllString(m[1], ""))
		if len(text) < 40 {
			continue // skip nav/caption fragments
		}
		sb.WriteString(text)
		sb.WriteString("\n")
		if sb.Len() > 8000 {
			break
		}
	}
	return sb.String()
}

// summarizeNews produces a detailed multi-paragraph English summary plus its
// Korean rendering via Haiku (cheap; runs at most once a day since the
// result is cached in daily_news). Falls back to the short RSS description
// (and "" for Korean) on any failure or when no API key is configured.
func summarizeNews(ctx context.Context, title, description, articleText string) (summaryEN, summaryKO string) {
	key := anthropicAPIKey(ctx)
	if key == "" {
		return description, ""
	}

	client := anthropic.NewClient(option.WithAPIKey(key))

	material := description
	if articleText != "" {
		material = description + "\n\nArticle text:\n" + articleText
	}
	prompt := fmt.Sprintf(`Summarize this business news story for English learners.

Title: %s

%s

Write a DETAILED English summary: 10-14 sentences across 2-3 paragraphs, in clear, natural
business English. Cover the key facts, the numbers involved, the background/context, and why
it matters. Separate paragraphs with \n\n. Then write the same summary in natural Korean,
matching the paragraph structure.

Also pick 4-6 useful business-English items FROM THE ARTICLE for learners: a mix of
"vocabulary" (single terms/collocations) and "expression" (useful full sentences or sentence
patterns, adapted to be generally reusable). Each needs a natural Korean translation — a real
Korean gloss, never a transliteration of the English word.

Respond with ONLY a JSON object, no prose, no markdown fences, in exactly this shape:
{"summary_en": "...", "summary_ko": "...", "vocab": [{"english": "...", "korean": "...", "category": "vocabulary"}]}`, title, material)

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     "claude-haiku-4-5",
		MaxTokens: 3800,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		log.Printf("news: summarize: %v", err)
		return description, ""
	}

	var raw strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			raw.WriteString(tb.Text)
		}
	}
	match := reJSONObject.FindString(raw.String())
	if match == "" {
		log.Printf("news: summarize: no JSON in response")
		return description, ""
	}
	var out struct {
		SummaryEN string            `json:"summary_en"`
		SummaryKO string            `json:"summary_ko"`
		Vocab     []generatedPhrase `json:"vocab"`
	}
	if err := json.Unmarshal([]byte(match), &out); err != nil || strings.TrimSpace(out.SummaryEN) == "" {
		log.Printf("news: summarize: parse failed: %v", err)
		return description, ""
	}

	// Feed the extracted items into the phrase pool as Media-section content
	// (quiz Section 2). Best-effort; dupes are dropped by the unique index.
	inserted := 0
	for _, item := range out.Vocab {
		english := strings.TrimSpace(item.English)
		korean := strings.TrimSpace(item.Korean)
		if english == "" || korean == "" {
			continue
		}
		category := item.Category
		if category != "vocabulary" && category != "expression" {
			category = "expression"
		}
		tag, err := db.Exec(ctx, `
			INSERT INTO phraseup.phrases (english_text, korean_text, category, source)
			VALUES ($1, $2, $3, 'news')
			ON CONFLICT (english_text) DO NOTHING
		`, english, korean, category)
		if err == nil {
			inserted += int(tag.RowsAffected())
		}
	}
	if inserted > 0 {
		log.Printf("news: extracted %d media phrases from today's article", inserted)
	}

	return strings.TrimSpace(out.SummaryEN), strings.TrimSpace(out.SummaryKO)
}

// ensureTodayNews returns today's cached story, fetching and caching it on
// the first request of the day (Seoul time). On fetch failure it falls back
// to the most recent cached story rather than erroring the widget.
func ensureTodayNews(ctx context.Context) (NewsStory, error) {
	today := time.Now().In(seoulTZ).Format("2006-01-02")

	var s NewsStory
	var d time.Time
	err := db.QueryRow(ctx, `
		SELECT news_date, title, summary, summary_ko, url, image_url, source
		FROM phraseup.daily_news WHERE news_date = $1
	`, today).Scan(&d, &s.Title, &s.Summary, &s.SummaryKo, &s.URL, &s.ImageURL, &s.Source)
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
			SELECT news_date, title, summary, summary_ko, url, image_url, source
			FROM phraseup.daily_news ORDER BY news_date DESC LIMIT 1
		`).Scan(&d, &s.Title, &s.Summary, &s.SummaryKo, &s.URL, &s.ImageURL, &s.Source)
		if err != nil {
			return NewsStory{}, fetchErr
		}
		s.Date = d.Format("2006-01-02")
		return s, nil
	}

	articleText := fetchArticleText(ctx, fetched.URL)
	fetched.Summary, fetched.SummaryKo = summarizeNews(ctx, fetched.Title, fetched.Summary, articleText)

	if _, err := db.Exec(ctx, `
		INSERT INTO phraseup.daily_news (news_date, title, summary, summary_ko, url, image_url, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (news_date) DO NOTHING
	`, today, fetched.Title, fetched.Summary, fetched.SummaryKo, fetched.URL, fetched.ImageURL, fetched.Source); err != nil {
		log.Printf("news: cache save: %v", err)
	}
	fetched.Date = today
	return fetched, nil
}

// getNewsByDate returns the cached story for a past date. Past days only
// exist from when the app started caching (one row per day) — there's no
// backfill source, so a miss is a normal state the UI must handle.
func getNewsByDate(ctx context.Context, dateStr string) (NewsStory, bool) {
	var s NewsStory
	var d time.Time
	err := db.QueryRow(ctx, `
		SELECT news_date, title, summary, summary_ko, url, image_url, source
		FROM phraseup.daily_news WHERE news_date = $1
	`, dateStr).Scan(&d, &s.Title, &s.Summary, &s.SummaryKo, &s.URL, &s.ImageURL, &s.Source)
	if err != nil {
		return NewsStory{}, false
	}
	s.Date = d.Format("2006-01-02")
	return s, true
}

func registerNewsRoutes(r *gin.Engine) {
	// /api/news?date=YYYY-MM-DD — today (or no date) ensures/fetches; past
	// dates serve the archive only.
	r.GET("/api/news", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		ctx := c.Request.Context()
		today := time.Now().In(seoulTZ).Format("2006-01-02")
		dateStr := c.Query("date")
		if dateStr == "" {
			dateStr = today
		}
		if _, err := time.Parse("2006-01-02", dateStr); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid date"})
			return
		}

		if dateStr == today {
			story, err := ensureTodayNews(ctx)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "news unavailable"})
				return
			}
			c.JSON(http.StatusOK, story)
			return
		}

		story, ok := getNewsByDate(ctx, dateStr)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"date": dateStr, "missing": true})
			return
		}
		c.JSON(http.StatusOK, story)
	})

	// Legacy path kept for any cached frontend still calling it.
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
