package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: 오늘의 문구 (플래시카드) ─────────────────────────────────────────
// Owns english_zoa.phrases + english_zoa.card_attempts. Work on this file +
// web/home.jsx (+ slack.go for the Slack sourcing) without touching the
// other sections. quiz.go reads english_zoa.phrases but doesn't write it.

var phraseSchemaStmts = []string{
	// One phrase per day, deduped against the source Slack message so a
	// retry never double-inserts the same day's phrase.
	`CREATE TABLE IF NOT EXISTS english_zoa.phrases (
		id              SERIAL PRIMARY KEY,
		english_text    TEXT NOT NULL,
		korean_text     TEXT NOT NULL,
		phrase_date     DATE NOT NULL UNIQUE,
		source_slack_ts TEXT UNIQUE,
		created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

	// One attempt row per user per phrase — re-flipping updates it in place
	// so score changes are idempotent (see recordAttempt).
	`CREATE TABLE IF NOT EXISTS english_zoa.card_attempts (
		email        TEXT NOT NULL REFERENCES english_zoa.users(email) ON DELETE CASCADE,
		phrase_id    INT NOT NULL REFERENCES english_zoa.phrases(id) ON DELETE CASCADE,
		result       TEXT NOT NULL CHECK (result IN ('known', 'unknown')),
		attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (email, phrase_id)
	)`,
}

type Phrase struct {
	ID          int    `json:"id"`
	EnglishText string `json:"english_text"`
	KoreanText  string `json:"korean_text"`
	PhraseDate  string `json:"phrase_date"`
}

// ensureTodayPhrase returns today's phrase, creating it on first request of
// the day. Tries Slack first (fetchPhraseFromSlack, slack.go); falls back to
// a built-in business-English phrase list when Slack isn't reachable/
// configured or parsing fails, so the flashcard always has something to show.
func ensureTodayPhrase(ctx context.Context) (Phrase, error) {
	seoulNow := time.Now().In(seoulTZ)
	dateStr := seoulNow.Format("2006-01-02")

	var p Phrase
	var d time.Time
	err := db.QueryRow(ctx, `
		SELECT id, english_text, korean_text, phrase_date FROM english_zoa.phrases WHERE phrase_date = $1
	`, dateStr).Scan(&p.ID, &p.EnglishText, &p.KoreanText, &d)
	if err == nil {
		p.PhraseDate = d.Format("2006-01-02")
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Phrase{}, err
	}

	english, korean, slackTS := fetchPhraseFromSlack(ctx)
	if english == "" {
		english, korean = fallbackPhrase(seoulNow)
		slackTS = ""
	}
	var slackTSArg any
	if slackTS != "" {
		slackTSArg = slackTS
	}

	// Upsert-select: a no-op UPDATE on conflict lets RETURNING work even when
	// a concurrent request already inserted today's row first.
	err = db.QueryRow(ctx, `
		INSERT INTO english_zoa.phrases (english_text, korean_text, phrase_date, source_slack_ts)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (phrase_date) DO UPDATE SET phrase_date = EXCLUDED.phrase_date
		RETURNING id, english_text, korean_text, phrase_date
	`, english, korean, dateStr, slackTSArg).Scan(&p.ID, &p.EnglishText, &p.KoreanText, &d)
	if err != nil {
		return Phrase{}, err
	}
	p.PhraseDate = d.Format("2006-01-02")
	return p, nil
}

// getAttempt returns "known"/"unknown", or "" if the user hasn't answered yet.
func getAttempt(ctx context.Context, email string, phraseID int) (string, error) {
	var result string
	err := db.QueryRow(ctx, `
		SELECT result FROM english_zoa.card_attempts WHERE email = $1 AND phrase_id = $2
	`, email, phraseID).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return result, err
}

// recordAttempt upserts the user's answer for a phrase and returns the score
// delta actually applied. Flipping known→unknown or re-answering the same way
// doesn't re-award points — only a transition into "known" scores, and a
// walk-back out of "known" refunds it — so replaying a card can't farm score.
func recordAttempt(ctx context.Context, email string, phraseID int, result string) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var prevResult string
	err = tx.QueryRow(ctx, `
		SELECT result FROM english_zoa.card_attempts WHERE email = $1 AND phrase_id = $2 FOR UPDATE
	`, email, phraseID).Scan(&prevResult)
	hadKnown := err == nil && prevResult == "known"
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO english_zoa.card_attempts (email, phrase_id, result, attempted_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (email, phrase_id) DO UPDATE SET result = EXCLUDED.result, attempted_at = NOW()
	`, email, phraseID, result); err != nil {
		return 0, err
	}

	delta := 0
	switch {
	case result == "known" && !hadKnown:
		delta = correctAnswerPoints
	case result == "unknown" && hadKnown:
		delta = -correctAnswerPoints
	}
	if delta != 0 {
		if err := addScoreTx(ctx, tx, email, delta); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return delta, nil
}

// fallbackPhrases seeds the flashcard when Slack isn't configured yet or a
// day's message doesn't parse — picked deterministically by day-of-year so
// everyone on the team sees the same fallback phrase on a given day. Content
// is business English (meetings, email, status updates) — that's the app's
// whole point, not general idioms.
var fallbackPhrases = []struct{ En, Ko string }{
	{"Let's circle back on this next week.", "이건 다음 주에 다시 논의해요."},
	{"Could you walk me through the numbers?", "숫자 좀 설명해 주시겠어요?"},
	{"I'll loop you in on that thread.", "그 스레드에 참조로 넣어드릴게요."},
	{"Let's table this for now.", "이건 일단 보류하죠."},
	{"We need to move the needle on this.", "이 부분에서 실질적인 진전을 만들어야 해요."},
	{"Can we sync up before the call?", "콜 전에 미리 맞춰볼까요?"},
	{"I'll follow up with an email recap.", "이메일로 요약해서 다시 보내드릴게요."},
	{"Let's take this offline.", "이건 따로 얘기하시죠."},
	{"We're on the same page.", "우리 의견이 일치하네요."},
	{"Let's put a pin in that.", "그건 잠시 보류해둘게요."},
	{"Could you send over the deck?", "발표 자료 좀 보내주시겠어요?"},
	{"I don't have the bandwidth right now.", "지금은 여유가 없어요."},
	{"Let's touch base next week.", "다음 주에 다시 이야기해요."},
	{"That's a good catch, thanks.", "잘 짚어주셨네요, 감사해요."},
}

func fallbackPhrase(now time.Time) (string, string) {
	p := fallbackPhrases[now.YearDay()%len(fallbackPhrases)]
	return p.En, p.Ko
}

func registerPhraseRoutes(r *gin.Engine) {
	r.GET("/api/phrase/today", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		phrase, err := ensureTodayPhrase(ctx)
		if err != nil {
			log.Printf("ensureTodayPhrase: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "phrase unavailable"})
			return
		}
		attempt, err := getAttempt(ctx, email, phrase.ID)
		if err != nil {
			log.Printf("getAttempt: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"phrase": phrase, "attempt": attempt})
	})

	r.POST("/api/phrase/attempt", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			PhraseID int    `json:"phrase_id"`
			Result   string `json:"result"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || (body.Result != "known" && body.Result != "unknown") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "attempt failed"})
			return
		}
		scoreDelta, err := recordAttempt(ctx, email, body.PhraseID, body.Result)
		if err != nil {
			log.Printf("recordAttempt: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "attempt failed"})
			return
		}
		score, err := getScore(ctx, email)
		if err != nil {
			log.Printf("getScore: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "score_delta": scoreDelta, "score": score})
	})
}
