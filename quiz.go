package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: quiz — two independent tracks, each a user-picked test length ──
// Owns phraseup.quiz_questions. Reads phraseup.phrases (owned by phrase.go,
// topped up by ai.go) but never writes it. Work on this file + web/quiz.jsx
// without touching the other sections.
//
// Vocab track only ever tests category='vocabulary' phrases (single terms —
// multiple_choice only, word-order doesn't make sense for one word) and only
// ever offers vocabulary items as multiple-choice distractors. Phrase track
// only tests category='expression' (full sentences — multiple_choice or
// word_order) with expression-only distractors. Earlier versions picked
// distractors from the whole pool regardless of category, so a vocabulary
// question could show full-sentence wrong answers — that's the bug this
// track split + category-scoped distractor queries fixes.
//
// A "session" is one user-initiated test run (POST /api/quiz/start) of a
// chosen length — not tied to a calendar day, so a session_id (not
// quiz_date) is the grouping key and there's no cap on how many times a day
// someone can test. Each question is still graded exactly once
// (recordQuizAnswer) and "score" is a plain COUNT of correct answers
// (score.go) — wrong answers never subtract from anything.
//
// The correct answer is never sent to the client before grading: multiple-
// choice options carry real phrase ids (fine, since nothing else in the
// response says which one is correct), and word-order chips carry random
// opaque tokens rather than their real position, so "sort the ids" isn't a
// shortcut. The actual answer key lives server-side in `correct_answer`.

var quizSchemaStmts = []string{
	// quiz_attempts belonged to the old "infinite pool, one-shot per phrase"
	// design; the first quiz_questions shape (date-based, single fixed daily
	// set) is superseded by the session-based one below. Both migrations are
	// one-time and guarded so a normal deploy never re-runs them.
	`DROP TABLE IF EXISTS phraseup.quiz_attempts`,
	`DO $$ BEGIN
		IF EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'phraseup' AND table_name = 'quiz_questions' AND column_name = 'quiz_date'
		) THEN
			DROP TABLE phraseup.quiz_questions;
		END IF;
	END $$`,
	`CREATE TABLE IF NOT EXISTS phraseup.quiz_questions (
		id             SERIAL PRIMARY KEY,
		email          TEXT NOT NULL REFERENCES phraseup.users(email) ON DELETE CASCADE,
		session_id     TEXT NOT NULL,
		track          TEXT NOT NULL CHECK (track IN ('vocab', 'phrase')),
		seq            SMALLINT NOT NULL,
		phrase_id      INT NOT NULL REFERENCES phraseup.phrases(id) ON DELETE CASCADE,
		category       TEXT NOT NULL,
		question_type  TEXT NOT NULL CHECK (question_type IN ('multiple_choice', 'word_order')),
		prompt         TEXT NOT NULL,
		options        JSONB NOT NULL,
		correct_answer JSONB NOT NULL,
		result         TEXT CHECK (result IN ('correct', 'incorrect')),
		answered_at    TIMESTAMPTZ,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (email, session_id, seq)
	)`,
}

const (
	trackVocab  = "vocab"
	trackPhrase = "phrase"

	minPhrasesForMultipleChoice = 4 // 1 correct + 3 distractors, same category
)

var (
	vocabCounts  = []int{10, 20, 30}
	phraseCounts = []int{5, 10, 15}
)

func trackCategory(track string) string {
	if track == trackVocab {
		return "vocabulary"
	}
	return "expression"
}

func validTrackCount(track string, count int) bool {
	var allowed []int
	switch track {
	case trackVocab:
		allowed = vocabCounts
	case trackPhrase:
		allowed = phraseCounts
	default:
		return false
	}
	for _, c := range allowed {
		if c == count {
			return true
		}
	}
	return false
}

type QuizOption struct {
	ID         string `json:"id"`
	KoreanText string `json:"korean_text,omitempty"` // multiple_choice
	Text       string `json:"text,omitempty"`        // word_order
}

// quizAnswerKey is stored server-side only (correct_answer column) and never
// serialized into an API response sent before grading.
type quizAnswerKey struct {
	CorrectID string   `json:"correct_id,omitempty"` // multiple_choice
	Order     []string `json:"order,omitempty"`      // word_order, in correct sequence
}

type DailyQuizQuestion struct {
	ID           int          `json:"id"`
	Seq          int          `json:"seq"`
	Category     string       `json:"category"`
	QuestionType string       `json:"question_type"`
	Prompt       string       `json:"prompt"`
	Options      []QuizOption `json:"options"`
	Result       string       `json:"result,omitempty"` // "" | "correct" | "incorrect"
}

// buildWordOrderChips splits English text into shuffled chips with opaque
// ids, plus the correct id order. Pure function (no DB) so it's unit-tested
// directly in quiz_test.go.
func buildWordOrderChips(english string) ([]QuizOption, []string) {
	words := strings.Fields(english)
	ids := make([]string, len(words))
	used := map[string]bool{}
	for i := range words {
		for {
			id := strconv.FormatInt(rand.Int63(), 36)
			if !used[id] {
				used[id] = true
				ids[i] = id
				break
			}
		}
	}
	options := make([]QuizOption, len(words))
	for i, w := range words {
		options[i] = QuizOption{ID: ids[i], Text: w}
	}
	rand.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	correctOrder := append([]string(nil), ids...)
	return options, correctOrder
}

// buildMultipleChoiceQuestion draws its 3 distractors from the SAME category
// as the question phrase — a vocabulary word only ever competes against
// other vocabulary words, never against full-sentence expressions.
func buildMultipleChoiceQuestion(ctx context.Context, phraseID int, category, english, korean string) (prompt string, options []QuizOption, answer quizAnswerKey, err error) {
	rows, err := db.Query(ctx, `
		SELECT id, korean_text FROM phraseup.phrases WHERE id != $1 AND category = $2 ORDER BY random() LIMIT 3
	`, phraseID, category)
	if err != nil {
		return "", nil, quizAnswerKey{}, err
	}
	defer rows.Close()

	correctID := strconv.Itoa(phraseID)
	options = []QuizOption{{ID: correctID, KoreanText: korean}}
	for rows.Next() {
		var id int
		var ko string
		if err := rows.Scan(&id, &ko); err != nil {
			continue
		}
		options = append(options, QuizOption{ID: strconv.Itoa(id), KoreanText: ko})
	}
	if err = rows.Err(); err != nil {
		return "", nil, quizAnswerKey{}, err
	}
	rand.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })

	return english, options, quizAnswerKey{CorrectID: correctID}, nil
}

func buildWordOrderQuestion(english, korean string) (prompt string, options []QuizOption, answer quizAnswerKey) {
	options, order := buildWordOrderChips(english)
	return korean, options, quizAnswerKey{Order: order}
}

// chooseQuestionType picks a type for one phrase. Vocab track is always
// multiple-choice (a single word/term can't make a word-order puzzle).
// Phrase track mixes both when the expression pool is big enough for
// multiple-choice distractors; below that it falls back to word-order
// (which only needs the one phrase it's built from).
func chooseQuestionType(track string, wordCount, categoryTotal int) string {
	if track == trackVocab {
		return "multiple_choice"
	}
	canWordOrder := wordCount >= 2
	canMultipleChoice := categoryTotal >= minPhrasesForMultipleChoice
	switch {
	case canWordOrder && canMultipleChoice:
		if rand.Intn(2) == 0 {
			return "multiple_choice"
		}
		return "word_order"
	case canWordOrder:
		return "word_order"
	default:
		return "multiple_choice"
	}
}

// pickSessionPhraseIDs prefers phrases (within the given category) this user
// has never been quizzed on before (across all past sessions); once that
// runs out it pads with random repeats so a small pool still yields a full
// session.
func pickSessionPhraseIDs(ctx context.Context, email, category string, count int) ([]int, error) {
	rows, err := db.Query(ctx, `
		SELECT p.id FROM phraseup.phrases p
		WHERE p.category = $1 AND NOT EXISTS (
			SELECT 1 FROM phraseup.quiz_questions q WHERE q.email = $2 AND q.phrase_id = p.id
		)
		ORDER BY random()
		LIMIT $3
	`, category, email, count)
	if err != nil {
		return nil, err
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(ids) >= count {
		return ids[:count], nil
	}

	need := count - len(ids)
	padRows, err := db.Query(ctx, `SELECT id FROM phraseup.phrases WHERE category = $1 ORDER BY random() LIMIT $2`, category, need)
	if err != nil {
		return nil, err
	}
	defer padRows.Close()
	for padRows.Next() {
		var id int
		if err := padRows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, padRows.Err()
}

func loadSession(ctx context.Context, email, sessionID string) ([]DailyQuizQuestion, error) {
	rows, err := db.Query(ctx, `
		SELECT id, seq, category, question_type, prompt, options, COALESCE(result, '')
		FROM phraseup.quiz_questions
		WHERE email = $1 AND session_id = $2
		ORDER BY seq
	`, email, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var qs []DailyQuizQuestion
	for rows.Next() {
		var q DailyQuizQuestion
		var optionsRaw []byte
		if err := rows.Scan(&q.ID, &q.Seq, &q.Category, &q.QuestionType, &q.Prompt, &optionsRaw, &q.Result); err != nil {
			continue
		}
		if err := json.Unmarshal(optionsRaw, &q.Options); err != nil {
			continue
		}
		qs = append(qs, q)
	}
	return qs, rows.Err()
}

func newSessionID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + strconv.FormatInt(rand.Int63(), 36)
}

// startQuizSession generates a fresh set of `count` questions for one track
// and returns its session id + questions. Returns ("", nil, nil) — not an
// error — when the track's category has no phrases yet at all.
func startQuizSession(ctx context.Context, email, track string, count int) (string, []DailyQuizQuestion, error) {
	category := trackCategory(track)

	var categoryTotal int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM phraseup.phrases WHERE category = $1`, category).Scan(&categoryTotal); err != nil {
		return "", nil, err
	}
	if categoryTotal == 0 {
		return "", nil, nil
	}

	phraseIDs, err := pickSessionPhraseIDs(ctx, email, category, count)
	if err != nil {
		return "", nil, err
	}

	sessionID := newSessionID()
	for seq, phraseID := range phraseIDs {
		var english, korean string
		if err := db.QueryRow(ctx, `
			SELECT english_text, korean_text FROM phraseup.phrases WHERE id = $1
		`, phraseID).Scan(&english, &korean); err != nil {
			return "", nil, err
		}
		wordCount := len(strings.Fields(english))
		qtype := chooseQuestionType(track, wordCount, categoryTotal)

		var (
			prompt  string
			options []QuizOption
			answer  quizAnswerKey
		)
		if qtype == "multiple_choice" {
			prompt, options, answer, err = buildMultipleChoiceQuestion(ctx, phraseID, category, english, korean)
			if err != nil {
				return "", nil, err
			}
		} else {
			prompt, options, answer = buildWordOrderQuestion(english, korean)
		}

		optionsJSON, err := json.Marshal(options)
		if err != nil {
			return "", nil, err
		}
		answerJSON, err := json.Marshal(answer)
		if err != nil {
			return "", nil, err
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO phraseup.quiz_questions
				(email, session_id, track, seq, phrase_id, category, question_type, prompt, options, correct_answer)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, email, sessionID, track, seq, phraseID, category, qtype, prompt, optionsJSON, answerJSON); err != nil {
			return "", nil, err
		}
	}

	questions, err := loadSession(ctx, email, sessionID)
	return sessionID, questions, err
}

type QuizDayStat struct {
	Date            string `json:"date"`
	VocabAttempted  int    `json:"vocab_attempted"`
	VocabCorrect    int    `json:"vocab_correct"`
	PhraseAttempted int    `json:"phrase_attempted"`
	PhraseCorrect   int    `json:"phrase_correct"`
}

// getQuizHistory returns per-day (Asia/Seoul) attempted/correct counts split
// by track from the last `days` days, newest first — shown on the quiz
// page's sidebar alongside the login streak.
func getQuizHistory(ctx context.Context, email string, days int) ([]QuizDayStat, error) {
	rows, err := db.Query(ctx, `
		SELECT (answered_at AT TIME ZONE 'Asia/Seoul')::date AS day,
		       track,
		       COUNT(*) AS attempted,
		       COUNT(*) FILTER (WHERE result = 'correct') AS correct
		FROM phraseup.quiz_questions
		WHERE email = $1
		  AND answered_at IS NOT NULL
		  AND answered_at >= NOW() - make_interval(days => $2)
		GROUP BY day, track
		ORDER BY day DESC
	`, email, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byDate := map[string]*QuizDayStat{}
	order := []string{}
	for rows.Next() {
		var day time.Time
		var track string
		var attempted, correct int
		if err := rows.Scan(&day, &track, &attempted, &correct); err != nil {
			continue
		}
		date := day.Format("2006-01-02")
		s, ok := byDate[date]
		if !ok {
			s = &QuizDayStat{Date: date}
			byDate[date] = s
			order = append(order, date)
		}
		switch track {
		case trackVocab:
			s.VocabAttempted, s.VocabCorrect = attempted, correct
		case trackPhrase:
			s.PhraseAttempted, s.PhraseCorrect = attempted, correct
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stats := make([]QuizDayStat, 0, len(order))
	for _, date := range order {
		stats = append(stats, *byDate[date])
	}
	return stats, nil
}

func tallyResults(qs []DailyQuizQuestion) (answered, correct int) {
	for _, q := range qs {
		if q.Result != "" {
			answered++
			if q.Result == "correct" {
				correct++
			}
		}
	}
	return
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// wordOrderMatches grades a word-order submission by the WORD SEQUENCE the
// chip ids spell out, not by the id sequence itself. Chip ids are minted
// per-position, so a sentence containing the same word twice has two chips
// that look identical on screen — tapping them in either order must count
// as correct, and comparing raw id sequences (the old behavior) wrongly
// failed one of those orders.
func wordOrderMatches(options []QuizOption, submittedIDs, correctIDs []string) bool {
	if len(submittedIDs) == 0 || len(submittedIDs) != len(correctIDs) {
		return false
	}
	textByID := make(map[string]string, len(options))
	for _, opt := range options {
		textByID[opt.ID] = opt.Text
	}
	for i := range submittedIDs {
		submittedWord, ok1 := textByID[submittedIDs[i]]
		correctWord, ok2 := textByID[correctIDs[i]]
		if !ok1 || !ok2 || submittedWord != correctWord {
			return false
		}
	}
	return true
}

// recordQuizAnswer grades a question exactly once — a question with a
// stored result just returns that result again without touching the count,
// so retries (or a slow double-click) can't be replayed for more credit. A
// wrong answer is simply recorded as "incorrect" and never reduces anything.
// The answer key is returned too (safe now — grading already happened) so
// the frontend can reveal the correct choice/sentence either way.
func recordQuizAnswer(ctx context.Context, email string, questionID int, selectedID string, orderedIDs []string) (correct, alreadyAnswered bool, answer quizAnswerKey, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, false, quizAnswerKey{}, err
	}
	defer tx.Rollback(ctx)

	var qtype string
	var answerRaw, optionsRaw []byte
	var existingResult *string
	err = tx.QueryRow(ctx, `
		SELECT question_type, correct_answer, options, result
		FROM phraseup.quiz_questions
		WHERE id = $1 AND email = $2
		FOR UPDATE
	`, questionID, email).Scan(&qtype, &answerRaw, &optionsRaw, &existingResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, quizAnswerKey{}, fmt.Errorf("question not found")
	}
	if err != nil {
		return false, false, quizAnswerKey{}, err
	}

	if err := json.Unmarshal(answerRaw, &answer); err != nil {
		return false, false, quizAnswerKey{}, err
	}

	switch qtype {
	case "multiple_choice":
		correct = selectedID != "" && selectedID == answer.CorrectID
	case "word_order":
		var options []QuizOption
		if err := json.Unmarshal(optionsRaw, &options); err != nil {
			return false, false, quizAnswerKey{}, err
		}
		correct = wordOrderMatches(options, orderedIDs, answer.Order)
	}

	if existingResult != nil {
		return *existingResult == "correct", true, answer, tx.Commit(ctx)
	}

	result := "incorrect"
	if correct {
		result = "correct"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE phraseup.quiz_questions SET result = $2, answered_at = NOW() WHERE id = $1
	`, questionID, result); err != nil {
		return false, false, quizAnswerKey{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, false, quizAnswerKey{}, err
	}
	return correct, false, answer, nil
}

func registerQuizRoutes(r *gin.Engine) {
	r.POST("/api/quiz/start", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			Track string `json:"track"`
			Count int    `json:"count"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || !validTrackCount(body.Track, body.Count) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid track/count"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		// Feed the pool before building the session: full curated static list
		// (phrase.go, one-time bulk seed), one Slack-sourced phrase for today
		// if configured, and an AI top-up batch if running low (ai.go) — all
		// best-effort.
		if err := seedStaticPhrasesIfMissing(ctx); err != nil {
			log.Printf("seedStaticPhrasesIfMissing: %v", err)
		}
		if _, err := ensureTodayPhrase(ctx); err != nil {
			log.Printf("ensureTodayPhrase: %v", err)
		}
		topUpPhrasePoolIfLow(ctx)

		sessionID, qs, err := startQuizSession(ctx, email, body.Track, body.Count)
		if err != nil {
			log.Printf("startQuizSession: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		if qs == nil {
			c.JSON(http.StatusOK, gin.H{"session_id": "", "questions": []DailyQuizQuestion{}, "message": "No content for this track yet — check back soon."})
			return
		}
		answered, correct := tallyResults(qs)
		c.JSON(http.StatusOK, gin.H{
			"session_id":     sessionID,
			"questions":      qs,
			"total":          len(qs),
			"answered_count": answered,
			"correct_count":  correct,
		})
	})

	r.GET("/api/quiz/session/:id", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		qs, err := loadSession(c.Request.Context(), email, c.Param("id"))
		if err != nil {
			log.Printf("loadSession: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		answered, correct := tallyResults(qs)
		c.JSON(http.StatusOK, gin.H{
			"questions":      qs,
			"total":          len(qs),
			"answered_count": answered,
			"correct_count":  correct,
		})
	})

	r.GET("/api/quiz/history", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		stats, err := getQuizHistory(c.Request.Context(), email, 30)
		if err != nil {
			log.Printf("getQuizHistory: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "history unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"days": stats})
	})

	r.POST("/api/quiz/answer", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			QuestionID int      `json:"question_id"`
			SelectedID string   `json:"selected_id"`
			OrderedIDs []string `json:"ordered_ids"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		correct, alreadyAnswered, answer, err := recordQuizAnswer(ctx, email, body.QuestionID, body.SelectedID, body.OrderedIDs)
		if err != nil {
			log.Printf("recordQuizAnswer: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "answer failed"})
			return
		}
		totalCorrect, err := getQuizCorrectCount(ctx, email)
		if err != nil {
			log.Printf("getQuizCorrectCount: %v", err)
		}
		resp := gin.H{
			"correct":       correct,
			"newly_correct": correct && !alreadyAnswered,
			"correct_count": totalCorrect,
		}
		if answer.CorrectID != "" {
			resp["correct_id"] = answer.CorrectID
		}
		if len(answer.Order) > 0 {
			resp["correct_order"] = answer.Order
		}
		c.JSON(http.StatusOK, resp)
	})
}
