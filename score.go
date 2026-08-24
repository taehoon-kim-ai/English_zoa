package main

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: 점수 / 리더보드 — 다른 섹션(phrase.go, quiz.go)이 공유해서 쓴다 ──
// Owns english_zoa.user_scores. Any section that awards points calls
// addScoreTx from inside its own DB transaction.

const (
	streakBonusEvery  = 7  // every Nth consecutive login day...
	streakBonusPoints = 10 // ...awards this many bonus points (profile.go)
	quizCorrectPoints = 2  // per correct quiz question, either type (quiz.go)
)

var scoreSchemaStmts = []string{
	// Summary table for fast leaderboard reads — kept in sync transactionally
	// by each section's scoring code rather than aggregated on every request.
	`CREATE TABLE IF NOT EXISTS english_zoa.user_scores (
		email       TEXT PRIMARY KEY REFERENCES english_zoa.users(email) ON DELETE CASCADE,
		total_score INT NOT NULL DEFAULT 0,
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
}

func getScore(ctx context.Context, email string) (int, error) {
	var score int
	err := db.QueryRow(ctx, `
		SELECT total_score FROM english_zoa.user_scores WHERE email = $1
	`, email).Scan(&score)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return score, err
}

func addScoreTx(ctx context.Context, tx pgx.Tx, email string, delta int) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO english_zoa.user_scores (email, total_score, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (email) DO UPDATE SET
			total_score = english_zoa.user_scores.total_score + $2,
			updated_at  = NOW()
	`, email, delta)
	return err
}

type LeaderboardEntry struct {
	Email      string `json:"email"`
	Nickname   string `json:"nickname"`
	TotalScore int    `json:"total_score"`
}

func getLeaderboard(ctx context.Context, limit int) ([]LeaderboardEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT u.email, u.nickname, s.total_score
		FROM english_zoa.user_scores s
		JOIN english_zoa.users u ON u.email = s.email
		ORDER BY s.total_score DESC, u.nickname ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []LeaderboardEntry{}
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.Email, &e.Nickname, &e.TotalScore); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func registerLeaderboardRoutes(r *gin.Engine) {
	r.GET("/api/leaderboard", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		rows, err := getLeaderboard(c.Request.Context(), 50)
		if err != nil {
			log.Printf("getLeaderboard: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "leaderboard unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"leaderboard": rows})
	})
}
