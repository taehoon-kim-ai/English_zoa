package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── section: 문구 소스 — 더 이상 자체 화면(오늘의 문구)은 없다. ensureTodayPhrase는
// 매일 하나씩 english_zoa.phrases에 새 문구를 쌓는 내부 파이프라인으로만 쓰이고,
// quiz.go가 이 풀에서 문제를 뽑는다. Work on this file (+ slack.go) for the
// phrase-sourcing pipeline; work on quiz.go for how it's used in the quiz.

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
	// card_attempts belonged to the removed flashcard screen — drop it if a
	// prior deploy created it.
	`DROP TABLE IF EXISTS english_zoa.card_attempts`,
}

type Phrase struct {
	ID          int    `json:"id"`
	EnglishText string `json:"english_text"`
	KoreanText  string `json:"korean_text"`
	PhraseDate  string `json:"phrase_date"`
}

// ensureTodayPhrase makes sure today's phrase exists in the pool, creating it
// on first call of the day. Tries Slack first (fetchPhraseFromSlack,
// slack.go); falls back to a built-in business-English phrase when Slack
// isn't reachable/configured or parsing fails, so the pool keeps growing by
// one phrase a day either way.
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

// fallbackPhrases seeds the pool when Slack isn't configured yet or a day's
// message doesn't parse — picked deterministically by day-of-year so
// everyone on the team gets the same fallback phrase added on a given day.
// Content is business English (meetings, email, status updates) — that's the
// app's whole point, not general idioms.
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
