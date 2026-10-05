package main

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // enregistre le pilote "pgx" auprès de database/sql
)

// postgresDialect décrit PostgreSQL. pgx accepte l'URL postgresql:// telle
// que Cassiopée la fournit.
var postgresDialect = dialect{
	engine:     EnginePostgres,
	driver:     "pgx",
	dsn:        DBTarget.URL,
	createSQL:  "CREATE TABLE IF NOT EXISTS canary_hits (id BIGSERIAL PRIMARY KEY, pod TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())",
	insertSQL:  "INSERT INTO canary_hits (pod) VALUES ($1)",
	versionSQL: "SELECT 'PostgreSQL ' || current_setting('server_version')",
	isAuthError: func(err error) bool {
		// 28P01 : mot de passe invalide ; 28000 : autorisation refusée.
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && (pgErr.Code == "28P01" || pgErr.Code == "28000")
	},
}
