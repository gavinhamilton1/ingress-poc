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

	return db
}
