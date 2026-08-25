package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"

	"cloud.google.com/go/cloudsqlconn"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// db is the shared Postgres pool (Apps Platform's shared exp-db, `phraseup`
// schema). Scoring/leaderboard has no meaning without persistence, so unlike
// a live-fetch dashboard we don't try to run without it (see main.go).
//
// Schema is assembled from each feature file's own *SchemaStmts slice
// (profile.go, phrase.go, score.go, quiz.go) so a section's table definition
// travels with the rest of that section's code — see initSchema below.
var db *pgxpool.Pool

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
	log.Println("DB ready (phraseup schema)")
	return nil
}

func dbAvailable() bool { return db != nil }

// initSchema runs the shared schema-create statement, then each section's
// own table statements in dependency order (profile's `users` table first,
// since phrase/score/quiz tables reference it via foreign key).
func initSchema(ctx context.Context) error {
	stmts := []string{`CREATE SCHEMA IF NOT EXISTS phraseup`}
	stmts = append(stmts, profileSchemaStmts...)
	stmts = append(stmts, phraseSchemaStmts...)
	stmts = append(stmts, scoreSchemaStmts...)
	stmts = append(stmts, quizSchemaStmts...)
	stmts = append(stmts, tedtalkSchemaStmts...)
	stmts = append(stmts, translateSchemaStmts...)
	stmts = append(stmts, newsSchemaStmts...)
	stmts = append(stmts, battleSchemaStmts...)

	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			if s == `CREATE SCHEMA IF NOT EXISTS phraseup` && canContinueSchemaCreate(err, schemaExists(ctx, "phraseup")) {
				log.Println("DB schema phraseup exists; continuing without database CREATE privilege")
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
