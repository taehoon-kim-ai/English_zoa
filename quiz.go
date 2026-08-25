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
		user_answer    JSONB,
		result         TEXT CHECK (result IN ('correct', 'incorrect')),
		answered_at    TIMESTAMPTZ,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (email, session_id, seq)
	)`,
	`ALTER TABLE phraseup.quiz_questions ADD COLUMN IF NOT EXISTS user_answer JSONB`,
	// 'review' sessions (mistake practice) reuse the same table with a third
	// track value — widen the CHECK constraint that predates it.
	`ALTER TABLE phraseup.quiz_questions DROP CONSTRAINT IF EXISTS quiz_questions_track_check`,
	`ALTER TABLE phraseup.quiz_questions ADD CONSTRAINT quiz_questions_track_check CHECK (track IN ('vocab', 'phrase', 'review'))`,
}

const (
	trackVocab  = "vocab"
	trackPhrase = "phrase"
	trackReview = "review" // mistake practice — phrases whose latest answer was wrong

	reviewSessionMax = 10 // a review session covers up to this many mistakes

	minPhrasesForMultipleChoice = 4 // 1 correct + 3 distractors, same category
)

var (
	vocabCounts  = []int{10, 20, 30}
	phraseCounts = []int{5, 10, 15}
)

// sourceFilter returns the SQL source list for a quiz section: "core" is
// the word bank (curated + AI + Slack), "media" is content extracted from
// TED talks / the daily news article.
func sourceGroupList(group string) []string {
	if group == "media" {
		return []string{"news", "ted"}
	}
	return []string{"curated", "ai", "slack"}
}

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
	case trackReview:
		return true // size is server-decided (min(reviewSessionMax, mistakes available))
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

// userSubmission is what the user actually answered (user_answer column),
// stored at grading time for the review screen.
type userSubmission struct {
	SelectedID string   `json:"selected_id,omitempty"` // multiple_choice
	OrderedIDs []string `json:"ordered_ids,omitempty"` // word_order
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
func pickSessionPhraseIDs(ctx context.Context, email, category string, sources []string, count int) ([]int, error) {
	rows, err := db.Query(ctx, `
		SELECT p.id FROM phraseup.phrases p
		WHERE p.category = $1 AND p.source = ANY($4) AND NOT EXISTS (
			SELECT 1 FROM phraseup.quiz_questions q WHERE q.email = $2 AND q.phrase_id = p.id
		)
		ORDER BY random()
		LIMIT $3
	`, category, email, count, sources)
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
	padRows, err := db.Query(ctx, `SELECT id FROM phraseup.phrases WHERE category = $1 AND source = ANY($3) ORDER BY random() LIMIT $2`, category, need, sources)
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

// pickMistakePhraseIDs returns phrases whose LATEST graded answer (across
// all sessions and both tracks) was incorrect — answering one correctly in a
// later session removes it from the mistake pool automatically.
func pickMistakePhraseIDs(ctx context.Context, email string, limit int) ([]int, error) {
	rows, err := db.Query(ctx, `
		SELECT phrase_id FROM (
			SELECT DISTINCT ON (phrase_id) phrase_id, result
			FROM phraseup.quiz_questions
			WHERE email = $1 AND result IS NOT NULL
			ORDER BY phrase_id, answered_at DESC
		) latest
		WHERE result = 'incorrect'
		ORDER BY random()
		LIMIT $2
	`, email, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
// and returns its session id + questions. The review track ignores `count`
// and covers up to reviewSessionMax of the user's current mistakes (mixed
// categories). Returns ("", nil, nil) — not an error — when there's no
// content to build from (empty category pool, or no mistakes to review).
func startQuizSession(ctx context.Context, email, track, sourceGroup string, count int) (string, []DailyQuizQuestion, error) {
	var phraseIDs []int
	var err error

	if track == trackReview {
		phraseIDs, err = pickMistakePhraseIDs(ctx, email, reviewSessionMax)
	} else {
		category := trackCategory(track)
		sources := sourceGroupList(sourceGroup)
		var categoryTotal int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM phraseup.phrases WHERE category = $1 AND source = ANY($2)`, category, sources).Scan(&categoryTotal); err != nil {
			return "", nil, err
		}
		if categoryTotal == 0 {
			return "", nil, nil
		}
		phraseIDs, err = pickSessionPhraseIDs(ctx, email, category, sources, count)
	}
	if err != nil {
		return "", nil, err
	}
	if len(phraseIDs) == 0 {
		return "", nil, nil
	}

	sessionID := newSessionID()
	for seq, phraseID := range phraseIDs {
		var category, english, korean string
		if err := db.QueryRow(ctx, `
			SELECT category, english_text, korean_text FROM phraseup.phrases WHERE id = $1
		`, phraseID).Scan(&category, &english, &korean); err != nil {
			return "", nil, err
		}

		// The review track mixes categories, so pick the question type by
		// the phrase's own category (vocabulary can't be a word-order
		// puzzle); distractor availability is checked per category.
		typeTrack := track
		if track == trackReview {
			typeTrack = trackPhrase
			if category == "vocabulary" {
				typeTrack = trackVocab
			}
		}
		var categoryTotal int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM phraseup.phrases WHERE category = $1`, category).Scan(&categoryTotal); err != nil {
			return "", nil, err
		}
		wordCount := len(strings.Fields(english))
		qtype := chooseQuestionType(typeTrack, wordCount, categoryTotal)

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

type QuizSessionSummary struct {
	SessionID string `json:"session_id"`
	Track     string `json:"track"`
	StartedAt string `json:"started_at"`
	Total     int    `json:"total"`
	Answered  int    `json:"answered"`
	Correct   int    `json:"correct"`
}

func getQuizSessions(ctx context.Context, email string, limit int) ([]QuizSessionSummary, error) {
	rows, err := db.Query(ctx, `
		SELECT session_id, track, MIN(created_at) AS started,
		       COUNT(*), COUNT(result), COUNT(*) FILTER (WHERE result = 'correct')
		FROM phraseup.quiz_questions
		WHERE email = $1
		GROUP BY session_id, track
		ORDER BY started DESC
		LIMIT $2
	`, email, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := []QuizSessionSummary{}
	for rows.Next() {
		var s QuizSessionSummary
		var started time.Time
		if err := rows.Scan(&s.SessionID, &s.Track, &started, &s.Total, &s.Answered, &s.Correct); err != nil {
			continue
		}
		s.StartedAt = started.In(seoulTZ).Format("2006-01-02 15:04")
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

type QuizReviewItem struct {
	Seq          int    `json:"seq"`
	Category     string `json:"category"`
	QuestionType string `json:"question_type"`
	Prompt       string `json:"prompt"`
	Result       string `json:"result"` // "" = never answered
	CorrectText  string `json:"correct_text"`
	YourText     string `json:"your_text"` // "" for unanswered or pre-feature rows
}

// resolveAnswerText renders an answer key / submission as display text —
// the Korean meaning for multiple choice, the assembled sentence for
// word order. Pure function, unit-tested in quiz_test.go.
func resolveAnswerText(qtype string, options []QuizOption, selectedID string, orderedIDs []string) string {
	textByID := make(map[string]string, len(options))
	koByID := make(map[string]string, len(options))
	for _, opt := range options {
		textByID[opt.ID] = opt.Text
		koByID[opt.ID] = opt.KoreanText
	}
	if qtype == "multiple_choice" {
		return koByID[selectedID]
	}
	words := make([]string, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		if w, ok := textByID[id]; ok {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

// getSessionReview returns a finished (or partial) session with the correct
// answer and the user's own answer resolved to display text. Correct answers
// are only revealed for questions that have been graded — an unanswered
// question in an abandoned session stays hidden so the session could still
// be finished honestly later.
func getSessionReview(ctx context.Context, email, sessionID string) ([]QuizReviewItem, error) {
	rows, err := db.Query(ctx, `
		SELECT seq, category, question_type, prompt, options, correct_answer,
		       COALESCE(result, ''), user_answer
		FROM phraseup.quiz_questions
		WHERE email = $1 AND session_id = $2
		ORDER BY seq
	`, email, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []QuizReviewItem{}
	for rows.Next() {
		var item QuizReviewItem
		var optionsRaw, answerRaw []byte
		var userRaw []byte
		if err := rows.Scan(&item.Seq, &item.Category, &item.QuestionType, &item.Prompt,
			&optionsRaw, &answerRaw, &item.Result, &userRaw); err != nil {
			continue
		}
		if item.Result != "" {
			var options []QuizOption
			var answer quizAnswerKey
			if json.Unmarshal(optionsRaw, &options) == nil && json.Unmarshal(answerRaw, &answer) == nil {
				item.CorrectText = resolveAnswerText(item.QuestionType, options, answer.CorrectID, answer.Order)
				if len(userRaw) > 0 {
					var sub userSubmission
					if json.Unmarshal(userRaw, &sub) == nil {
						item.YourText = resolveAnswerText(item.QuestionType, options, sub.SelectedID, sub.OrderedIDs)
					}
				}
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
	// Store what the user actually submitted so past sessions can be
	// reviewed later (quiz review screen).
	userAnswerJSON, err := json.Marshal(userSubmission{SelectedID: selectedID, OrderedIDs: orderedIDs})
	if err != nil {
		return false, false, quizAnswerKey{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE phraseup.quiz_questions SET result = $2, user_answer = $3, answered_at = NOW() WHERE id = $1
	`, questionID, result, userAnswerJSON); err != nil {
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
			Track  string `json:"track"`
			Count  int    `json:"count"`
			Source string `json:"source"` // "core" (default) | "media"
		}
		if err := c.ShouldBindJSON(&body); err != nil || !validTrackCount(body.Track, body.Count) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid track/count"})
			return
		}
		if body.Source != "media" {
			body.Source = "core"
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		// Feed the pool before building the session: full curated static list
		// (phrase.go, one-time bulk seed), one Slack-sourced phrase for today
		// if configured, and — if this user has fewer unseen items in the
		// track than the session needs — a synchronous AI generation so the
		// session doesn't fill up with repeats (ai.go). All best-effort.
		if err := seedStaticPhrasesIfMissing(ctx); err != nil {
			log.Printf("seedStaticPhrasesIfMissing: %v", err)
		}
		if _, err := ensureTodayPhrase(ctx); err != nil {
			log.Printf("ensureTodayPhrase: %v", err)
		}
		if body.Track != trackReview && body.Source == "core" {
			// Media content only grows from real TED/news material — no AI
			// filler there, so top-ups apply to the word bank only.
			ensureFreshContent(ctx, email, trackCategory(body.Track), body.Count)
		}

		sessionID, qs, err := startQuizSession(ctx, email, body.Track, body.Source, body.Count)
		if err != nil {
			log.Printf("startQuizSession: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		if qs == nil {
			msg := "No content for this track yet — check back soon."
			if body.Track == trackReview {
				msg = "No mistakes to review — everything you've missed has been re-answered correctly. 🎉"
			} else if body.Source == "media" {
				msg = "No media content yet — words are collected from each day's news article, so this section grows daily."
			}
			c.JSON(http.StatusOK, gin.H{"session_id": "", "questions": []DailyQuizQuestion{}, "message": msg})
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

	r.GET("/api/quiz/sessions", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		sessions, err := getQuizSessions(c.Request.Context(), email, 20)
		if err != nil {
			log.Printf("getQuizSessions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "sessions unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sessions": sessions})
	})

	r.GET("/api/quiz/session/:id/review", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		items, err := getSessionReview(c.Request.Context(), email, c.Param("id"))
		if err != nil {
			log.Printf("getSessionReview: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "review unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
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
