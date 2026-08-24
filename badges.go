package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ── section: gamification — daily goal + badges. No table of its own: every
// badge is computed live from data other sections already record
// (quiz_questions, login_events), so there's nothing to keep in sync and a
// badge can never be stale. Pairs with the goal ring / badge shelf in
// web/main.jsx and web/profile.jsx.

const dailyGoalQuestions = 10

type Badge struct {
	ID     string `json:"id"`
	Icon   string `json:"icon"`
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Earned bool   `json:"earned"`
}

type DailyGoal struct {
	Answered int `json:"answered"`
	Correct  int `json:"correct"`
	Goal     int `json:"goal"`
}

func getDailyGoal(ctx context.Context, email string) (DailyGoal, error) {
	g := DailyGoal{Goal: dailyGoalQuestions}
	today := time.Now().In(seoulTZ).Format("2006-01-02")
	err := db.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE result = 'correct')
		FROM phraseup.quiz_questions
		WHERE email = $1 AND answered_at IS NOT NULL
		  AND (answered_at AT TIME ZONE 'Asia/Seoul')::date = $2
	`, email, today).Scan(&g.Answered, &g.Correct)
	return g, err
}

func getBadges(ctx context.Context, email string) ([]Badge, error) {
	var totalAnswered, totalCorrect, currentStreak, earlyLogins, perfectSessions int

	if err := db.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE result = 'correct')
		FROM phraseup.quiz_questions
		WHERE email = $1 AND answered_at IS NOT NULL
	`, email).Scan(&totalAnswered, &totalCorrect); err != nil {
		return nil, err
	}

	if err := db.QueryRow(ctx, `
		SELECT COALESCE(MAX(streak_count), 0) FROM phraseup.login_events WHERE email = $1
	`, email).Scan(&currentStreak); err != nil {
		return nil, err
	}

	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM phraseup.login_events
		WHERE email = $1 AND EXTRACT(HOUR FROM login_time AT TIME ZONE 'Asia/Seoul') < 9
	`, email).Scan(&earlyLogins); err != nil {
		return nil, err
	}

	// A perfect session = a fully-answered session of 5+ questions with zero
	// wrong answers.
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT session_id
			FROM phraseup.quiz_questions
			WHERE email = $1
			GROUP BY session_id
			HAVING COUNT(*) >= 5
			   AND COUNT(*) = COUNT(result)
			   AND COUNT(*) = COUNT(*) FILTER (WHERE result = 'correct')
		) perfect
	`, email).Scan(&perfectSessions); err != nil {
		return nil, err
	}

	return []Badge{
		{ID: "first_steps", Icon: "🐣", Name: "First Steps", Desc: "Answer your first question", Earned: totalAnswered >= 1},
		{ID: "sharp_50", Icon: "🎯", Name: "Sharpshooter", Desc: "50 correct answers", Earned: totalCorrect >= 50},
		{ID: "century", Icon: "💪", Name: "Century Club", Desc: "100 questions answered", Earned: totalAnswered >= 100},
		{ID: "perfect", Icon: "💯", Name: "Flawless", Desc: "Finish a quiz with a perfect score", Earned: perfectSessions >= 1},
		{ID: "week_streak", Icon: "🔥", Name: "On Fire", Desc: "7-day login streak", Earned: currentStreak >= 7},
		{ID: "early_bird", Icon: "🌅", Name: "Early Bird", Desc: "Log in before 9 AM", Earned: earlyLogins >= 1},
	}, nil
}

func registerStatsRoutes(r *gin.Engine) {
	r.GET("/api/stats", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		goal, err := getDailyGoal(ctx, email)
		if err != nil {
			log.Printf("getDailyGoal: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "stats unavailable"})
			return
		}
		badges, err := getBadges(ctx, email)
		if err != nil {
			log.Printf("getBadges: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "stats unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"daily_goal": goal, "badges": badges})
	})
}
