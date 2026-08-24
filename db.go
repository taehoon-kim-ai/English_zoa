package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// db is the shared Postgres pool (Apps Platform's shared exp-db, `english_zoa`
// schema). Scoring/leaderboard has no meaning without persistence, so unlike
// a live-fetch dashboard we don't try to run without it (see main.go).
var db *pgxpool.Pool

const (
	streakBonusEvery    = 7  // every Nth consecutive login day...
	streakBonusPoints   = 10 // ...awards this many bonus points
	correctAnswerPoints = 1
)

// initDB connects to the shared experimental Postgres ("exp-db"), mirroring
// the MADANG dashboard's connection pattern (local tunnel vs. Cloud SQL
// connector with IAM auth) since both apps run on the same Apps Platform v2.
func initDB(ctx context.Context) error {
	dbUser := os.Getenv("DB_USER")
	dbName := os.Getenv("DB_NAME")
	instanceConn := os.Getenv("INSTANCE_CONNECTION_NAME")

	if dbName == "" {
		dbName = "postgres"
	}

	var (
		pool *pgxpool.Pool
		err  error
	)

	if instanceConn == "" {
		// Local dev — tunnel must be running: `apps-platform connect-db`
		if dbUser == "" {
			dbUser = "postgres"
		}
		dsn := fmt.Sprintf("host=localhost port=5432 user=%s dbname=%s sslmode=disable", dbUser, dbName)
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			return fmt.Errorf("local db: %w", err)
		}
	} else {
		// Production — Cloud SQL connector with IAM auth + private IP
		d, dialErr := cloudsqlconn.NewDialer(ctx,
			cloudsqlconn.WithIAMAuthN(),
			cloudsqlconn.WithDefaultDialOptions(cloudsqlconn.WithPrivateIP()),
		)
		if dialErr != nil {
			return fmt.Errorf("cloud sql dialer: %w", dialErr)
		}
		dsn := fmt.Sprintf("user=%s dbname=%s sslmode=disable", dbUser, dbName)
		cfg, parseErr := pgxpool.ParseConfig(dsn)
		if parseErr != nil {
			return fmt.Errorf("parse config: %w", parseErr)
		}
		cfg.ConnConfig.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.Dial(ctx, instanceConn)
		}
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return fmt.Errorf("production db: %w", err)
		}
	}

	db = pool
	if err := initSchema(ctx); err != nil {
		db = nil
		pool.Close()
		return err
	}
	log.Println("DB ready (english_zoa schema)")
	return nil
}

func dbAvailable() bool { return db != nil }

func initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE SCHEMA IF NOT EXISTS english_zoa`,

		`CREATE TABLE IF NOT EXISTS english_zoa.users (
			email          TEXT PRIMARY KEY,
			nickname       TEXT NOT NULL DEFAULT '',
			status_message TEXT NOT NULL DEFAULT '',
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

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

		`CREATE TABLE IF NOT EXISTS english_zoa.login_events (
			email        TEXT NOT NULL REFERENCES english_zoa.users(email) ON DELETE CASCADE,
			login_date   DATE NOT NULL,
			login_time   TIMESTAMPTZ NOT NULL,
			streak_count INT NOT NULL DEFAULT 1,
			PRIMARY KEY (email, login_date)
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

		// Summary table for fast leaderboard reads — kept in sync transactionally
		// by recordAttempt/recordLogin rather than aggregated on every request.
		`CREATE TABLE IF NOT EXISTS english_zoa.user_scores (
			email       TEXT PRIMARY KEY REFERENCES english_zoa.users(email) ON DELETE CASCADE,
			total_score INT NOT NULL DEFAULT 0,
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			if s == `CREATE SCHEMA IF NOT EXISTS english_zoa` && canContinueSchemaCreate(err, schemaExists(ctx, "english_zoa")) {
				log.Println("DB schema english_zoa exists; continuing without database CREATE privilege")
				continue
			}
			return fmt.Errorf("schema init: %w", err)
		}
	}
	return nil
}

func schemaExists(ctx context.Context, name string) bool {
	var exists bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.schemata WHERE schema_name = $1
		)
	`, name).Scan(&exists)
	return err == nil && exists
}

func canContinueSchemaCreate(err error, schemaExists bool) bool {
	if !schemaExists {
		return false
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

// ── users / profile ─────────────────────────────────────────────────────────

type Profile struct {
	Email         string `json:"email"`
	Nickname      string `json:"nickname"`
	StatusMessage string `json:"status_message"`
}

func defaultNickname(email string) string {
	local := strings.TrimSpace(strings.SplitN(email, "@", 2)[0])
	if local == "" {
		return "익명"
	}
	return local
}

// ensureUser creates the user + user_scores row on first sight. Idempotent —
// call at the top of any handler that needs the user to already exist.
func ensureUser(ctx context.Context, email string) error {
	if _, err := db.Exec(ctx, `
		INSERT INTO english_zoa.users (email, nickname) VALUES ($1, $2)
		ON CONFLICT (email) DO NOTHING
	`, email, defaultNickname(email)); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		INSERT INTO english_zoa.user_scores (email) VALUES ($1)
		ON CONFLICT (email) DO NOTHING
	`, email)
	return err
}

func getUser(ctx context.Context, email string) (Profile, error) {
	p := Profile{Email: email}
	err := db.QueryRow(ctx, `
		SELECT nickname, status_message FROM english_zoa.users WHERE email = $1
	`, email).Scan(&p.Nickname, &p.StatusMessage)
	return p, err
}

func updateProfile(ctx context.Context, email, nickname, statusMessage string) error {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = defaultNickname(email)
	}
	if r := []rune(nickname); len(r) > 24 {
		nickname = string(r[:24])
	}
	if r := []rune(statusMessage); len(r) > 80 {
		statusMessage = string(r[:80])
	}
	_, err := db.Exec(ctx, `
		UPDATE english_zoa.users SET nickname = $2, status_message = $3 WHERE email = $1
	`, email, nickname, statusMessage)
	return err
}

// ── scoring ─────────────────────────────────────────────────────────────────

func getScore(ctx context.Context, email string) (int, error) {
	var score int
	err := db.QueryRow(ctx, `
		SELECT total_score FROM english_zoa.user_scores WHERE email = $1
	`, email).Scan(&score)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return score, err
}

func addScoreTx(ctx context.Context, tx pgx.Tx, email string, delta int) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO english_zoa.user_scores (email, total_score, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (email) DO UPDATE SET
			total_score = english_zoa.user_scores.total_score + $2,
			updated_at  = NOW()
	`, email, delta)
	return err
}

type LeaderboardEntry struct {
	Email      string `json:"email"`
	Nickname   string `json:"nickname"`
	TotalScore int    `json:"total_score"`
}

func getLeaderboard(ctx context.Context, limit int) ([]LeaderboardEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT u.email, u.nickname, s.total_score
		FROM english_zoa.user_scores s
		JOIN english_zoa.users u ON u.email = s.email
		ORDER BY s.total_score DESC, u.nickname ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []LeaderboardEntry{}
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.Email, &e.Nickname, &e.TotalScore); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ── login / streak / calendar ────────────────────────────────────────────────

type LoginEvent struct {
	Date        string `json:"date"`
	Time        string `json:"time"`
	StreakCount int    `json:"streak_count"`
}

// recordLogin records today's first request as a login event (idempotent —
// repeat calls the same day just return the existing streak) and extends the
// streak from yesterday's row if present. Every streakBonusEvery-th
// consecutive day awards a one-time bonus.
func recordLogin(ctx context.Context, email string, now time.Time) (int, error) {
	seoulNow := now.In(seoulTZ)
	today := seoulNow.Format("2006-01-02")
	yesterday := seoulNow.AddDate(0, 0, -1).Format("2006-01-02")

	var existingStreak int
	err := db.QueryRow(ctx, `
		SELECT streak_count FROM english_zoa.login_events WHERE email = $1 AND login_date = $2
	`, email, today).Scan(&existingStreak)
	if err == nil {
		return existingStreak, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	streak := 1
	var prevStreak int
	err = db.QueryRow(ctx, `
		SELECT streak_count FROM english_zoa.login_events WHERE email = $1 AND login_date = $2
	`, email, yesterday).Scan(&prevStreak)
	if err == nil {
		streak = prevStreak + 1
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO english_zoa.login_events (email, login_date, login_time, streak_count)
		VALUES ($1, $2, $3, $4)
	`, email, today, seoulNow, streak); err != nil {
		return 0, err
	}

	if streak%streakBonusEvery == 0 {
		tx, err := db.Begin(ctx)
		if err != nil {
			return streak, err
		}
		defer tx.Rollback(ctx)
		if err := addScoreTx(ctx, tx, email, streakBonusPoints); err != nil {
			return streak, err
		}
		if err := tx.Commit(ctx); err != nil {
			return streak, err
		}
	}
	return streak, nil
}

func getLoginHistory(ctx context.Context, email string, limit int) ([]LoginEvent, error) {
	rows, err := db.Query(ctx, `
		SELECT login_date, login_time, streak_count
		FROM english_zoa.login_events
		WHERE email = $1
		ORDER BY login_date DESC
		LIMIT $2
	`, email, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []LoginEvent{}
	for rows.Next() {
		var date, loginTime time.Time
		var streak int
		if err := rows.Scan(&date, &loginTime, &streak); err != nil {
			continue
		}
		events = append(events, LoginEvent{
			Date:        date.Format("2006-01-02"),
			Time:        loginTime.In(seoulTZ).Format("15:04"),
			StreakCount: streak,
		})
	}
	return events, rows.Err()
}

// ── phrases / attempts ────────────────────────────────────────────────────────

type Phrase struct {
	ID          int    `json:"id"`
	EnglishText string `json:"english_text"`
	KoreanText  string `json:"korean_text"`
	PhraseDate  string `json:"phrase_date"`
}

// ensureTodayPhrase returns today's phrase, creating it on first request of
// the day. Tries Slack first (fetchPhraseFromSlack, slack.go); falls back to
// a built-in phrase list when Slack isn't reachable/configured or parsing
// fails, so the flashcard always has something to show.
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
// everyone on the team sees the same fallback phrase on a given day.
var fallbackPhrases = []struct{ En, Ko string }{
	{"Better late than never.", "늦더라도 안 하는 것보다는 낫다."},
	{"Actions speak louder than words.", "말보다 행동이 중요하다."},
	{"Let's circle back on this.", "이건 나중에 다시 이야기해요."},
	{"I'll keep you posted.", "진행 상황 계속 알려드릴게요."},
	{"That rings a bell.", "그거 어디서 들어본 것 같아요."},
	{"Long story short...", "짧게 말하면..."},
	{"It's not rocket science.", "그렇게 어려운 일이 아니에요."},
	{"Let's touch base tomorrow.", "내일 잠깐 이야기해요."},
	{"I'm swamped today.", "오늘 너무 바빠요."},
	{"Give it a shot.", "한번 해봐요."},
}

func fallbackPhrase(now time.Time) (string, string) {
	p := fallbackPhrases[now.YearDay()%len(fallbackPhrases)]
	return p.En, p.Ko
}
