package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: 개인 페이지 (닉네임/상태메시지/캘린더/로그인·스트릭) ──────────────
// Owns english_zoa.users + english_zoa.login_events. Work on this file +
// web/profile.jsx without touching the other sections.

var profileSchemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS english_zoa.users (
		email          TEXT PRIMARY KEY,
		nickname       TEXT NOT NULL DEFAULT '',
		status_message TEXT NOT NULL DEFAULT '',
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS english_zoa.login_events (
		email        TEXT NOT NULL REFERENCES english_zoa.users(email) ON DELETE CASCADE,
		login_date   DATE NOT NULL,
		login_time   TIMESTAMPTZ NOT NULL,
		streak_count INT NOT NULL DEFAULT 1,
		PRIMARY KEY (email, login_date)
	)`,
}

type Profile struct {
	Email         string `json:"email"`
	Nickname      string `json:"nickname"`
	StatusMessage string `json:"status_message"`
}

func defaultNickname(email string) string {
	local := strings.TrimSpace(strings.SplitN(email, "@", 2)[0])
	if local == "" {
		return "익명"
	}
	return local
}

// ensureUser creates the user + user_scores row on first sight. Idempotent —
// call at the top of any handler that needs the user to already exist.
func ensureUser(ctx context.Context, email string) error {
	if _, err := db.Exec(ctx, `
		INSERT INTO english_zoa.users (email, nickname) VALUES ($1, $2)
		ON CONFLICT (email) DO NOTHING
	`, email, defaultNickname(email)); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		INSERT INTO english_zoa.user_scores (email) VALUES ($1)
		ON CONFLICT (email) DO NOTHING
	`, email)
	return err
}

func getUser(ctx context.Context, email string) (Profile, error) {
	p := Profile{Email: email}
	err := db.QueryRow(ctx, `
		SELECT nickname, status_message FROM english_zoa.users WHERE email = $1
	`, email).Scan(&p.Nickname, &p.StatusMessage)
	return p, err
}

func updateProfile(ctx context.Context, email, nickname, statusMessage string) error {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = defaultNickname(email)
	}
	if r := []rune(nickname); len(r) > 24 {
		nickname = string(r[:24])
	}
	if r := []rune(statusMessage); len(r) > 80 {
		statusMessage = string(r[:80])
	}
	_, err := db.Exec(ctx, `
		UPDATE english_zoa.users SET nickname = $2, status_message = $3 WHERE email = $1
	`, email, nickname, statusMessage)
	return err
}

type LoginEvent struct {
	Date        string `json:"date"`
	Time        string `json:"time"`
	StreakCount int    `json:"streak_count"`
}

// recordLogin records today's first request as a login event (idempotent —
// repeat calls the same day just return the existing streak) and extends the
// streak from yesterday's row if present. Every streakBonusEvery-th
// consecutive day awards a one-time bonus (score.go).
func recordLogin(ctx context.Context, email string, now time.Time) (int, error) {
	seoulNow := now.In(seoulTZ)
	today := seoulNow.Format("2006-01-02")
	yesterday := seoulNow.AddDate(0, 0, -1).Format("2006-01-02")

	var existingStreak int
	err := db.QueryRow(ctx, `
		SELECT streak_count FROM english_zoa.login_events WHERE email = $1 AND login_date = $2
	`, email, today).Scan(&existingStreak)
	if err == nil {
		return existingStreak, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	streak := 1
	var prevStreak int
	err = db.QueryRow(ctx, `
		SELECT streak_count FROM english_zoa.login_events WHERE email = $1 AND login_date = $2
	`, email, yesterday).Scan(&prevStreak)
	if err == nil {
		streak = prevStreak + 1
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO english_zoa.login_events (email, login_date, login_time, streak_count)
		VALUES ($1, $2, $3, $4)
	`, email, today, seoulNow, streak); err != nil {
		return 0, err
	}

	if streak%streakBonusEvery == 0 {
		tx, err := db.Begin(ctx)
		if err != nil {
			return streak, err
		}
		defer tx.Rollback(ctx)
		if err := addScoreTx(ctx, tx, email, streakBonusPoints); err != nil {
			return streak, err
		}
		if err := tx.Commit(ctx); err != nil {
			return streak, err
		}
	}
	return streak, nil
}

func getLoginHistory(ctx context.Context, email string, limit int) ([]LoginEvent, error) {
	rows, err := db.Query(ctx, `
		SELECT login_date, login_time, streak_count
		FROM english_zoa.login_events
		WHERE email = $1
		ORDER BY login_date DESC
		LIMIT $2
	`, email, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []LoginEvent{}
	for rows.Next() {
		var date, loginTime time.Time
		var streak int
		if err := rows.Scan(&date, &loginTime, &streak); err != nil {
			continue
		}
		events = append(events, LoginEvent{
			Date:        date.Format("2006-01-02"),
			Time:        loginTime.In(seoulTZ).Format("15:04"),
			StreakCount: streak,
		})
	}
	return events, rows.Err()
}

func registerProfileRoutes(r *gin.Engine) {
	r.GET("/api/me", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "profile unavailable"})
			return
		}
		streak, err := recordLogin(ctx, email, time.Now())
		if err != nil {
			log.Printf("recordLogin: %v", err)
		}
		profile, err := getUser(ctx, email)
		if err != nil {
			log.Printf("getUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "profile unavailable"})
			return
		}
		score, err := getScore(ctx, email)
		if err != nil {
			log.Printf("getScore: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{
			"email":          email,
			"nickname":       profile.Nickname,
			"status_message": profile.StatusMessage,
			"streak":         streak,
			"score":          score,
		})
	})

	r.POST("/api/profile", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			Nickname      string `json:"nickname"`
			StatusMessage string `json:"status_message"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
			return
		}
		if err := updateProfile(ctx, email, body.Nickname, body.StatusMessage); err != nil {
			log.Printf("updateProfile: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.GET("/api/calendar", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		events, err := getLoginHistory(c.Request.Context(), email, 90)
		if err != nil {
			log.Printf("getLoginHistory: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "calendar unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"events": events})
	})
}
