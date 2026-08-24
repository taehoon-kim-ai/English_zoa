package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ── section: TED Talk of the day — main page hero. No table of its own —
// a fixed curated list rotated deterministically by date, so "today's talk"
// is the same for the whole team and any past date is reproducible without
// persisting anything. Video ids below were verified against ted.com/
// youtube.com search results before hardcoding (never guess a video id —
// a wrong one just embeds a broken/wrong player).

type TEDTalk struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
	Speaker string `json:"speaker"`
}

var tedTalks = []TEDTalk{
	{"eIho2S0ZahI", "How to speak so that people want to listen", "Julian Treasure"},
	{"Ks-_Mh1QhMc", "Your body language may shape who you are", "Amy Cuddy"},
	{"qp0HIF3SfI4", "How great leaders inspire action", "Simon Sinek"},
	{"R1vskiVDwl4", "10 ways to have a better conversation", "Celeste Headlee"},
	{"rrkrvAUbU9Y", "The puzzle of motivation", "Dan Pink"},
	{"iCvmsMzlF7o", "The power of vulnerability", "Brené Brown"},
	{"c0KYU2j0TM4", "The power of introverts", "Susan Cain"},
	{"1nYFpuc2Umk", "The secret structure of great talks", "Nancy Duarte"},
	{"18uDutylDa4", "Why we have too few women leaders", "Sheryl Sandberg"},
	{"bNpx7gpSqbY", "The single biggest reason why start-ups succeed", "Bill Gross"},
	{"lmyZMtPVodo", "Why good leaders make you feel safe", "Simon Sinek"},
	{"3boKz0Exros", "How to turn a group of strangers into a team", "Amy Edmondson"},
	{"H14bBuluwB8", "Grit: the power of passion and perseverance", "Angela Duckworth"},
	{"fxbCHn6gE3U", "The surprising habits of original thinkers", "Adam Grant"},
	{"fLJsdqxnZb0", "The happy secret to better work", "Shawn Achor"},
	{"PY_kd46RfVE", "Dare to disagree", "Margaret Heffernan"},
	{"RcGyVTAoXEU", "How to make stress your friend", "Kelly McGonigal"},
	{"RNlLRIqNgHo", "Your elusive creative genius", "Elizabeth Gilbert"},
}

// tedTalkEpoch anchors the rotation so "today's talk" is reproducible for
// any date (past or future) without storing anything — day N since this
// anchor always maps to the same list index.
var tedTalkEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func tedTalkForDate(date time.Time) TEDTalk {
	days := int(date.Sub(tedTalkEpoch).Hours() / 24)
	idx := days % len(tedTalks)
	if idx < 0 {
		idx += len(tedTalks)
	}
	return tedTalks[idx]
}

// tedtalk_comments is keyed by video_id, not by date — the same talk repeats
// every len(tedTalks) days, and its discussion thread reappears with it
// rather than starting over each time it airs.
var tedtalkSchemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS phraseup.tedtalk_comments (
		id         SERIAL PRIMARY KEY,
		video_id   TEXT NOT NULL,
		email      TEXT NOT NULL REFERENCES phraseup.users(email) ON DELETE CASCADE,
		body       TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS tedtalk_comments_video_id_idx ON phraseup.tedtalk_comments (video_id)`,
}

type TEDTalkComment struct {
	ID        int    `json:"id"`
	Email     string `json:"email"`
	Nickname  string `json:"nickname"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

func getTedTalkComments(ctx context.Context, videoID string) ([]TEDTalkComment, error) {
	rows, err := db.Query(ctx, `
		SELECT c.id, c.email, u.nickname, c.body, c.created_at
		FROM phraseup.tedtalk_comments c
		JOIN phraseup.users u ON u.email = c.email
		WHERE c.video_id = $1
		ORDER BY c.created_at ASC
	`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	comments := []TEDTalkComment{}
	for rows.Next() {
		var c TEDTalkComment
		var createdAt time.Time
		if err := rows.Scan(&c.ID, &c.Email, &c.Nickname, &c.Body, &createdAt); err != nil {
			continue
		}
		c.CreatedAt = createdAt.In(seoulTZ).Format("2006-01-02 15:04")
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

func addTedTalkComment(ctx context.Context, videoID, email, body string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO phraseup.tedtalk_comments (video_id, email, body) VALUES ($1, $2, $3)
	`, videoID, email, body)
	return err
}

func registerTedTalkRoutes(r *gin.Engine) {
	r.GET("/api/tedtalk", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		dateStr := c.Query("date")
		var date time.Time
		if dateStr == "" {
			date = time.Now().In(seoulTZ)
		} else {
			parsed, err := time.ParseInLocation("2006-01-02", dateStr, seoulTZ)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid date"})
				return
			}
			date = parsed
		}
		talk := tedTalkForDate(date)
		c.JSON(http.StatusOK, gin.H{
			"date":     date.Format("2006-01-02"),
			"video_id": talk.VideoID,
			"title":    talk.Title,
			"speaker":  talk.Speaker,
		})
	})

	r.GET("/api/tedtalk/comments", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		videoID := c.Query("video_id")
		if videoID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "video_id required"})
			return
		}
		comments, err := getTedTalkComments(c.Request.Context(), videoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "comments unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"comments": comments})
	})

	r.POST("/api/tedtalk/comments", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			VideoID string `json:"video_id"`
			Body    string `json:"body"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.VideoID == "" || strings.TrimSpace(body.Body) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "comment failed"})
			return
		}
		text := strings.TrimSpace(body.Body)
		if r := []rune(text); len(r) > 1000 {
			text = string(r[:1000])
		}
		if err := addTedTalkComment(ctx, body.VideoID, email, text); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "comment failed"})
			return
		}
		comments, err := getTedTalkComments(ctx, body.VideoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "comments unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"comments": comments})
	})
}
