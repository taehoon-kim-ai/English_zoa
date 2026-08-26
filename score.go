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
// this section owns no table (the retired one is dropped in migrations.go).

func getQuizCorrectCount(ctx context.Context, email string) (int, error) {
	var count int
	err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM phraseup.quiz_questions WHERE email = $1 AND result = 'correct'
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
		FROM phraseup.users u
		LEFT JOIN phraseup.quiz_questions q ON q.email = u.email
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
			warnScan("quiz leaderboard", err)
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
		FROM phraseup.login_events le
		JOIN phraseup.users u ON u.email = le.email
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
			warnScan("streak leaderboard", err)
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

// ── weekly rankings + Friday celebration ────────────────────────────────────
// The competitive week runs Friday 09:00 KST → next Friday 09:00 KST.
// During the week the main page shows live standings; every Friday from
// 09:00 the page celebrates the champions of the week that JUST closed
// (best battle win rate, best streak, most correct answers).

// weekStart returns the most recent Friday 09:00 KST at or before now.
func weekStart(now time.Time) time.Time {
	now = now.In(seoulTZ)
	anchor := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, seoulTZ)
	daysBack := (int(now.Weekday()) - int(time.Friday) + 7) % 7
	anchor = anchor.AddDate(0, 0, -daysBack)
	if anchor.After(now) {
		anchor = anchor.AddDate(0, 0, -7)
	}
	return anchor
}

type BattleRecord struct {
	Nickname string  `json:"nickname"`
	Wins     int     `json:"wins"`
	Losses   int     `json:"losses"`
	WinRate  float64 `json:"win_rate"`
}

// getBattleStandings tallies W/L over [from, to) from finished battles.
func getBattleStandings(ctx context.Context, from, to time.Time, limit int) ([]BattleRecord, error) {
	rows, err := db.Query(ctx, `
		WITH results AS (
			-- legacy 1v1 matches (winner_email set)
			SELECT winner_email AS email, 1 AS win, 0 AS loss
			FROM phraseup.battles
			WHERE status = 'finished' AND finished_at >= $1 AND finished_at < $2 AND winner_email IS NOT NULL
			UNION ALL
			SELECT CASE WHEN winner_email = host_email THEN guest_email ELSE host_email END, 0, 1
			FROM phraseup.battles
			WHERE status = 'finished' AND finished_at >= $1 AND finished_at < $2
			  AND winner_email IS NOT NULL AND guest_email IS NOT NULL
			UNION ALL
			-- team matches (winner_team set; draws count for no one)
			SELECT bp.email,
			       CASE WHEN bp.team =  b.winner_team THEN 1 ELSE 0 END,
			       CASE WHEN bp.team <> b.winner_team THEN 1 ELSE 0 END
			FROM phraseup.battles b
			JOIN phraseup.battle_players bp ON bp.battle_id = b.id
			WHERE b.status = 'finished' AND b.finished_at >= $1 AND b.finished_at < $2
			  AND b.winner_team IN ('left', 'right')
		)
		SELECT u.nickname, SUM(r.win), SUM(r.loss)
		FROM results r JOIN phraseup.users u ON u.email = r.email
		GROUP BY u.nickname
		ORDER BY SUM(r.win)::float / GREATEST(SUM(r.win) + SUM(r.loss), 1) DESC, SUM(r.win) DESC
		LIMIT $3
	`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []BattleRecord{}
	for rows.Next() {
		var r BattleRecord
		if err := rows.Scan(&r.Nickname, &r.Wins, &r.Losses); err != nil {
			warnScan("battle record", err)
			continue
		}
		if r.Wins+r.Losses > 0 {
			r.WinRate = float64(r.Wins) / float64(r.Wins+r.Losses)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

type WeeklyEntry struct {
	Nickname string `json:"nickname"`
	Value    int    `json:"value"`
}

func getWeeklyStreakTop(ctx context.Context, from, to time.Time, limit int) ([]WeeklyEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT u.nickname, MAX(le.streak_count)
		FROM phraseup.login_events le JOIN phraseup.users u ON u.email = le.email
		WHERE le.login_time >= $1 AND le.login_time < $2
		GROUP BY u.nickname ORDER BY MAX(le.streak_count) DESC LIMIT $3
	`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []WeeklyEntry{}
	for rows.Next() {
		var e WeeklyEntry
		if err := rows.Scan(&e.Nickname, &e.Value); err != nil {
			warnScan("weekly streaks", err)
			continue
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func getWeeklyWordsTop(ctx context.Context, from, to time.Time, limit int) ([]WeeklyEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT u.nickname, COUNT(*)
		FROM phraseup.quiz_questions q JOIN phraseup.users u ON u.email = q.email
		WHERE q.result = 'correct' AND q.answered_at >= $1 AND q.answered_at < $2
		GROUP BY u.nickname ORDER BY COUNT(*) DESC LIMIT $3
	`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []WeeklyEntry{}
	for rows.Next() {
		var e WeeklyEntry
		if err := rows.Scan(&e.Nickname, &e.Value); err != nil {
			warnScan("weekly words", err)
			continue
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func registerWeeklyRoutes(r *gin.Engine) {
	r.GET("/api/weekly", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		ctx := c.Request.Context()
		now := time.Now().In(seoulTZ)
		start := weekStart(now)

		battle, err := getBattleStandings(ctx, start, now, 5)
		if err != nil {
			log.Printf("getBattleStandings: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "weekly unavailable"})
			return
		}
		streak, err := getWeeklyStreakTop(ctx, start, now, 5)
		if err != nil {
			log.Printf("getWeeklyStreakTop: %v", err)
			streak = []WeeklyEntry{}
		}
		words, err := getWeeklyWordsTop(ctx, start, now, 5)
		if err != nil {
			log.Printf("getWeeklyWordsTop: %v", err)
			words = []WeeklyEntry{}
		}

		resp := gin.H{
			"week_start": start.Format("2006-01-02 15:04"),
			"live":       gin.H{"battle": battle, "streak": streak, "words": words},
		}

		// Celebration day: every Friday from 09:00 KST until midnight, honor
		// the champions of the week that closed at 09:00 this morning.
		if now.Weekday() == time.Friday && !now.Before(start) && now.Format("2006-01-02") == start.Format("2006-01-02") {
			prevStart := start.AddDate(0, 0, -7)
			cBattle, _ := getBattleStandings(ctx, prevStart, start, 1)
			cStreak, _ := getWeeklyStreakTop(ctx, prevStart, start, 1)
			cWords, _ := getWeeklyWordsTop(ctx, prevStart, start, 1)
			champs := gin.H{}
			if len(cBattle) > 0 {
				champs["battle"] = cBattle[0]
			}
			if len(cStreak) > 0 {
				champs["streak"] = cStreak[0]
			}
			if len(cWords) > 0 {
				champs["words"] = cWords[0]
			}
			if len(champs) > 0 {
				resp["celebration"] = gin.H{
					"week_of":   prevStart.Format("2006-01-02"),
					"champions": champs,
				}
			}
		}
		c.JSON(http.StatusOK, resp)
	})
}
