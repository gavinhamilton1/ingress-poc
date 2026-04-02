package main

import (
	"context"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// initDB connects to Postgres and ensures the auth_sessions table exists.
// Returns nil if DATABASE_URL is not configured or the connection fails —
// the store then operates in in-memory-only mode (sessions lost on pod restart).
func initDB() *sqlx.DB {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Printf("auth-service: DATABASE_URL not set — sessions will be in-memory only (lost on pod restart)")
		return nil
	}

	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		log.Printf("auth-service: Postgres connect failed, falling back to in-memory sessions: %v", err)
		return nil
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(3)

	schema := `
CREATE TABLE IF NOT EXISTS auth_sessions (
    sid        TEXT PRIMARY KEY,
    sub        TEXT NOT NULL,
    email      TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    roles      JSONB NOT NULL DEFAULT '[]',
    entity     TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    dpop_jkt   TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'active'
);
CREATE INDEX IF NOT EXISTS auth_sessions_sub_idx     ON auth_sessions(sub);
CREATE INDEX IF NOT EXISTS auth_sessions_expires_idx ON auth_sessions(expires_at);
CREATE INDEX IF NOT EXISTS auth_sessions_status_idx  ON auth_sessions(status);
`
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		log.Printf("auth-service: failed to initialise auth_sessions table (non-fatal): %v", err)
	}

	log.Printf("auth-service: connected to Postgres — sessions will persist across pod restarts")
	return db
}
