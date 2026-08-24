package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ── section: leaderboards — reads profile.go's login_events and quiz.go's
// quiz_questions but owns no table of its own. Quiz score and daily streak
// are deliberately kept as two separate, independent stats (not blended into
// one number): the quiz leaderboard ranks by total correct answers ever, and
// the streak leaderboard ranks by each person's best current-month streak.
// Wrong answers never subtract from anything — there is nothing to subtract
// from, since "score" here is a plain COUNT of correct answers.

// The old points system (user_scores) is gone — quiz "score" is now a plain
// COUNT over quiz_questions and streak comes straight from login_events, so
// this section owns no table, just a cleanup of the retired one.
var scoreSchemaStmts = []string{
	`DROP TABLE IF EXISTS english_zoa.user_scores`,
}

func getQuizCorrectCount(ctx context.Context, email string) (int, error) {
	var count int
	err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM english_zoa.quiz_questions WHERE email = $1 AND result = 'correct'
	`, email).Scan(&count)
	return count, err
}

type QuizLeaderboardEntry struct {
	Email        string `json:"email"`
	Nickname     string `json:"nickname"`
	CorrectCount int    `json:"correct_count"`
}

func getQuizLeaderboard(ctx context.Context, limit int) ([]QuizLeaderboardEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT u.email, u.nickname, COUNT(*) FILTER (WHERE q.result = 'correct') AS correct_count
		FROM english_zoa.users u
		LEFT JOIN english_zoa.quiz_questions q ON q.email = u.email
		GROUP BY u.email, u.nickname
		ORDER BY correct_count DESC, u.nickname ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []QuizLeaderboardEntry{}
	for rows.Next() {
		var e QuizLeaderboardEntry
		if err := rows.Scan(&e.Email, &e.Nickname, &e.CorrectCount); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

type StreakLeaderboardEntry struct {
	Email      string `json:"email"`
	Nickname   string `json:"nickname"`
	BestStreak int    `json:"best_streak"`
}

// getStreakLeaderboard ranks by each user's best streak_count logged so far
// THIS calendar month (Asia/Seoul) — a running streak carries value earned
// in prior months too (same as any "current streak" stat), it just resets
// the leaderboard window monthly so it stays a fresh competition.
func getStreakLeaderboard(ctx context.Context, limit int) ([]StreakLeaderboardEntry, error) {
	now := time.Now().In(seoulTZ)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, seoulTZ).Format("2006-01-02")

	rows, err := db.Query(ctx, `
		SELECT u.email, u.nickname, MAX(le.streak_count) AS best_streak
		FROM english_zoa.login_events le
		JOIN english_zoa.users u ON u.email = le.email
		WHERE le.login_date >= $1
		GROUP BY u.email, u.nickname
		ORDER BY best_streak DESC, u.nickname ASC
		LIMIT $2
	`, monthStart, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []StreakLeaderboardEntry{}
	for rows.Next() {
		var e StreakLeaderboardEntry
		if err := rows.Scan(&e.Email, &e.Nickname, &e.BestStreak); err != nil {
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
		ctx := c.Request.Context()
		quiz, err := getQuizLeaderboard(ctx, 50)
		if err != nil {
			log.Printf("getQuizLeaderboard: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "leaderboard unavailable"})
			return
		}
		streak, err := getStreakLeaderboard(ctx, 50)
		if err != nil {
			log.Printf("getStreakLeaderboard: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "leaderboard unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"quiz": quiz, "streak": streak})
	})
}
