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
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ── section: word battle — pairs with web/battle.jsx. Three game types:
// "word" and "phrase" are TEAM typing races — any number of players split
// into a left and a right team (1v1 is just one player per side), the host
// names the teams and starts the match, and for 60 seconds every round is
// a fresh item where the first correct submission from either side scores
// for their team. Each wrong attempt earns THAT player one more hint
// letter. "tetris" is team-based too: every player runs their own board,
// a correct vocab answer unlocks 5 seconds of free play, cleared lines
// count for the team AND send every living opponent a garbage line with a
// single gap, and a team loses when all of its players have topped out.
//
// "Real-time" is 1s client polling of /api/battle/:id/state — responsive
// enough for typing races, needs no websockets, and stays correct across
// Cloud Run instances because all state lives in Postgres and round wins
// are settled under row locks. The match clock is also settled lazily
// under the same lock: the first poll/answer after ends_at finishes the
// match, so no server-side timer is needed. Spectators poll the same
// state endpoint (it exposes no answers) — only participants can submit.
//
// Owns phraseup.battles + phraseup.battle_rounds + phraseup.battle_players.

// Table DDL for battles + battle_rounds + battle_players lives in
// migrations.go. Races reuse host_score/guest_score as the LEFT/RIGHT team
// scores; winner_email stays NULL for team matches and winner_team
// ('left' | 'right' | 'draw') carries the result instead. battle_rounds.hints
// holds per-player wrong-attempt hint levels for the current round
// ({email: level}); battle_players carries per-player tetris state (line
// totals, pending garbage, elimination — a team loses when all are dead).

const (
	battleMatchCountdownSec = 3    // the 3‥2‥1‥START! before round 1
	battleRoundPauseMs      = 1500 // banner pause between later rounds
	battleRaceDurationSec   = 60   // the whole race is one minute
	battleTeamNameMax       = 24
)

// battleHMACKey signs the tetris word-gate tokens so grading stays
// stateless across instances. BATTLE_HMAC_KEY overrides it when set.
// ponytail: the compiled-in fallback is deliberate — it only protects a
// party-game shortcut, and a fixed key keeps all instances in agreement
// with zero config. If the stakes ever rise, move it to Secret Manager
// like ANTHROPIC_API_KEY (ai.go).
var battleHMACKey = func() []byte {
	if k := os.Getenv("BATTLE_HMAC_KEY"); k != "" {
		return []byte(k)
	}
	return []byte("phraseup-battle-gate-v1")
}()

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

// battleRevealDelay is how long a round stays hidden after it starts:
// round 1 gets the full 3‥2‥1‥START! countdown, later rounds just a short
// "last round" banner pause so a 60-second match keeps its pace.
func battleRevealDelay(roundNo int) time.Duration {
	if roundNo <= 1 {
		return battleMatchCountdownSec * time.Second
	}
	return battleRoundPauseMs * time.Millisecond
}

// battleHint masks the answer, revealing the first `level` letters of each
// word (punctuation always shows). Level rises by one per wrong attempt.
func battleHint(english string, level int) string {
	if level <= 0 {
		return ""
	}
	words := strings.Fields(english)
	out := make([]string, len(words))
	for i, w := range words {
		runes := []rune(w)
		masked := make([]rune, len(runes))
		for j, ch := range runes {
			if j < level || (!unicode.IsLetter(ch) && !unicode.IsNumber(ch)) {
				masked[j] = ch
			} else {
				masked[j] = '•'
			}
		}
		out[i] = string(masked)
	}
	return strings.Join(out, " ")
}

// createBattle opens a waiting battle lobby, cancelling the host's earlier
// waiting ones (one open battle per person). The host is seated on the
// left team; everyone else joins/switches sides until the host starts.
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

	var battleID int
	err := db.QueryRow(ctx, `
		INSERT INTO phraseup.battles (game_type, rounds_total, host_email, duration_seconds)
		VALUES ($1, 0, $2, $3)
		RETURNING id
	`, gameType, email, battleRaceDurationSec).Scan(&battleID)
	if err != nil {
		return 0, err
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO phraseup.battle_players (battle_id, email, team) VALUES ($1, $2, 'left')
	`, battleID, email); err != nil {
		return 0, err
	}
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
	ID          int    `json:"id"`
	GameType    string `json:"game_type"`
	HostName    string `json:"host_name"`
	GuestName   string `json:"guest_name,omitempty"`
	PlayerCount int    `json:"player_count"`
	Status      string `json:"status"`
	IsMine      bool   `json:"is_mine"`
	CreatedAt   string `json:"created_at"`
}

// getBattleLobby lists joinable (waiting) and watchable (active) battles.
func getBattleLobby(ctx context.Context, email string) (entries []BattleLobbyEntry, activeID int, err error) {
	err = db.QueryRow(ctx, `
		SELECT id FROM phraseup.battles b
		WHERE status IN ('waiting', 'active') AND (
			host_email = $1 OR guest_email = $1
			OR EXISTS (SELECT 1 FROM phraseup.battle_players bp WHERE bp.battle_id = b.id AND bp.email = $1)
		)
		ORDER BY created_at DESC LIMIT 1
	`, email).Scan(&activeID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}

	rows, err := db.Query(ctx, `
		SELECT b.id, b.game_type, hu.nickname,
		       COALESCE((SELECT nickname FROM phraseup.users WHERE email = b.guest_email), ''),
		       (SELECT COUNT(*) FROM phraseup.battle_players bp WHERE bp.battle_id = b.id),
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
		if err := rows.Scan(&e.ID, &e.GameType, &e.HostName, &e.GuestName, &e.PlayerCount, &e.Status, &e.IsMine, &created); err != nil {
			warnScan("battle lobby", err)
			continue
		}
		e.CreatedAt = created.In(seoulTZ).Format("15:04")
		entries = append(entries, e)
	}
	return entries, activeID, rows.Err()
}

// joinBattle seats the newcomer in the requested (or emptier) team; the
// match waits in the lobby until the host starts it.
func joinBattle(ctx context.Context, battleID int, email, team string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM phraseup.battles WHERE id = $1 FOR UPDATE
	`, battleID).Scan(&status); err != nil {
		return errors.New("battle not found")
	}
	if status != "waiting" {
		return errors.New("battle already started")
	}

	if team != "left" && team != "right" {
		// default to the emptier side
		var left, right int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FILTER (WHERE team = 'left'), COUNT(*) FILTER (WHERE team = 'right')
			FROM phraseup.battle_players WHERE battle_id = $1
		`, battleID).Scan(&left, &right); err != nil {
			return err
		}
		team = "right"
		if right > left {
			team = "left"
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO phraseup.battle_players (battle_id, email, team) VALUES ($1, $2, $3)
		ON CONFLICT (battle_id, email) DO UPDATE SET team = $3
	`, battleID, email, team); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// switchTeam moves a seated player to the other side while the lobby is open.
func switchTeam(ctx context.Context, battleID int, email, team string) error {
	if team != "left" && team != "right" {
		return errors.New("invalid team")
	}
	ct, err := db.Exec(ctx, `
		UPDATE phraseup.battle_players bp SET team = $3
		FROM phraseup.battles b
		WHERE bp.battle_id = $1 AND bp.email = $2 AND b.id = bp.battle_id AND b.status = 'waiting'
	`, battleID, email, team)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("not in this lobby")
	}
	return nil
}

// setTeamName lets the host rename a side while the lobby is open.
func setTeamName(ctx context.Context, battleID int, email, side, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name can't be empty")
	}
	if len([]rune(name)) > battleTeamNameMax {
		name = string([]rune(name)[:battleTeamNameMax])
	}
	col := "team_left_name"
	if side == "right" {
		col = "team_right_name"
	} else if side != "left" {
		return errors.New("invalid side")
	}
	ct, err := db.Exec(ctx, `
		UPDATE phraseup.battles SET `+col+` = $3
		WHERE id = $1 AND host_email = $2 AND status = 'waiting'
	`, battleID, email, name)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("only the host can rename teams in an open lobby")
	}
	return nil
}

// leaveBattle removes a non-host player from an open lobby; the host
// leaving cancels the whole lobby.
func leaveBattle(ctx context.Context, battleID int, email string) error {
	var host, status string
	if err := db.QueryRow(ctx, `
		SELECT host_email, status FROM phraseup.battles WHERE id = $1
	`, battleID).Scan(&host, &status); err != nil {
		return errors.New("battle not found")
	}
	if status != "waiting" {
		return errors.New("battle already started")
	}
	if host == email {
		_, err := db.Exec(ctx, `UPDATE phraseup.battles SET status = 'cancelled' WHERE id = $1`, battleID)
		return err
	}
	_, err := db.Exec(ctx, `DELETE FROM phraseup.battle_players WHERE battle_id = $1 AND email = $2`, battleID, email)
	return err
}

// startBattle: host kicks off the race once both teams are seated. The
// match clock covers the countdown plus the playing minute.
func startBattle(ctx context.Context, battleID int, email string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status, host, gameType string
	var duration int
	if err := tx.QueryRow(ctx, `
		SELECT status, host_email, game_type, duration_seconds
		FROM phraseup.battles WHERE id = $1 FOR UPDATE
	`, battleID).Scan(&status, &host, &gameType, &duration); err != nil {
		return errors.New("battle not found")
	}
	if host != email {
		return errors.New("only the host can start the match")
	}
	if status != "waiting" {
		return errors.New("battle can't be started")
	}

	var left, right int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE team = 'left'), COUNT(*) FILTER (WHERE team = 'right')
		FROM phraseup.battle_players WHERE battle_id = $1
	`, battleID).Scan(&left, &right); err != nil {
		return err
	}
	if left == 0 || right == 0 {
		return errors.New("both teams need at least one player")
	}

	if gameType == "tetris" {
		// No clock — team tetris ends by elimination.
		if _, err := tx.Exec(ctx, `
			UPDATE phraseup.battles SET status = 'active', started_at = NOW() WHERE id = $1
		`, battleID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE phraseup.battles SET status = 'active', started_at = NOW(),
			ends_at = NOW() + make_interval(secs => $2 + `+strconv.Itoa(battleMatchCountdownSec)+`)
		WHERE id = $1
	`, battleID, duration); err != nil {
		return err
	}
	if err := startRound(ctx, tx, battleID, 1, battleCategory(gameType)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// finishExpiredRace settles an active race whose clock has run out. Safe to
// call from any poller — the row lock makes the first caller win and the
// rest see 'finished'.
func finishExpiredRace(ctx context.Context, battleID int) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status, gameType string
	var endsAt *time.Time
	var leftScore, rightScore int
	if err := tx.QueryRow(ctx, `
		SELECT status, game_type, ends_at, host_score, guest_score
		FROM phraseup.battles WHERE id = $1 FOR UPDATE
	`, battleID).Scan(&status, &gameType, &endsAt, &leftScore, &rightScore); err != nil {
		return err
	}
	if status != "active" || gameType == "tetris" || endsAt == nil || time.Now().Before(*endsAt) {
		return nil
	}
	winner := "draw"
	if leftScore > rightScore {
		winner = "left"
	} else if rightScore > leftScore {
		winner = "right"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE phraseup.battles SET status = 'finished', winner_team = $2, finished_at = ends_at WHERE id = $1
	`, battleID, winner); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type BattlePlayer struct {
	Nickname string `json:"nickname"`
	Lines    int    `json:"lines"`
	Dead     bool   `json:"dead"`
}

type BattleTeam struct {
	Name    string         `json:"name"`
	Score   int            `json:"score"`
	Players []BattlePlayer `json:"players"`
}

type BattleState struct {
	ID         int    `json:"id"`
	GameType   string `json:"game_type"`
	Status     string `json:"status"`
	HostName   string `json:"host_name"`
	WinnerIsMe bool   `json:"winner_is_me,omitempty"`
	Role       string `json:"role"` // player | spectator

	TeamLeft       *BattleTeam `json:"team_left,omitempty"`
	TeamRight      *BattleTeam `json:"team_right,omitempty"`
	MyTeam         string      `json:"my_team,omitempty"`
	IsHost         bool        `json:"is_host"`
	WinnerTeam     string      `json:"winner_team,omitempty"`
	WinnerTeamName string      `json:"winner_team_name,omitempty"`
	TimeLeftMs     int64       `json:"time_left_ms"`
	DurationS      int         `json:"duration_seconds"`

	// races
	RoundNo       int    `json:"round_no,omitempty"`
	KoreanPrompt  string `json:"korean_prompt,omitempty"`
	RevealInMs    int64  `json:"reveal_in_ms"`
	MyHint        string `json:"my_hint,omitempty"`
	LastRoundWord string `json:"last_round_word,omitempty"`
	LastRoundWin  string `json:"last_round_winner,omitempty"`

	// tetris — no omitempty: zeros must serialize
	MyPendingGarbage int  `json:"my_pending_garbage"`
	IAmDead          bool `json:"i_am_dead"`
}

func getBattleState(ctx context.Context, battleID int, email string) (BattleState, error) {
	var s BattleState
	var host string
	var leftName, rightName, winnerTeam string
	var startedAt, endsAt *time.Time
	var leftScore, rightScore int
	load := func() error {
		return db.QueryRow(ctx, `
			SELECT b.id, b.game_type, b.status, b.host_score, b.guest_score,
			       b.host_email, hu.nickname,
			       b.team_left_name, b.team_right_name, COALESCE(b.winner_team, ''),
			       b.started_at, b.ends_at, b.duration_seconds
			FROM phraseup.battles b
			JOIN phraseup.users hu ON hu.email = b.host_email
			WHERE b.id = $1
		`, battleID).Scan(&s.ID, &s.GameType, &s.Status, &leftScore, &rightScore,
			&host, &s.HostName, &leftName, &rightName, &winnerTeam,
			&startedAt, &endsAt, &s.DurationS)
	}
	if err := load(); err != nil {
		return BattleState{}, err
	}

	// Lazy clock: the first observer past ends_at settles a timed race.
	if s.Status == "active" && s.GameType != "tetris" && endsAt != nil && time.Now().After(*endsAt) {
		if err := finishExpiredRace(ctx, battleID); err != nil {
			log.Printf("finishExpiredRace(%d): %v", battleID, err)
		}
		if err := load(); err != nil {
			return BattleState{}, err
		}
	}

	s.IsHost = email == host
	s.Role = "spectator"

	// Teams (all game types). For tetris, a team's score is its line total.
	left := &BattleTeam{Name: leftName, Score: leftScore, Players: []BattlePlayer{}}
	right := &BattleTeam{Name: rightName, Score: rightScore, Players: []BattlePlayer{}}
	rows, err := db.Query(ctx, `
		SELECT bp.team, u.nickname, bp.email, bp.lines, bp.garbage, bp.dead
		FROM phraseup.battle_players bp
		JOIN phraseup.users u ON u.email = bp.email
		WHERE bp.battle_id = $1 ORDER BY bp.joined_at
	`, battleID)
	if err != nil {
		return BattleState{}, err
	}
	for rows.Next() {
		var team, nick, pEmail string
		var lines, garbage int
		var dead bool
		if err := rows.Scan(&team, &nick, &pEmail, &lines, &garbage, &dead); err != nil {
			warnScan("battle players", err)
			continue
		}
		p := BattlePlayer{Nickname: nick, Lines: lines, Dead: dead}
		if team == "left" {
			left.Players = append(left.Players, p)
		} else {
			right.Players = append(right.Players, p)
		}
		if pEmail == email {
			s.MyTeam = team
			s.Role = "player"
			s.MyPendingGarbage = garbage
			s.IAmDead = dead
		}
	}
	rows.Close()
	if s.GameType == "tetris" {
		leftLines, rightLines := 0, 0
		for _, p := range left.Players {
			leftLines += p.Lines
		}
		for _, p := range right.Players {
			rightLines += p.Lines
		}
		left.Score, right.Score = leftLines, rightLines
	}
	s.TeamLeft, s.TeamRight = left, right
	s.WinnerTeam = winnerTeam
	switch winnerTeam {
	case "left":
		s.WinnerTeamName = leftName
	case "right":
		s.WinnerTeamName = rightName
	}
	s.WinnerIsMe = (winnerTeam == "left" || winnerTeam == "right") && winnerTeam == s.MyTeam

	if s.Status == "active" {
		if s.GameType != "tetris" && endsAt != nil {
			s.TimeLeftMs = time.Until(*endsAt).Milliseconds()
			if s.TimeLeftMs < 0 {
				s.TimeLeftMs = 0
			}
		}
		if s.GameType == "tetris" && startedAt != nil {
			// tetris shares the 3‥2‥1‥START! anchor
			s.RevealInMs = time.Until(startedAt.Add(battleMatchCountdownSec * time.Second)).Milliseconds()
			if s.RevealInMs < 0 {
				s.RevealInMs = 0
			}
		}
	}

	if s.Status == "active" && s.GameType != "tetris" {
		var roundNo int
		var roundStarted time.Time
		var korean, english, hints string
		err := db.QueryRow(ctx, `
			SELECT r.round_no, r.started_at, p.korean_text, p.english_text, COALESCE(r.hints->>$2, '0')
			FROM phraseup.battle_rounds r JOIN phraseup.phrases p ON p.id = r.phrase_id
			WHERE r.battle_id = $1 AND r.winner_email IS NULL
			ORDER BY r.round_no ASC LIMIT 1
		`, battleID, email).Scan(&roundNo, &roundStarted, &korean, &english, &hints)
		if err == nil {
			s.RoundNo = roundNo
			revealAt := roundStarted.Add(battleRevealDelay(roundNo))
			s.RevealInMs = time.Until(revealAt).Milliseconds()
			if s.RevealInMs <= 0 {
				s.RevealInMs = 0
				s.KoreanPrompt = korean
				if lvl, _ := strconv.Atoi(hints); lvl > 0 && s.MyTeam != "" {
					s.MyHint = battleHint(english, lvl)
				}
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
// takes the round for their team (settled under a row lock on the battle).
// A wrong submission earns the submitter one more hint letter. The match
// itself is only ended by the clock.
func answerBattleRound(ctx context.Context, battleID int, email, text string) (correct bool, hint string, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx)

	var status, gameType, myTeam string
	var endsAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT b.status, b.game_type, b.ends_at,
		       COALESCE((SELECT team FROM phraseup.battle_players bp WHERE bp.battle_id = b.id AND bp.email = $2), '')
		FROM phraseup.battles b WHERE b.id = $1 FOR UPDATE
	`, battleID, email).Scan(&status, &gameType, &endsAt, &myTeam)
	if err != nil {
		return false, "", errors.New("battle not found")
	}
	if myTeam == "" {
		return false, "", errors.New("spectators can't answer")
	}
	if status != "active" || gameType == "tetris" {
		return false, "", errors.New("battle is not accepting answers")
	}
	if endsAt != nil && time.Now().After(*endsAt) {
		tx.Rollback(ctx)
		if err := finishExpiredRace(ctx, battleID); err != nil {
			log.Printf("finishExpiredRace(%d): %v", battleID, err)
		}
		return false, "", errors.New("time's up")
	}

	var roundID, roundNo int
	var startedAt time.Time
	var english, myHints string
	err = tx.QueryRow(ctx, `
		SELECT r.id, r.round_no, r.started_at, p.english_text, COALESCE(r.hints->>$2, '0')
		FROM phraseup.battle_rounds r JOIN phraseup.phrases p ON p.id = r.phrase_id
		WHERE r.battle_id = $1 AND r.winner_email IS NULL
		ORDER BY r.round_no ASC LIMIT 1
	`, battleID, email).Scan(&roundID, &roundNo, &startedAt, &english, &myHints)
	if err != nil {
		return false, "", errors.New("no open round")
	}
	if time.Since(startedAt) < battleRevealDelay(roundNo) {
		return false, "", errors.New("too early — not revealed yet")
	}

	if normalizeBattleAnswer(text) != normalizeBattleAnswer(english) {
		lvl, _ := strconv.Atoi(myHints)
		lvl++
		if _, err := tx.Exec(ctx, `
			UPDATE phraseup.battle_rounds SET hints = jsonb_set(hints, ARRAY[$2], to_jsonb($3::int)) WHERE id = $1
		`, roundID, email, lvl); err != nil {
			return false, "", err
		}
		return false, battleHint(english, lvl), tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `UPDATE phraseup.battle_rounds SET winner_email = $2 WHERE id = $1`, roundID, email); err != nil {
		return false, "", err
	}
	scoreCol := "guest_score" // right team
	if myTeam == "left" {
		scoreCol = "host_score"
	}
	if _, err := tx.Exec(ctx, `UPDATE phraseup.battles SET `+scoreCol+` = `+scoreCol+` + 1 WHERE id = $1`, battleID); err != nil {
		return false, "", err
	}
	if err := startRound(ctx, tx, battleID, roundNo+1, battleCategory(gameType)); err != nil {
		return false, "", err
	}
	return true, "", tx.Commit(ctx)
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
			warnScan("battle phrases", err)
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
			BattleID int    `json:"battle_id"`
			Team     string `json:"team"`
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
		if err := joinBattle(ctx, body.BattleID, email, body.Team); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/battle/side", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			BattleID int    `json:"battle_id"`
			Team     string `json:"team"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		if err := switchTeam(c.Request.Context(), body.BattleID, email, body.Team); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/battle/team-name", func(c *gin.Context) {
		email, ok := requireEmail(c)
		if !ok {
			return
		}
		var body struct {
			BattleID int    `json:"battle_id"`
			Side     string `json:"side"`
			Name     string `json:"name"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		if err := setTeamName(c.Request.Context(), body.BattleID, email, body.Side, body.Name); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/battle/start", func(c *gin.Context) {
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
		if err := startBattle(c.Request.Context(), body.BattleID, email); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	r.POST("/api/battle/leave", func(c *gin.Context) {
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
		if err := leaveBattle(c.Request.Context(), body.BattleID, email); err != nil {
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
		correct, hint, err := answerBattleRound(c.Request.Context(), body.BattleID, email, body.Text)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"correct": correct, "hint": hint})
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
		// EVERY living player on the other team (one garbage line per line
		// cleared, single gap each — gap placement is client-side rendering).
		ctx := c.Request.Context()
		ct, err := db.Exec(ctx, `
			UPDATE phraseup.battle_players bp SET lines = bp.lines + $3
			FROM phraseup.battles b
			WHERE bp.battle_id = $1 AND bp.email = $2 AND NOT bp.dead
			  AND b.id = bp.battle_id AND b.status = 'active' AND b.game_type = 'tetris'
		`, body.BattleID, email, body.Lines)
		if err != nil || ct.RowsAffected() == 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "not your active tetris battle"})
			return
		}
		if _, err := db.Exec(ctx, `
			UPDATE phraseup.battle_players bp SET garbage = bp.garbage + $3
			WHERE bp.battle_id = $1 AND NOT bp.dead
			  AND bp.team <> (SELECT team FROM phraseup.battle_players WHERE battle_id = $1 AND email = $2)
		`, body.BattleID, email, body.Lines); err != nil {
			log.Printf("tetris garbage: %v", err)
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
			UPDATE phraseup.battle_players SET garbage = GREATEST(garbage - $3, 0)
			WHERE battle_id = $1 AND email = $2
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
		ctx := c.Request.Context()
		tx, err := db.Begin(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "gameover failed"})
			return
		}
		defer tx.Rollback(ctx)

		var status string
		if err := tx.QueryRow(ctx, `
			SELECT status FROM phraseup.battles WHERE id = $1 AND game_type = 'tetris' FOR UPDATE
		`, body.BattleID).Scan(&status); err != nil || status != "active" {
			c.JSON(http.StatusConflict, gin.H{"error": "not your active tetris battle"})
			return
		}
		var myTeam string
		if err := tx.QueryRow(ctx, `
			UPDATE phraseup.battle_players SET dead = TRUE
			WHERE battle_id = $1 AND email = $2 RETURNING team
		`, body.BattleID, email).Scan(&myTeam); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "not your active tetris battle"})
			return
		}
		var teamAlive int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM phraseup.battle_players
			WHERE battle_id = $1 AND team = $2 AND NOT dead
		`, body.BattleID, myTeam).Scan(&teamAlive); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "gameover failed"})
			return
		}
		if teamAlive == 0 {
			winner := "left"
			if myTeam == "left" {
				winner = "right"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE phraseup.battles SET status = 'finished', winner_team = $2, finished_at = NOW() WHERE id = $1
			`, body.BattleID, winner); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "gameover failed"})
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "gameover failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "team_eliminated": teamAlive == 0})
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
