package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: word battle — live 1v1 typing race. One player opens a battle,
// another joins from the lobby; both see the same Korean meaning and the
// first to type the matching English word wins. "Real-time" here is 1s
// client polling of /api/battle/:id/state — plenty responsive for a typing
// race, needs no websockets, and survives Cloud Run scale-to-zero (state
// lives in Postgres, first-correct-wins is settled by a row lock, so it's
// correct even across multiple instances). Owns phraseup.battles.

var battleSchemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS phraseup.battles (
		id           SERIAL PRIMARY KEY,
		phrase_id    INT NOT NULL REFERENCES phraseup.phrases(id) ON DELETE CASCADE,
		status       TEXT NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'active', 'finished', 'cancelled')),
		host_email   TEXT NOT NULL REFERENCES phraseup.users(email) ON DELETE CASCADE,
		guest_email  TEXT REFERENCES phraseup.users(email) ON DELETE CASCADE,
		winner_email TEXT,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		started_at   TIMESTAMPTZ,
		finished_at  TIMESTAMPTZ
	)`,
}

// battleCountdownSeconds is how long both players see the get-ready screen
// after a join before the word is revealed — the reveal moment is
// started_at + countdown, computed identically on both clients from the
// server timestamps so nobody gets a head start.
const battleCountdownSeconds = 3

var reBattleNormalize = regexp.MustCompile(`[^a-z0-9 ]`)

// normalizeBattleAnswer folds case, punctuation, and whitespace runs so
// "Touch base!" matches "touch base".
func normalizeBattleAnswer(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reBattleNormalize.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// createBattle opens a new waiting battle on a random vocabulary phrase,
// cancelling any earlier waiting battle by the same host first (one open
// battle per person).
func createBattle(ctx context.Context, email string) (int, error) {
	if _, err := db.Exec(ctx, `
		UPDATE phraseup.battles SET status = 'cancelled'
		WHERE host_email = $1 AND status = 'waiting'
	`, email); err != nil {
		return 0, err
	}

	var battleID int
	err := db.QueryRow(ctx, `
		INSERT INTO phraseup.battles (phrase_id, host_email)
		SELECT id, $1 FROM phraseup.phrases WHERE category = 'vocabulary' ORDER BY random() LIMIT 1
		RETURNING id
	`, email).Scan(&battleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errors.New("no vocabulary in the pool yet")
	}
	return battleID, err
}

type BattleLobbyEntry struct {
	ID        int    `json:"id"`
	HostName  string `json:"host_name"`
	IsMine    bool   `json:"is_mine"`
	CreatedAt string `json:"created_at"`
}

func getBattleLobby(ctx context.Context, email string) (entries []BattleLobbyEntry, activeID int, err error) {
	// Battles I'm already in that are still running take priority.
	err = db.QueryRow(ctx, `
		SELECT id FROM phraseup.battles
		WHERE status IN ('waiting', 'active') AND (host_email = $1 OR guest_email = $1)
		ORDER BY created_at DESC LIMIT 1
	`, email).Scan(&activeID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}

	rows, err := db.Query(ctx, `
		SELECT b.id, u.nickname, b.host_email = $1, b.created_at
		FROM phraseup.battles b
		JOIN phraseup.users u ON u.email = b.host_email
		WHERE b.status = 'waiting' AND b.created_at > NOW() - INTERVAL '30 minutes'
		ORDER BY b.created_at DESC
		LIMIT 10
	`, email)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	entries = []BattleLobbyEntry{}
	for rows.Next() {
		var e BattleLobbyEntry
		var created time.Time
		if err := rows.Scan(&e.ID, &e.HostName, &e.IsMine, &created); err != nil {
			continue
		}
		e.CreatedAt = created.In(seoulTZ).Format("15:04")
		entries = append(entries, e)
	}
	return entries, activeID, rows.Err()
}

// joinBattle claims a waiting battle. The row lock makes a double-join race
// resolve to exactly one winner of the guest seat.
func joinBattle(ctx context.Context, battleID int, email string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status, host string
	if err := tx.QueryRow(ctx, `
		SELECT status, host_email FROM phraseup.battles WHERE id = $1 FOR UPDATE
	`, battleID).Scan(&status, &host); err != nil {
		return errors.New("battle not found")
	}
	if status != "waiting" {
		return errors.New("battle already started")
	}
	if host == email {
		return errors.New("you can't join your own battle")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE phraseup.battles SET guest_email = $2, status = 'active', started_at = NOW() WHERE id = $1
	`, battleID, email); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type BattleState struct {
	ID            int    `json:"id"`
	Status        string `json:"status"`
	HostName      string `json:"host_name"`
	GuestName     string `json:"guest_name,omitempty"`
	WinnerName    string `json:"winner_name,omitempty"`
	WinnerIsMe    bool   `json:"winner_is_me,omitempty"`
	KoreanPrompt  string `json:"korean_prompt,omitempty"`  // revealed once active
	EnglishAnswer string `json:"english_answer,omitempty"` // revealed once finished
	RevealInMs    int64  `json:"reveal_in_ms"`             // countdown remaining; <=0 means the word is live
}

func getBattleState(ctx context.Context, battleID int, email string) (BattleState, error) {
	var s BattleState
	var guest, winner *string
	var startedAt *time.Time
	var korean, english string
	err := db.QueryRow(ctx, `
		SELECT b.id, b.status, hu.nickname,
		       (SELECT nickname FROM phraseup.users WHERE email = b.guest_email),
		       (SELECT nickname FROM phraseup.users WHERE email = b.winner_email),
		       COALESCE(b.winner_email = $2, FALSE), b.started_at, p.korean_text, p.english_text
		FROM phraseup.battles b
		JOIN phraseup.users u2 ON u2.email = b.host_email
		JOIN phraseup.users hu ON hu.email = b.host_email
		JOIN phraseup.phrases p ON p.id = b.phrase_id
		WHERE b.id = $1
	`, battleID, email).Scan(&s.ID, &s.Status, &s.HostName, &guest, &winner, &s.WinnerIsMe, &startedAt, &korean, &english)
	if err != nil {
		return BattleState{}, err
	}
	if guest != nil {
		s.GuestName = *guest
	}
	if winner != nil {
		s.WinnerName = *winner
	}
	if s.Status == "active" && startedAt != nil {
		revealAt := startedAt.Add(battleCountdownSeconds * time.Second)
		s.RevealInMs = time.Until(revealAt).Milliseconds()
		if s.RevealInMs <= 0 {
			s.KoreanPrompt = korean
		}
	}
	if s.Status == "finished" {
		s.KoreanPrompt = korean
		s.EnglishAnswer = english
	}
	return s, nil
}

// answerBattle grades a submission; the first CORRECT submission (settled
// under a row lock) finishes the battle. Wrong submissions just return
// correct=false and the race continues.
func answerBattle(ctx context.Context, battleID int, email, text string) (correct, won bool, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback(ctx)

	var status, english string
	var startedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT b.status, p.english_text, b.started_at
		FROM phraseup.battles b JOIN phraseup.phrases p ON p.id = b.phrase_id
		WHERE b.id = $1 AND (b.host_email = $2 OR b.guest_email = $2)
		FOR UPDATE OF b
	`, battleID, email).Scan(&status, &english, &startedAt)
	if err != nil {
		return false, false, errors.New("battle not found")
	}
	if status != "active" {
		return false, false, errors.New("battle is not active")
	}
	if startedAt != nil && time.Since(*startedAt) < battleCountdownSeconds*time.Second {
		return false, false, errors.New("too early — the word isn't revealed yet")
	}

	if normalizeBattleAnswer(text) != normalizeBattleAnswer(english) {
		return false, false, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE phraseup.battles SET status = 'finished', winner_email = $2, finished_at = NOW() WHERE id = $1
	`, battleID, email); err != nil {
		return false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, err
	}
	return true, true, nil
}

func registerBattleRoutes(r *gin.Engine) {
	r.GET("/api/battle/lobby", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "lobby unavailable"})
			return
		}
		entries, activeID, err := getBattleLobby(ctx, email)
		if err != nil {
			log.Printf("getBattleLobby: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "lobby unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"battles": entries, "active_battle_id": activeID})
	})

	r.POST("/api/battle/create", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create failed"})
			return
		}
		if err := seedStaticPhrasesIfMissing(ctx); err != nil {
			log.Printf("seedStaticPhrasesIfMissing: %v", err)
		}
		id, err := createBattle(ctx, email)
		if err != nil {
			log.Printf("createBattle: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"battle_id": id})
	})

	r.POST("/api/battle/join", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			BattleID int `json:"battle_id"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "join failed"})
			return
		}
		if err := joinBattle(ctx, body.BattleID, email); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.GET("/api/battle/:id/state", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		battleID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		state, err := getBattleState(c.Request.Context(), battleID, email)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "battle not found"})
			return
		}
		c.JSON(http.StatusOK, state)
	})

	r.POST("/api/battle/answer", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			BattleID int    `json:"battle_id"`
			Text     string `json:"text"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		correct, won, err := answerBattle(c.Request.Context(), body.BattleID, email, body.Text)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"correct": correct, "won": won})
	})

	r.POST("/api/battle/cancel", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		if _, err := db.Exec(c.Request.Context(), `
			UPDATE phraseup.battles SET status = 'cancelled'
			WHERE host_email = $1 AND status = 'waiting'
		`, email); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cancel failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}
