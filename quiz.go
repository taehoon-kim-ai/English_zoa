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

// ── section: quiz — 10 fresh questions a day, multiple-choice + word-order ──
// Owns english_zoa.quiz_questions. Reads english_zoa.phrases (owned by
// phrase.go, topped up by ai.go) but never writes it. Work on this file +
// web/quiz.jsx without touching the other sections.
//
// Each user gets a fixed set of dailyQuizSize questions generated once per
// day (Seoul time) and stored — refreshing the page doesn't reshuffle them,
// and each question is graded exactly once (recordQuizAnswer). "Score" here
// is just the count of correct answers (score.go) — wrong answers are never
// subtracted, there's nothing to subtract from.
//
// The correct answer is never sent to the client before grading: multiple-
// choice options carry real phrase ids (fine, since nothing else in the
// response says which one is correct), and word-order chips carry random
// opaque tokens rather than their real position, so "sort the ids" isn't a
// shortcut. The actual answer key lives server-side in `correct_answer`.

var quizSchemaStmts = []string{
	// quiz_attempts belonged to the old "infinite pool, one-shot per phrase"
	// design — replaced by the fixed daily set below.
	`DROP TABLE IF EXISTS english_zoa.quiz_attempts`,
	`CREATE TABLE IF NOT EXISTS english_zoa.quiz_questions (
		id             SERIAL PRIMARY KEY,
		email          TEXT NOT NULL REFERENCES english_zoa.users(email) ON DELETE CASCADE,
		quiz_date      DATE NOT NULL,
		seq            SMALLINT NOT NULL,
		phrase_id      INT NOT NULL REFERENCES english_zoa.phrases(id) ON DELETE CASCADE,
		category       TEXT NOT NULL DEFAULT 'expression',
		question_type  TEXT NOT NULL CHECK (question_type IN ('multiple_choice', 'word_order')),
		prompt         TEXT NOT NULL,
		options        JSONB NOT NULL,
		correct_answer JSONB NOT NULL,
		result         TEXT CHECK (result IN ('correct', 'incorrect')),
		answered_at    TIMESTAMPTZ,
		UNIQUE (email, quiz_date, seq)
	)`,
	`ALTER TABLE english_zoa.quiz_questions ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'expression'`,
}

const (
	dailyQuizSize               = 10
	minPhrasesForMultipleChoice = 4 // 1 correct + 3 distractors
)

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

func buildMultipleChoiceQuestion(ctx context.Context, phraseID int, english, korean string) (prompt string, options []QuizOption, answer quizAnswerKey, err error) {
	rows, err := db.Query(ctx, `
		SELECT id, korean_text FROM english_zoa.phrases WHERE id != $1 ORDER BY random() LIMIT 3
	`, phraseID)
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

// chooseQuestionType picks a type for one phrase. Word-order needs at least
// two words to be a meaningful puzzle, so single-word vocabulary items are
// always multiple-choice; multiple-choice needs 3 distractors from other
// phrases, so it's only offered once the pool is big enough.
func chooseQuestionType(category string, wordCount, totalPhrases int) string {
	canWordOrder := category != "vocabulary" && wordCount >= 2
	canMultipleChoice := totalPhrases >= minPhrasesForMultipleChoice
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

// pickDailyPhraseIDs prefers phrases this user has never been quizzed on
// before (across all days); once that runs out it pads with random repeats
// so a small team with a small phrase pool still gets a full set of 10.
func pickDailyPhraseIDs(ctx context.Context, email string, count int) ([]int, error) {
	rows, err := db.Query(ctx, `
		SELECT p.id FROM english_zoa.phrases p
		WHERE NOT EXISTS (
			SELECT 1 FROM english_zoa.quiz_questions q WHERE q.email = $1 AND q.phrase_id = p.id
		)
		ORDER BY random()
		LIMIT $2
	`, email, count)
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
	padRows, err := db.Query(ctx, `SELECT id FROM english_zoa.phrases ORDER BY random() LIMIT $1`, need)
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

func loadDailyQuiz(ctx context.Context, email, dateStr string) ([]DailyQuizQuestion, error) {
	rows, err := db.Query(ctx, `
		SELECT id, seq, category, question_type, prompt, options, COALESCE(result, '')
		FROM english_zoa.quiz_questions
		WHERE email = $1 AND quiz_date = $2
		ORDER BY seq
	`, email, dateStr)
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

// ensureDailyQuiz returns today's 10 questions for this user, generating them
// on first request of the day. Returns (nil, nil) when the phrase pool is
// still empty (brand new deploy, Slack/AI not wired up yet, first request).
func ensureDailyQuiz(ctx context.Context, email, dateStr string) ([]DailyQuizQuestion, error) {
	existing, err := loadDailyQuiz(ctx, email, dateStr)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return existing, nil
	}

	var total int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM english_zoa.phrases`).Scan(&total); err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, nil
	}

	phraseIDs, err := pickDailyPhraseIDs(ctx, email, dailyQuizSize)
	if err != nil {
		return nil, err
	}

	for seq, phraseID := range phraseIDs {
		var category, english, korean string
		if err := db.QueryRow(ctx, `
			SELECT category, english_text, korean_text FROM english_zoa.phrases WHERE id = $1
		`, phraseID).Scan(&category, &english, &korean); err != nil {
			return nil, err
		}
		wordCount := len(strings.Fields(english))
		qtype := chooseQuestionType(category, wordCount, total)

		var (
			prompt  string
			options []QuizOption
			answer  quizAnswerKey
		)
		if qtype == "multiple_choice" {
			prompt, options, answer, err = buildMultipleChoiceQuestion(ctx, phraseID, english, korean)
			if err != nil {
				return nil, err
			}
		} else {
			prompt, options, answer = buildWordOrderQuestion(english, korean)
		}

		optionsJSON, err := json.Marshal(options)
		if err != nil {
			return nil, err
		}
		answerJSON, err := json.Marshal(answer)
		if err != nil {
			return nil, err
		}
		if _, err := db.Exec(ctx, `
			INSERT INTO english_zoa.quiz_questions
				(email, quiz_date, seq, phrase_id, category, question_type, prompt, options, correct_answer)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (email, quiz_date, seq) DO NOTHING
		`, email, dateStr, seq, phraseID, category, qtype, prompt, optionsJSON, answerJSON); err != nil {
			return nil, err
		}
	}

	return loadDailyQuiz(ctx, email, dateStr)
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
	var answerRaw []byte
	var existingResult *string
	err = tx.QueryRow(ctx, `
		SELECT question_type, correct_answer, result
		FROM english_zoa.quiz_questions
		WHERE id = $1 AND email = $2
		FOR UPDATE
	`, questionID, email).Scan(&qtype, &answerRaw, &existingResult)
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
		correct = len(orderedIDs) > 0 && slicesEqual(orderedIDs, answer.Order)
	}

	if existingResult != nil {
		return *existingResult == "correct", true, answer, tx.Commit(ctx)
	}

	result := "incorrect"
	if correct {
		result = "correct"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE english_zoa.quiz_questions SET result = $2, answered_at = NOW() WHERE id = $1
	`, questionID, result); err != nil {
		return false, false, quizAnswerKey{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, false, quizAnswerKey{}, err
	}
	return correct, false, answer, nil
}

func registerQuizRoutes(r *gin.Engine) {
	r.GET("/api/quiz/today", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		// Feed the pool before building today's quiz: the full curated
		// static list (phrase.go, one-time bulk seed), one Slack-sourced
		// phrase for today if configured, and an AI top-up batch if the pool
		// is running low (ai.go) — all best-effort, quiz generation proceeds
		// either way.
		if err := seedStaticPhrasesIfMissing(ctx); err != nil {
			log.Printf("seedStaticPhrasesIfMissing: %v", err)
		}
		if _, err := ensureTodayPhrase(ctx); err != nil {
			log.Printf("ensureTodayPhrase: %v", err)
		}
		topUpPhrasePoolIfLow(ctx)

		dateStr := time.Now().In(seoulTZ).Format("2006-01-02")
		qs, err := ensureDailyQuiz(ctx, email, dateStr)
		if err != nil {
			log.Printf("ensureDailyQuiz: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		if qs == nil {
			c.JSON(http.StatusOK, gin.H{"questions": []DailyQuizQuestion{}, "message": "No phrases yet — check back soon."})
			return
		}
		answered, correct := 0, 0
		for _, q := range qs {
			if q.Result != "" {
				answered++
				if q.Result == "correct" {
					correct++
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"questions":      qs,
			"total":          len(qs),
			"answered_count": answered,
			"correct_count":  correct,
		})
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
