package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── versioned schema migrations ─────────────────────────────────────────────
// Replaces the old "every boot re-runs every feature file's *SchemaStmts"
// approach: each migration runs exactly once (recorded in
// phraseup.schema_migrations), inside one transaction, under a Postgres
// advisory lock so concurrent Cloud Run instances can't race each other.
//
// Rules:
//   - Never edit an applied migration — append a new version instead.
//   - Versions are strictly increasing (checked by pendingMigrations' test).
//   - Statements stay idempotent where cheap (IF NOT EXISTS) so the very
//     first run against the pre-migration production DB is a clean no-op.

type migration struct {
	version int
	name    string
	stmts   []string
}

var migrations = []migration{
	{1, "legacy cleanup", []string{
		// Retired points system (score.go) — quiz "score" is a plain COUNT now.
		`DROP TABLE IF EXISTS phraseup.user_scores`,
		// Removed flashcard screen (phrase.go).
		`DROP TABLE IF EXISTS phraseup.card_attempts`,
		// Old "infinite pool, one-shot per phrase" quiz design (quiz.go).
		`DROP TABLE IF EXISTS phraseup.quiz_attempts`,
		// First quiz_questions shape (date-based, single fixed daily set) is
		// superseded by the session-based one created in the next migration.
		`DO $$ BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'phraseup' AND table_name = 'quiz_questions' AND column_name = 'quiz_date'
			) THEN
				DROP TABLE phraseup.quiz_questions;
			END IF;
		END $$`,
		// Pre-rounds battles schema is replaced wholesale (battle.go).
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
	}},
	{2, "baseline schema", []string{
		// profile.go — users first: everything else references it via FK.
		`CREATE TABLE IF NOT EXISTS phraseup.users (
			email          TEXT PRIMARY KEY,
			nickname       TEXT NOT NULL DEFAULT '',
			status_message TEXT NOT NULL DEFAULT '',
			last_active_at TIMESTAMPTZ,
			goal_vocab     INT NOT NULL DEFAULT 10,
			goal_phrase    INT NOT NULL DEFAULT 5,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE phraseup.users ADD COLUMN IF NOT EXISTS last_active_at TIMESTAMPTZ`,
		`ALTER TABLE phraseup.users ADD COLUMN IF NOT EXISTS goal_vocab INT NOT NULL DEFAULT 10`,
		`ALTER TABLE phraseup.users ADD COLUMN IF NOT EXISTS goal_phrase INT NOT NULL DEFAULT 5`,
		`CREATE TABLE IF NOT EXISTS phraseup.login_events (
			email        TEXT NOT NULL REFERENCES phraseup.users(email) ON DELETE CASCADE,
			login_date   DATE NOT NULL,
			login_time   TIMESTAMPTZ NOT NULL,
			streak_count INT NOT NULL DEFAULT 1,
			PRIMARY KEY (email, login_date)
		)`,
		// phrase.go — phrase_date is nullable (only the Slack daily source
		// sets it); english_text unique so bulk seeding and AI top-ups can
		// both use ON CONFLICT DO NOTHING freely.
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
		`ALTER TABLE phraseup.phrases ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'curated'`,
		// quiz.go — session-based quiz questions.
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
		// 'review' sessions reuse the same table with a third track value.
		`ALTER TABLE phraseup.quiz_questions DROP CONSTRAINT IF EXISTS quiz_questions_track_check`,
		`ALTER TABLE phraseup.quiz_questions ADD CONSTRAINT quiz_questions_track_check CHECK (track IN ('vocab', 'phrase', 'review'))`,
		// tedtalk.go — comments keyed by video_id (the rotation repeats).
		`CREATE TABLE IF NOT EXISTS phraseup.tedtalk_comments (
			id         SERIAL PRIMARY KEY,
			video_id   TEXT NOT NULL,
			email      TEXT NOT NULL REFERENCES phraseup.users(email) ON DELETE CASCADE,
			body       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS tedtalk_comments_video_id_idx ON phraseup.tedtalk_comments (video_id)`,
		// translate.go — server-side cache by normalized-input hash.
		`CREATE TABLE IF NOT EXISTS phraseup.translation_cache (
			input_hash       TEXT PRIMARY KEY,
			input_text       TEXT NOT NULL,
			detected_lang    TEXT NOT NULL,
			translation      TEXT NOT NULL,
			business_version TEXT NOT NULL,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// news.go — one cached story per day.
		`CREATE TABLE IF NOT EXISTS phraseup.daily_news (
			news_date  DATE PRIMARY KEY,
			title      TEXT NOT NULL,
			summary    TEXT NOT NULL,
			summary_ko TEXT NOT NULL DEFAULT '',
			url        TEXT NOT NULL,
			image_url  TEXT NOT NULL DEFAULT '',
			source     TEXT NOT NULL DEFAULT 'BBC Business',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE phraseup.daily_news ADD COLUMN IF NOT EXISTS summary_ko TEXT NOT NULL DEFAULT ''`,
		// battle.go — battles, rounds, players.
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
		// Team-race additions: races reuse host_score/guest_score as LEFT/
		// RIGHT team scores; winner_team carries the result for team matches.
		`ALTER TABLE phraseup.battles ADD COLUMN IF NOT EXISTS team_left_name  TEXT NOT NULL DEFAULT 'Team Blue'`,
		`ALTER TABLE phraseup.battles ADD COLUMN IF NOT EXISTS team_right_name TEXT NOT NULL DEFAULT 'Team Red'`,
		`ALTER TABLE phraseup.battles ADD COLUMN IF NOT EXISTS winner_team TEXT`,
		`ALTER TABLE phraseup.battles ADD COLUMN IF NOT EXISTS duration_seconds INT NOT NULL DEFAULT 60`,
		`ALTER TABLE phraseup.battles ADD COLUMN IF NOT EXISTS ends_at TIMESTAMPTZ`,
		`CREATE TABLE IF NOT EXISTS phraseup.battle_players (
			battle_id INT  NOT NULL REFERENCES phraseup.battles(id) ON DELETE CASCADE,
			email     TEXT NOT NULL REFERENCES phraseup.users(email) ON DELETE CASCADE,
			team      TEXT NOT NULL CHECK (team IN ('left', 'right')),
			joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (battle_id, email)
		)`,
		`ALTER TABLE phraseup.battle_rounds ADD COLUMN IF NOT EXISTS hints JSONB NOT NULL DEFAULT '{}'`,
		`ALTER TABLE phraseup.battle_players ADD COLUMN IF NOT EXISTS lines   INT  NOT NULL DEFAULT 0`,
		`ALTER TABLE phraseup.battle_players ADD COLUMN IF NOT EXISTS garbage INT  NOT NULL DEFAULT 0`,
		`ALTER TABLE phraseup.battle_players ADD COLUMN IF NOT EXISTS dead    BOOL NOT NULL DEFAULT FALSE`,
	}},
	{3, "one-time gloss fixes", []string{
		// Korean answers that were transliterations of the English word made
		// those questions trivially guessable (phrase.go).
		`UPDATE phraseup.phrases SET korean_text = '상승 효과' WHERE english_text = 'synergy' AND korean_text LIKE '%시너지%'`,
		`UPDATE phraseup.phrases SET korean_text = '(비교) 기준점, 성능 지표' WHERE english_text = 'benchmark' AND korean_text LIKE '%벤치마크%'`,
		`UPDATE phraseup.phrases SET korean_text = '(제품·사업) 추진 계획' WHERE english_text = 'roadmap' AND korean_text LIKE '%로드맵%'`,
		`UPDATE phraseup.phrases SET korean_text = '신규 인력 적응 지원 과정' WHERE english_text = 'onboarding' AND korean_text LIKE '%온보딩%'`,
	}},
	{4, "translate daily usage counter", []string{
		// Per-user daily cap on the translator's Claude calls (translate.go)
		// — Postgres-backed so it stays correct across Cloud Run instances.
		`CREATE TABLE IF NOT EXISTS phraseup.translate_usage (
			email      TEXT NOT NULL,
			usage_date DATE NOT NULL,
			calls      INT  NOT NULL DEFAULT 0,
			PRIMARY KEY (email, usage_date)
		)`,
	}},
}

// migrationLockKey is an arbitrary app-wide advisory-lock id; it only has to
// differ from other advisory locks on the shared exp-db instance.
const migrationLockKey int64 = 727270_2026

// pendingMigrations returns, in order, the migrations not yet applied.
// Split out from runMigrations so the skip logic is testable without a DB.
func pendingMigrations(applied map[int]bool) []migration {
	var out []migration
	for _, m := range migrations {
		if !applied[m.version] {
			out = append(out, m)
		}
	}
	return out
}

// runMigrations applies pending migrations, one transaction each, holding a
// session-level advisory lock so only one instance migrates at a time.
func runMigrations(ctx context.Context) error {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrations: acquire conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("migrations: advisory lock: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS phraseup.schema_migrations (
		version    INT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("migrations: bookkeeping table: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM phraseup.schema_migrations`)
	if err != nil {
		return fmt.Errorf("migrations: read applied: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("migrations: scan applied: %w", err)
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrations: read applied: %w", err)
	}

	for _, m := range pendingMigrations(applied) {
		if err := applyMigration(ctx, conn, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		log.Printf("migration %d applied: %s", m.version, m.name)
	}
	return nil
}

// applyMigration runs one migration's statements plus its bookkeeping row in
// a single transaction, so a failure partway leaves nothing recorded.
func applyMigration(ctx context.Context, conn *pgxpool.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(context.Background()) // no-op after Commit

	for _, s := range m.stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO phraseup.schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit(ctx)
}
