package main

import (
	"log"
	"os"

	"github.com/jmoiron/sqlx"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func initDB() *sqlx.DB {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://ingress:ingress_poc@postgres:5432/ingress_registry?sslmode=disable"
	}

	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	// Run migrations in order
	migrations := []string{
		"migrations/001_init.sql",
		"migrations/002_k8s_migration.sql",
	}
	for _, mf := range migrations {
		schema, err := os.ReadFile(mf)
		if err != nil {
			log.Printf("Warning: could not read migration file %s: %v", mf, err)
			continue
		}
		db.MustExec(string(schema))
		log.Printf("Applied migration: %s", mf)
	}

	// Inline migrations (not file-based, always safe to re-run due to IF NOT EXISTS / WHERE guards)
	inlineMigrations := []struct {
		name string
		sql  string
	}{
		{
			name: "003_fleet_k8s_name",
			sql: `ALTER TABLE fleets ADD COLUMN IF NOT EXISTS k8s_name TEXT DEFAULT '';
UPDATE fleets SET k8s_name = id WHERE k8s_name = '' OR k8s_name IS NULL;`,
		},
		{
			name: "004_route_payload_policy_ref",
			sql:  `ALTER TABLE routes ADD COLUMN IF NOT EXISTS payload_policy_ref TEXT DEFAULT '';`,
		},
		{
			name: "005_route_payload_policy_rego",
			sql: `ALTER TABLE routes ADD COLUMN IF NOT EXISTS payload_policy_rego TEXT DEFAULT '';
-- Rego source for payload_policy_ref, evaluated in-process by auth-service
-- (via the OPA Go SDK) rather than pushed to a separate OPA service — see
-- checkPayloadOPA in cmd/auth-service/payload_policy.go.`,
		},
		{
			name: "006_global_payload_policy",
			sql: `CREATE TABLE IF NOT EXISTS global_payload_policy (
	id TEXT PRIMARY KEY DEFAULT 'global',
	rego_source TEXT NOT NULL,
	updated_at DOUBLE PRECISION NOT NULL
);
-- Single-row table: one platform-wide policy (package
-- ingress.policy.payload.global), evaluated by auth-service for every
-- route with a request body — before, and independent of, any
-- route-specific payload_policy_ref. This is the lever for "patch every
-- route against a new attack pattern in one place" rather than having to
-- regenerate every route's own policy.`,
		},
	}
	for _, m := range inlineMigrations {
		if _, err := db.Exec(m.sql); err != nil {
			log.Printf("Warning: inline migration %s failed: %v", m.name, err)
		} else {
			log.Printf("Applied inline migration: %s", m.name)
		}
	}

	return db
}
