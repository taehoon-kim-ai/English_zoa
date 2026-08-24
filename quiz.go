package main

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: 퀴즈 (지난 문구 4지선다) ─────────────────────────────────────────
// Owns english_zoa.quiz_attempts. Reads english_zoa.phrases (owned by
// phrase.go) but never writes it. Work on this file + web/quiz.jsx without
// touching the other sections.
//
// One scored attempt per user per phrase, like card_attempts — but unlike
// the flashcard's togglable know/don't-know, a quiz answer is one-shot: once
// you've seen the correct answer, re-answering the same question is practice
// only (see recordQuizAnswer) so score can't be farmed by retrying.

var quizSchemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS english_zoa.quiz_attempts (
		email        TEXT NOT NULL REFERENCES english_zoa.users(email) ON DELETE CASCADE,
		phrase_id    INT NOT NULL REFERENCES english_zoa.phrases(id) ON DELETE CASCADE,
		correct      BOOLEAN NOT NULL,
		attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (email, phrase_id)
	)`,
}

// minPhrasesForQuiz is how many distinct phrases must exist before a
// 4-option question can be built (1 correct + 3 distractors).
const minPhrasesForQuiz = 4

type QuizOption struct {
	ID         int    `json:"id"`
	KoreanText string `json:"korean_text"`
}

type QuizQuestion struct {
	PhraseID    int          `json:"phrase_id"`
	EnglishText string       `json:"english_text"`
	Options     []QuizOption `json:"options"`
	Scored      bool         `json:"scored"`
}

// pickQuizQuestion picks a phrase the user hasn't been quizzed on yet
// (falling back to a random already-seen one for unscored practice once
// they've covered everything) and assembles it with 3 random distractors.
// Returns (nil, nil) when there aren't enough phrases yet to build options.
func pickQuizQuestion(ctx context.Context, email string) (*QuizQuestion, error) {
	var total int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM english_zoa.phrases`).Scan(&total); err != nil {
		return nil, err
	}
	if total < minPhrasesForQuiz {
		return nil, nil
	}

	var phraseID int
	var english, korean string
	scored := true
	err := db.QueryRow(ctx, `
		SELECT p.id, p.english_text, p.korean_text
		FROM english_zoa.phrases p
		WHERE NOT EXISTS (
			SELECT 1 FROM english_zoa.quiz_attempts qa WHERE qa.email = $1 AND qa.phrase_id = p.id
		)
		ORDER BY random()
		LIMIT 1
	`, email).Scan(&phraseID, &english, &korean)
	if errors.Is(err, pgx.ErrNoRows) {
		scored = false
		err = db.QueryRow(ctx, `
			SELECT id, english_text, korean_text FROM english_zoa.phrases ORDER BY random() LIMIT 1
		`).Scan(&phraseID, &english, &korean)
	}
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(ctx, `
		SELECT id, korean_text FROM english_zoa.phrases WHERE id != $1 ORDER BY random() LIMIT 3
	`, phraseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	options := []QuizOption{{ID: phraseID, KoreanText: korean}}
	for rows.Next() {
		var opt QuizOption
		if err := rows.Scan(&opt.ID, &opt.KoreanText); err != nil {
			continue
		}
		options = append(options, opt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rand.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })

	return &QuizQuestion{PhraseID: phraseID, EnglishText: english, Options: options, Scored: scored}, nil
}

// recordQuizAnswer grades a one-shot answer. The first attempt on a phrase is
// the scored one; if the user already has a row for this phrase (practice
// replay), it's graded but doesn't touch score or overwrite the stored result.
func recordQuizAnswer(ctx context.Context, email string, phraseID, selectedID int) (correct bool, scoreDelta int, err error) {
	correct = selectedID == phraseID

	tx, err := db.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM english_zoa.quiz_attempts WHERE email = $1 AND phrase_id = $2)
	`, email, phraseID).Scan(&exists); err != nil {
		return false, 0, err
	}
	if exists {
		return correct, 0, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO english_zoa.quiz_attempts (email, phrase_id, correct, attempted_at)
		VALUES ($1, $2, $3, NOW())
	`, email, phraseID, correct); err != nil {
		return false, 0, err
	}

	if correct {
		if err := addScoreTx(ctx, tx, email, quizCorrectPoints); err != nil {
			return false, 0, err
		}
		scoreDelta = quizCorrectPoints
	}

	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return correct, scoreDelta, nil
}

func registerQuizRoutes(r *gin.Engine) {
	r.GET("/api/quiz/next", func(c *gin.Context) {
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
		q, err := pickQuizQuestion(ctx, email)
		if err != nil {
			log.Printf("pickQuizQuestion: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "quiz unavailable"})
			return
		}
		if q == nil {
			c.JSON(http.StatusOK, gin.H{"quiz": nil, "message": "문구가 좀 더 쌓이면 퀴즈를 풀 수 있어요"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"quiz": q})
	})

	r.POST("/api/quiz/answer", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			PhraseID   int `json:"phrase_id"`
			SelectedID int `json:"selected_id"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			log.Printf("ensureUser: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "answer failed"})
			return
		}
		correct, scoreDelta, err := recordQuizAnswer(ctx, email, body.PhraseID, body.SelectedID)
		if err != nil {
			log.Printf("recordQuizAnswer: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "answer failed"})
			return
		}
		score, err := getScore(ctx, email)
		if err != nil {
			log.Printf("getScore: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{"correct": correct, "score_delta": scoreDelta, "score": score})
	})
}
