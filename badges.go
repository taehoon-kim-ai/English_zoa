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

type Badge struct {
	ID     string `json:"id"`
	Icon   string `json:"icon"`
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Earned bool   `json:"earned"`
}

type TrackGoal struct {
	Answered int `json:"answered"`
	Correct  int `json:"correct"`
	Goal     int `json:"goal"`
}

type DailyGoal struct {
	Vocab  TrackGoal `json:"vocab"`
	Phrase TrackGoal `json:"phrase"`
}

// getDailyGoal reports today's per-track progress against the user's own
// per-track targets (users.goal_vocab / users.goal_phrase, editable on the
// profile page).
func getDailyGoal(ctx context.Context, email string) (DailyGoal, error) {
	var g DailyGoal
	if err := db.QueryRow(ctx, `
		SELECT goal_vocab, goal_phrase FROM phraseup.users WHERE email = $1
	`, email).Scan(&g.Vocab.Goal, &g.Phrase.Goal); err != nil {
		return g, err
	}

	today := time.Now().In(seoulTZ).Format("2006-01-02")
	rows, err := db.Query(ctx, `
		SELECT track, COUNT(*), COUNT(*) FILTER (WHERE result = 'correct')
		FROM phraseup.quiz_questions
		WHERE email = $1 AND answered_at IS NOT NULL
		  AND (answered_at AT TIME ZONE 'Asia/Seoul')::date = $2
		GROUP BY track
	`, email, today)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var track string
		var answered, correct int
		if err := rows.Scan(&track, &answered, &correct); err != nil {
			continue
		}
		switch track {
		case trackVocab:
			g.Vocab.Answered, g.Vocab.Correct = answered, correct
		case trackPhrase:
			g.Phrase.Answered, g.Phrase.Correct = answered, correct
		}
	}
	return g, rows.Err()
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
