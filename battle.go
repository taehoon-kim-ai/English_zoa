package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: word battle — live 1v1 games with spectating. Three game
// types: "word" and "phrase" are best-of-N typing races (default 10
// rounds — each round is a fresh item, first correct submission takes the
// round, most rounds wins); "tetris" is a battle-tetris where each next
// piece must be earned by answering a vocabulary question, and every line
// you clear sends the opponent a garbage line with a single gap.
//
// "Real-time" is 1s client polling of /api/battle/:id/state — responsive
// enough for typing races, needs no websockets, and stays correct across
// Cloud Run instances because all state lives in Postgres and round wins
// are settled under row locks. Spectators poll the same state endpoint
// (it exposes no answers) — only participants can submit.
//
// Owns phraseup.battles + phraseup.battle_rounds.

var battleSchemaStmts = []string{
	// Pre-rounds battles schema is replaced wholesale (one-time, guarded).
	`DO $$ BEGIN
		IF EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'phraseup' AND table_name = 'battles'
		) AND NOT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'phraseup' AND table_name = 'battles' AND column_name = 'game_type'
		) THEN
			DROP TABLE phraseup.battles;
		END IF;
	END $$`,
	`CREATE TABLE IF NOT EXISTS phraseup.battles (
		id            SERIAL PRIMARY KEY,
		game_type     TEXT NOT NULL DEFAULT 'word' CHECK (game_type IN ('word', 'phrase', 'tetris')),
		rounds_total  INT NOT NULL DEFAULT 10,
		status        TEXT NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'active', 'finished', 'cancelled')),
		host_email    TEXT NOT NULL REFERENCES phraseup.users(email) ON DELETE CASCADE,
		guest_email   TEXT REFERENCES phraseup.users(email) ON DELETE CASCADE,
		host_score    INT NOT NULL DEFAULT 0,
		guest_score   INT NOT NULL DEFAULT 0,
		host_garbage  INT NOT NULL DEFAULT 0,
		guest_garbage INT NOT NULL DEFAULT 0,
		host_lines    INT NOT NULL DEFAULT 0,
		guest_lines   INT NOT NULL DEFAULT 0,
		winner_email  TEXT,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		started_at    TIMESTAMPTZ,
		finished_at   TIMESTAMPTZ
	)`,
	`CREATE TABLE IF NOT EXISTS phraseup.battle_rounds (
		id           SERIAL PRIMARY KEY,
		battle_id    INT NOT NULL REFERENCES phraseup.battles(id) ON DELETE CASCADE,
		round_no     INT NOT NULL,
		phrase_id    INT NOT NULL REFERENCES phraseup.phrases(id) ON DELETE CASCADE,
		winner_email TEXT,
		started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (battle_id, round_no)
	)`,
}

const (
	battleCountdownSeconds = 3
	battleDefaultRounds    = 10
)

// battleHMACKey signs the tetris word-gate tokens so grading stays
// stateless across instances. A fixed app-level key is fine here — the
// only thing it protects is a party-game shortcut.
var battleHMACKey = []byte("phraseup-battle-gate-v1")

var reBattleNormalize = regexp.MustCompile(`[^a-z0-9 ]`)

func normalizeBattleAnswer(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reBattleNormalize.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func battleGateSig(battleID int, english string) string {
	mac := hmac.New(sha256.New, battleHMACKey)
	mac.Write([]byte(strconv.Itoa(battleID) + "|" + normalizeBattleAnswer(english)))
	return hex.EncodeToString(mac.Sum(nil))
}

func battleCategory(gameType string) string {
	if gameType == "phrase" {
		return "expression"
	}
	return "vocabulary" // word races and tetris gates both use single words
}

// createBattle opens a waiting battle, cancelling the host's earlier
// waiting ones (one open battle per person).
func createBattle(ctx context.Context, email, gameType string) (int, error) {
	if gameType != "word" && gameType != "phrase" && gameType != "tetris" {
		gameType = "word"
	}
	if _, err := db.Exec(ctx, `
		UPDATE phraseup.battles SET status = 'cancelled'
		WHERE host_email = $1 AND status = 'waiting'
	`, email); err != nil {
		return 0, err
	}

	rounds := battleDefaultRounds
	if gameType == "tetris" {
		rounds = 0
	}
	var battleID int
	err := db.QueryRow(ctx, `
		INSERT INTO phraseup.battles (game_type, rounds_total, host_email)
		VALUES ($1, $2, $3)
		RETURNING id
	`, gameType, rounds, email).Scan(&battleID)
	return battleID, err
}

// startRound inserts the next round with a fresh random phrase of the
// battle's category. Round start time doubles as the countdown anchor.
func startRound(ctx context.Context, tx pgx.Tx, battleID int, roundNo int, category string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO phraseup.battle_rounds (battle_id, round_no, phrase_id)
		SELECT $1, $2, id FROM phraseup.phrases WHERE category = $3 ORDER BY random() LIMIT 1
		ON CONFLICT (battle_id, round_no) DO NOTHING
	`, battleID, roundNo, category)
	return err
}

type BattleLobbyEntry struct {
	ID        int    `json:"id"`
	GameType  string `json:"game_type"`
	HostName  string `json:"host_name"`
	GuestName string `json:"guest_name,omitempty"`
	Status    string `json:"status"`
	IsMine    bool   `json:"is_mine"`
	CreatedAt string `json:"created_at"`
}

// getBattleLobby lists joinable (waiting) and watchable (active) battles.
func getBattleLobby(ctx context.Context, email string) (entries []BattleLobbyEntry, activeID int, err error) {
	err = db.QueryRow(ctx, `
		SELECT id FROM phraseup.battles
		WHERE status IN ('waiting', 'active') AND (host_email = $1 OR guest_email = $1)
		ORDER BY created_at DESC LIMIT 1
	`, email).Scan(&activeID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}

	rows, err := db.Query(ctx, `
		SELECT b.id, b.game_type, hu.nickname,
		       COALESCE((SELECT nickname FROM phraseup.users WHERE email = b.guest_email), ''),
		       b.status, b.host_email = $1, b.created_at
		FROM phraseup.battles b
		JOIN phraseup.users hu ON hu.email = b.host_email
		WHERE b.status IN ('waiting', 'active') AND b.created_at > NOW() - INTERVAL '60 minutes'
		ORDER BY b.status DESC, b.created_at DESC
		LIMIT 15
	`, email)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	entries = []BattleLobbyEntry{}
	for rows.Next() {
		var e BattleLobbyEntry
		var created time.Time
		if err := rows.Scan(&e.ID, &e.GameType, &e.HostName, &e.GuestName, &e.Status, &e.IsMine, &created); err != nil {
			continue
		}
		e.CreatedAt = created.In(seoulTZ).Format("15:04")
		entries = append(entries, e)
	}
	return entries, activeID, rows.Err()
}

func joinBattle(ctx context.Context, battleID int, email string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status, host, gameType string
	if err := tx.QueryRow(ctx, `
		SELECT status, host_email, game_type FROM phraseup.battles WHERE id = $1 FOR UPDATE
	`, battleID).Scan(&status, &host, &gameType); err != nil {
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
	if gameType != "tetris" {
		if err := startRound(ctx, tx, battleID, 1, battleCategory(gameType)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type BattleState struct {
	ID          int    `json:"id"`
	GameType    string `json:"game_type"`
	Status      string `json:"status"`
	HostName    string `json:"host_name"`
	GuestName   string `json:"guest_name,omitempty"`
	HostScore   int    `json:"host_score"`
	GuestScore  int    `json:"guest_score"`
	RoundsTotal int    `json:"rounds_total"`
	RoundNo     int    `json:"round_no,omitempty"`
	WinnerName  string `json:"winner_name,omitempty"`
	WinnerIsMe  bool   `json:"winner_is_me,omitempty"`
	Role        string `json:"role"` // host | guest | spectator

	// races
	KoreanPrompt  string `json:"korean_prompt,omitempty"`
	RevealInMs    int64  `json:"reveal_in_ms"`
	LastRoundWord string `json:"last_round_word,omitempty"`
	LastRoundWin  string `json:"last_round_winner,omitempty"`

	// tetris — no omitempty: zeros must serialize so the UI renders "0 lines"
	MyPendingGarbage int `json:"my_pending_garbage"`
	HostLines        int `json:"host_lines"`
	GuestLines       int `json:"guest_lines"`
}

func getBattleState(ctx context.Context, battleID int, email string) (BattleState, error) {
	var s BattleState
	var host, guest, winner string
	var hostGarbage, guestGarbage int
	err := db.QueryRow(ctx, `
		SELECT b.id, b.game_type, b.status, b.rounds_total, b.host_score, b.guest_score,
		       b.host_email, COALESCE(b.guest_email, ''), COALESCE(b.winner_email, ''),
		       hu.nickname, COALESCE((SELECT nickname FROM phraseup.users WHERE email = b.guest_email), ''),
		       COALESCE((SELECT nickname FROM phraseup.users WHERE email = b.winner_email), ''),
		       b.host_garbage, b.guest_garbage, b.host_lines, b.guest_lines
		FROM phraseup.battles b
		JOIN phraseup.users hu ON hu.email = b.host_email
		WHERE b.id = $1
	`, battleID).Scan(&s.ID, &s.GameType, &s.Status, &s.RoundsTotal, &s.HostScore, &s.GuestScore,
		&host, &guest, &winner, &s.HostName, &s.GuestName, &s.WinnerName,
		&hostGarbage, &guestGarbage, &s.HostLines, &s.GuestLines)
	if err != nil {
		return BattleState{}, err
	}

	switch email {
	case host:
		s.Role = "host"
		s.MyPendingGarbage = hostGarbage
	case guest:
		s.Role = "guest"
		s.MyPendingGarbage = guestGarbage
	default:
		s.Role = "spectator"
	}
	s.WinnerIsMe = winner != "" && winner == email

	if s.Status == "active" && s.GameType != "tetris" {
		var roundNo int
		var startedAt time.Time
		var korean string
		err := db.QueryRow(ctx, `
			SELECT r.round_no, r.started_at, p.korean_text
			FROM phraseup.battle_rounds r JOIN phraseup.phrases p ON p.id = r.phrase_id
			WHERE r.battle_id = $1 AND r.winner_email IS NULL
			ORDER BY r.round_no ASC LIMIT 1
		`, battleID).Scan(&roundNo, &startedAt, &korean)
		if err == nil {
			s.RoundNo = roundNo
			revealAt := startedAt.Add(battleCountdownSeconds * time.Second)
			s.RevealInMs = time.Until(revealAt).Milliseconds()
			if s.RevealInMs <= 0 {
				s.KoreanPrompt = korean
			}
		}
		// Last decided round, for the between-rounds banner.
		var lastWinner, lastWord string
		if err := db.QueryRow(ctx, `
			SELECT COALESCE((SELECT nickname FROM phraseup.users WHERE email = r.winner_email), ''), p.english_text
			FROM phraseup.battle_rounds r JOIN phraseup.phrases p ON p.id = r.phrase_id
			WHERE r.battle_id = $1 AND r.winner_email IS NOT NULL
			ORDER BY r.round_no DESC LIMIT 1
		`, battleID).Scan(&lastWinner, &lastWord); err == nil {
			s.LastRoundWin = lastWinner
			s.LastRoundWord = lastWord
		}
	}
	return s, nil
}

// answerBattleRound grades a race submission; the first correct submission
// takes the round (settled under a row lock on the round). Taking the final
// round — or a majority that can't be caught — finishes the match.
func answerBattleRound(ctx context.Context, battleID int, email, text string) (correct, wonRound, wonMatch bool, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, false, false, err
	}
	defer tx.Rollback(ctx)

	var status, gameType, host, guest string
	var roundsTotal, hostScore, guestScore int
	err = tx.QueryRow(ctx, `
		SELECT status, game_type, host_email, COALESCE(guest_email, ''), rounds_total, host_score, guest_score
		FROM phraseup.battles WHERE id = $1 FOR UPDATE
	`, battleID).Scan(&status, &gameType, &host, &guest, &roundsTotal, &hostScore, &guestScore)
	if err != nil {
		return false, false, false, errors.New("battle not found")
	}
	if email != host && email != guest {
		return false, false, false, errors.New("spectators can't answer")
	}
	if status != "active" || gameType == "tetris" {
		return false, false, false, errors.New("battle is not accepting answers")
	}

	var roundID, roundNo, phraseID int
	var startedAt time.Time
	var english string
	err = tx.QueryRow(ctx, `
		SELECT r.id, r.round_no, r.phrase_id, r.started_at, p.english_text
		FROM phraseup.battle_rounds r JOIN phraseup.phrases p ON p.id = r.phrase_id
		WHERE r.battle_id = $1 AND r.winner_email IS NULL
		ORDER BY r.round_no ASC LIMIT 1
	`, battleID).Scan(&roundID, &roundNo, &phraseID, &startedAt, &english)
	if err != nil {
		return false, false, false, errors.New("no open round")
	}
	if time.Since(startedAt) < battleCountdownSeconds*time.Second {
		return false, false, false, errors.New("too early — not revealed yet")
	}

	if normalizeBattleAnswer(text) != normalizeBattleAnswer(english) {
		return false, false, false, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `UPDATE phraseup.battle_rounds SET winner_email = $2 WHERE id = $1`, roundID, email); err != nil {
		return false, false, false, err
	}
	scoreCol := "guest_score"
	if email == host {
		scoreCol = "host_score"
	}
	if _, err := tx.Exec(ctx, `UPDATE phraseup.battles SET `+scoreCol+` = `+scoreCol+` + 1 WHERE id = $1`, battleID); err != nil {
		return false, false, false, err
	}
	if email == host {
		hostScore++
	} else {
		guestScore++
	}

	if roundNo >= roundsTotal {
		winner := host
		if guestScore > hostScore {
			winner = guest
		} else if guestScore == hostScore {
			winner = email // final-round taker breaks a tie
		}
		if _, err := tx.Exec(ctx, `
			UPDATE phraseup.battles SET status = 'finished', winner_email = $2, finished_at = NOW() WHERE id = $1
		`, battleID, winner); err != nil {
			return false, false, false, err
		}
		return true, true, true, tx.Commit(ctx)
	}

	if err := startRound(ctx, tx, battleID, roundNo+1, battleCategory(gameType)); err != nil {
		return false, false, false, err
	}
	return true, true, false, tx.Commit(ctx)
}

// tetrisGateQuestion returns a word-gate MC question. Grading is stateless:
// each option carries an HMAC over (battleID, option); the client sends back
// the chosen option and the sig THAT CAME WITH THE CORRECT option — matching
// sig+option proves the pick without the server storing anything.
func tetrisGateQuestion(ctx context.Context, battleID int) (gin.H, error) {
	rows, err := db.Query(ctx, `
		SELECT english_text, korean_text FROM phraseup.phrases
		WHERE category = 'vocabulary' ORDER BY random() LIMIT 4
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type opt struct {
		English string `json:"english"`
	}
	var options []opt
	var koreans []string
	for rows.Next() {
		var en, ko string
		if err := rows.Scan(&en, &ko); err != nil {
			continue
		}
		options = append(options, opt{English: en})
		koreans = append(koreans, ko)
	}
	if len(options) < 4 {
		return nil, errors.New("not enough vocabulary")
	}
	correctIdx := rand.Intn(len(options))
	correctKorean := koreans[correctIdx]
	sig := battleGateSig(battleID, options[correctIdx].English)
	rand.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	return gin.H{
		"korean":  correctKorean,
		"options": options,
		"sig":     sig,
	}, nil
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
		var body struct {
			GameType string `json:"game_type"`
		}
		_ = c.ShouldBindJSON(&body)
		ctx := c.Request.Context()
		if err := ensureUser(ctx, email); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create failed"})
			return
		}
		if err := seedStaticPhrasesIfMissing(ctx); err != nil {
			log.Printf("seedStaticPhrasesIfMissing: %v", err)
		}
		id, err := createBattle(ctx, email, body.GameType)
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
		correct, wonRound, wonMatch, err := answerBattleRound(c.Request.Context(), body.BattleID, email, body.Text)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"correct": correct, "won_round": wonRound, "won_match": wonMatch})
	})

	// ── tetris ──
	r.GET("/api/battle/:id/tetris/question", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		battleID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		q, err := tetrisGateQuestion(c.Request.Context(), battleID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, q)
	})

	r.POST("/api/battle/tetris/gate", func(c *gin.Context) {
		if _, ok := requireEmail(c); !ok {
			return
		}
		var body struct {
			BattleID int    `json:"battle_id"`
			English  string `json:"english"`
			Sig      string `json:"sig"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		ok := hmac.Equal([]byte(battleGateSig(body.BattleID, body.English)), []byte(body.Sig))
		c.JSON(http.StatusOK, gin.H{"correct": ok})
	})

	r.POST("/api/battle/tetris/lines", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			BattleID int `json:"battle_id"`
			Lines    int `json:"lines"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Lines < 1 || body.Lines > 4 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		// Cleared lines count toward my total and become pending garbage for
		// the opponent (one garbage line per line cleared, single gap each —
		// gap placement is client-side rendering).
		ct, err := db.Exec(c.Request.Context(), `
			UPDATE phraseup.battles SET
				host_lines    = host_lines    + CASE WHEN host_email  = $2 THEN $3 ELSE 0 END,
				guest_lines   = guest_lines   + CASE WHEN guest_email = $2 THEN $3 ELSE 0 END,
				guest_garbage = guest_garbage + CASE WHEN host_email  = $2 THEN $3 ELSE 0 END,
				host_garbage  = host_garbage  + CASE WHEN guest_email = $2 THEN $3 ELSE 0 END
			WHERE id = $1 AND status = 'active' AND game_type = 'tetris' AND (host_email = $2 OR guest_email = $2)
		`, body.BattleID, email, body.Lines)
		if err != nil || ct.RowsAffected() == 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "not your active tetris battle"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/battle/tetris/consume", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			BattleID int `json:"battle_id"`
			Lines    int `json:"lines"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Lines < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		if _, err := db.Exec(c.Request.Context(), `
			UPDATE phraseup.battles SET
				host_garbage  = GREATEST(host_garbage  - CASE WHEN host_email  = $2 THEN $3 ELSE 0 END, 0),
				guest_garbage = GREATEST(guest_garbage - CASE WHEN guest_email = $2 THEN $3 ELSE 0 END, 0)
			WHERE id = $1 AND (host_email = $2 OR guest_email = $2)
		`, body.BattleID, email, body.Lines); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "consume failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/battle/tetris/gameover", func(c *gin.Context) {
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
		// I topped out — the opponent wins.
		ct, err := db.Exec(c.Request.Context(), `
			UPDATE phraseup.battles SET status = 'finished', finished_at = NOW(),
				winner_email = CASE WHEN host_email = $2 THEN guest_email ELSE host_email END
			WHERE id = $1 AND status = 'active' AND game_type = 'tetris' AND (host_email = $2 OR guest_email = $2)
		`, body.BattleID, email)
		if err != nil || ct.RowsAffected() == 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "not your active tetris battle"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
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
