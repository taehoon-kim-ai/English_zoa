package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── section: phrase sourcing — no screen of its own. seedStaticPhrasesIfMissing
// bulk-loads the whole curated fallbackPhrases list immediately (not
// trickled one/day — a pool that only grows by one row per calendar day is
// exactly what produced the "only 2 questions" bug this replaces).
// ensureTodayPhrase adds one more from Slack per day on top when configured;
// ai.go tops the pool up further with AI-generated batches. quiz.go is the
// only consumer of the pool.

var phraseSchemaStmts = []string{
	// One phrase per calendar day for the Slack source (phrase_date IS NOT
	// NULL), but the bulk-seeded static list and AI-generated batches (ai.go)
	// aren't tied to a single day, so phrase_date is nullable and only
	// enforced unique among the non-null (daily-source) rows via the partial
	// index below. english_text is unique across the whole table so bulk
	// seeding and AI top-ups can both use ON CONFLICT DO NOTHING freely.
	`CREATE TABLE IF NOT EXISTS phraseup.phrases (
		id              SERIAL PRIMARY KEY,
		english_text    TEXT NOT NULL,
		korean_text     TEXT NOT NULL,
		category        TEXT NOT NULL DEFAULT 'expression' CHECK (category IN ('vocabulary', 'expression')),
		phrase_date     DATE,
		source_slack_ts TEXT UNIQUE,
		created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`ALTER TABLE phraseup.phrases ALTER COLUMN phrase_date DROP NOT NULL`,
	`ALTER TABLE phraseup.phrases DROP CONSTRAINT IF EXISTS phrases_phrase_date_key`,
	`CREATE UNIQUE INDEX IF NOT EXISTS phrases_phrase_date_unique ON phraseup.phrases (phrase_date) WHERE phrase_date IS NOT NULL`,
	`ALTER TABLE phraseup.phrases ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'expression'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS phrases_english_text_unique ON phraseup.phrases (english_text)`,
	// card_attempts belonged to the removed flashcard screen — drop it if a
	// prior deploy created it.
	`DROP TABLE IF EXISTS phraseup.card_attempts`,
}

// seedStaticPhrasesIfMissing bulk-inserts the curated fallbackPhrases list.
// Cheap to call on every request once seeded (single COUNT query short-
// circuits it) — see quiz.go's /api/quiz/today handler.
func seedStaticPhrasesIfMissing(ctx context.Context) error {
	var total int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM phraseup.phrases`).Scan(&total); err != nil {
		return err
	}
	if total >= len(fallbackPhrases) {
		return nil
	}
	for _, p := range fallbackPhrases {
		if _, err := db.Exec(ctx, `
			INSERT INTO phraseup.phrases (english_text, korean_text, category)
			VALUES ($1, $2, $3)
			ON CONFLICT (english_text) DO NOTHING
		`, p.En, p.Ko, p.Category); err != nil {
			return err
		}
	}
	return nil
}

type Phrase struct {
	ID          int    `json:"id"`
	EnglishText string `json:"english_text"`
	KoreanText  string `json:"korean_text"`
	Category    string `json:"category"`
	PhraseDate  string `json:"phrase_date,omitempty"`
}

// ensureTodayPhrase adds one Slack-sourced phrase for today on top of the
// bulk-seeded static pool, if #learning-english-with-ai is configured and
// has a parseable message today. A no-op (zero value, no error) when Slack
// isn't configured — the pool doesn't depend on this to have content.
func ensureTodayPhrase(ctx context.Context) (Phrase, error) {
	seoulNow := time.Now().In(seoulTZ)
	dateStr := seoulNow.Format("2006-01-02")

	var p Phrase
	var d time.Time
	err := db.QueryRow(ctx, `
		SELECT id, english_text, korean_text, category, phrase_date FROM phraseup.phrases WHERE phrase_date = $1
	`, dateStr).Scan(&p.ID, &p.EnglishText, &p.KoreanText, &p.Category, &d)
	if err == nil {
		p.PhraseDate = d.Format("2006-01-02")
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Phrase{}, err
	}

	english, korean, slackTS := fetchPhraseFromSlack(ctx)
	if english == "" {
		return Phrase{}, nil // Slack not configured/nothing parseable today — fine
	}

	// Upsert-select: a no-op UPDATE on conflict lets RETURNING work even when
	// a concurrent request already inserted today's row first.
	err = db.QueryRow(ctx, `
		INSERT INTO phraseup.phrases (english_text, korean_text, category, phrase_date, source_slack_ts)
		VALUES ($1, $2, 'expression', $3, $4)
		ON CONFLICT (phrase_date) WHERE phrase_date IS NOT NULL DO UPDATE SET phrase_date = EXCLUDED.phrase_date
		RETURNING id, english_text, korean_text, category, phrase_date
	`, english, korean, dateStr, slackTS).Scan(&p.ID, &p.EnglishText, &p.KoreanText, &p.Category, &d)
	if err != nil {
		return Phrase{}, err
	}
	p.PhraseDate = d.Format("2006-01-02")
	return p, nil
}

// fallbackPhrases seeds the pool when Slack isn't configured yet or a day's
// message doesn't parse, and backstops the quiz entirely until an Anthropic
// key is configured for ai.go. Mostly business-English "expression" (full
// workplace sentences) with a solid chunk of "vocabulary" (single business
// terms/collocations) mixed in, since both are quizzed differently
// (vocabulary is multiple-choice only — see quiz.go chooseQuestionType).
var fallbackPhrases = []struct{ En, Ko, Category string }{
	// expressions
	{"Let's circle back on this next week.", "이건 다음 주에 다시 논의해요.", "expression"},
	{"Could you walk me through the numbers?", "숫자 좀 설명해 주시겠어요?", "expression"},
	{"I'll loop you in on that thread.", "그 스레드에 참조로 넣어드릴게요.", "expression"},
	{"Let's table this for now.", "이건 일단 보류하죠.", "expression"},
	{"We need to move the needle on this.", "이 부분에서 실질적인 진전을 만들어야 해요.", "expression"},
	{"Can we sync up before the call?", "콜 전에 미리 맞춰볼까요?", "expression"},
	{"I'll follow up with an email recap.", "이메일로 요약해서 다시 보내드릴게요.", "expression"},
	{"Let's take this offline.", "이건 따로 얘기하시죠.", "expression"},
	{"We're on the same page.", "우리 의견이 일치하네요.", "expression"},
	{"Let's put a pin in that.", "그건 잠시 보류해둘게요.", "expression"},
	{"Could you send over the deck?", "발표 자료 좀 보내주시겠어요?", "expression"},
	{"I don't have the bandwidth right now.", "지금은 여유가 없어요.", "expression"},
	{"Let's touch base next week.", "다음 주에 다시 이야기해요.", "expression"},
	{"That's a good catch, thanks.", "잘 짚어주셨네요, 감사해요.", "expression"},
	{"Can you keep me in the loop on this?", "이 건 계속 공유해 주시겠어요?", "expression"},
	{"Let's park that for the next meeting.", "그건 다음 회의로 넘기죠.", "expression"},
	{"I'll get back to you by end of day.", "오늘 안으로 다시 연락드릴게요.", "expression"},
	{"We should align on priorities first.", "먼저 우선순위부터 맞춰야 해요.", "expression"},
	{"Let's not boil the ocean on this one.", "이건 너무 크게 벌이지 말죠.", "expression"},
	{"Can we push the deadline by a few days?", "마감을 며칠만 미룰 수 있을까요?", "expression"},
	{"I want to flag a potential risk here.", "여기 잠재적 리스크 하나 짚고 싶어요.", "expression"},
	{"Let's get everyone on the same call.", "다 같이 한 콜에서 이야기하죠.", "expression"},
	{"We're still ironing out the details.", "아직 세부사항을 조율 중이에요.", "expression"},
	{"Can you loop in legal on this?", "법무팀도 참조해 주시겠어요?", "expression"},
	{"Let's revisit this once we have more data.", "데이터가 더 모이면 다시 검토하죠.", "expression"},
	{"I'll ping you once it's ready.", "준비되면 바로 연락드릴게요.", "expression"},
	{"We're running a bit behind schedule.", "일정이 좀 밀리고 있어요.", "expression"},
	{"Let's set up a quick huddle.", "짧게 모여서 이야기해요.", "expression"},
	{"Can we table a follow-up for Friday?", "금요일에 후속 미팅 잡을까요?", "expression"},
	{"I appreciate you jumping on this so fast.", "이렇게 빨리 대응해 주셔서 감사해요.", "expression"},
	// vocabulary
	{"deadline", "마감 기한", "vocabulary"},
	{"stakeholder", "이해관계자", "vocabulary"},
	{"bandwidth", "(업무를 처리할) 여유, 여력", "vocabulary"},
	{"deliverable", "산출물, 결과물", "vocabulary"},
	{"onboarding", "온보딩, 신규 적응 교육", "vocabulary"},
	{"headcount", "인원 수, 정원", "vocabulary"},
	{"runway", "(자금) 여유 기간", "vocabulary"},
	{"churn", "이탈(률)", "vocabulary"},
	{"turnaround time", "처리 소요 시간", "vocabulary"},
	{"escalate", "상부에 보고하다, 확대하다", "vocabulary"},
	{"leverage", "활용하다", "vocabulary"},
	{"synergy", "시너지", "vocabulary"},
	{"benchmark", "기준, 벤치마크", "vocabulary"},
	{"procurement", "조달, 구매", "vocabulary"},
	{"compliance", "규정 준수", "vocabulary"},
	{"quarterly review", "분기 평가", "vocabulary"},
	{"KPI", "핵심성과지표", "vocabulary"},
	{"roadmap", "로드맵, 계획", "vocabulary"},
	{"budget overrun", "예산 초과", "vocabulary"},
	{"attrition", "인력 이탈, 자연 감소", "vocabulary"},
}

