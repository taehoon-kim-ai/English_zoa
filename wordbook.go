package main

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ── section: word of the day — pairs with the wordbook card in web/quiz.jsx.
// Two jobs: (1) make sure genuinely NEW vocabulary lands in the pool every
// day (so quizzes and battles stop cycling the same words), and (2) serve a
// 100-word daily study list (word — meaning), newest first, with today's
// fresh drop marked NEW.
//
// The daily drop is lazy: the first /api/wordbook (or quiz start) of the
// day that notices fewer than wordbookDailyNew of today's AI words kicks
// off ONE background generation (claude-opus-5, the same generator quizzes
// use). Best-effort — with no API key the list still serves the existing
// pool. Owns no table (reads/writes phraseup.phrases, source='ai').

const (
	wordbookSize     = 100 // study list length
	wordbookDailyNew = 20  // fresh vocabulary items per day
)

var wordbookDropMu sync.Mutex
var wordbookDropDay string // Seoul day a drop is running/done for (in-process throttle)

// ensureDailyWordDrop tops the pool up with today's fresh vocabulary once
// per day. Runs in the caller's goroutine (call it with `go` for fire-and-
// forget); cross-instance duplication is tolerable — the ON CONFLICT guard
// keeps the pool deduplicated either way.
func ensureDailyWordDrop(ctx context.Context) {
	today := time.Now().In(seoulTZ).Format("2006-01-02")

	wordbookDropMu.Lock()
	if wordbookDropDay == today {
		wordbookDropMu.Unlock()
		return
	}
	wordbookDropDay = today
	wordbookDropMu.Unlock()

	var todayCount int
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM phraseup.phrases
		WHERE category = 'vocabulary' AND source = 'ai'
		  AND (created_at AT TIME ZONE 'Asia/Seoul')::date = $1
	`, today).Scan(&todayCount); err != nil {
		log.Printf("wordbook: count today's drop: %v", err)
		return
	}
	if todayCount >= wordbookDailyNew {
		return
	}

	avoid := []string{}
	rows, err := db.Query(ctx, `
		SELECT english_text FROM phraseup.phrases
		WHERE category = 'vocabulary' ORDER BY id DESC LIMIT 60
	`)
	if err == nil {
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err == nil {
				avoid = append(avoid, text)
			}
		}
		rows.Close()
	}

	items, err := generateBusinessEnglishBatch(ctx, "vocabulary", wordbookDailyNew-todayCount, avoid)
	if err != nil {
		log.Printf("wordbook: daily drop: %v", err)
		return
	}
	inserted := 0
	for _, item := range items {
		tag, err := db.Exec(ctx, `
			INSERT INTO phraseup.phrases (english_text, korean_text, category, source)
			VALUES ($1, $2, 'vocabulary', 'ai')
			ON CONFLICT (english_text) DO NOTHING
		`, item.English, item.Korean)
		if err == nil && tag.RowsAffected() > 0 {
			inserted++
		}
	}
	if inserted > 0 {
		log.Printf("wordbook: daily drop added %d new words", inserted)
	}
}

type WordbookEntry struct {
	English string `json:"english"`
	Korean  string `json:"korean"`
	IsNew   bool   `json:"is_new"` // landed today (Seoul)
}

func registerWordbookRoutes(r *gin.Engine) {
	r.GET("/api/wordbook", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		ctx := c.Request.Context()
		if err := seedStaticPhrasesIfMissing(ctx); err != nil {
			log.Printf("seedStaticPhrasesIfMissing: %v", err)
		}
		// Kick today's drop in the background; today's list serves what's
		// already there and picks the new words up on the next visit.
		go ensureDailyWordDrop(context.WithoutCancel(ctx))

		today := time.Now().In(seoulTZ).Format("2006-01-02")
		rows, err := db.Query(ctx, `
			SELECT english_text, korean_text,
			       (created_at AT TIME ZONE 'Asia/Seoul')::date = $1
			FROM phraseup.phrases
			WHERE category = 'vocabulary'
			ORDER BY created_at DESC, id DESC
			LIMIT $2
		`, today, wordbookSize)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "wordbook unavailable"})
			return
		}
		defer rows.Close()

		words := []WordbookEntry{}
		for rows.Next() {
			var w WordbookEntry
			if err := rows.Scan(&w.English, &w.Korean, &w.IsNew); err != nil {
				warnScan("wordbook", err)
				continue
			}
			words = append(words, w)
		}
		c.JSON(http.StatusOK, gin.H{"words": words})
	})
}
