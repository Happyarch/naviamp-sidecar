package handlers

import (
	"database/sql"

	"github.com/happyarch/naviamp-sidecar/internal/config"
)

// Handler holds the shared dependencies injected into all endpoint handlers.
// A single Handler instance is created at startup and reused across requests.
type Handler struct {
	// db is the read-only connection to the Navidrome database.
	// All queries run through db/queries.go which uses parameterised statements.
	db *sql.DB

	// dbType is "sqlite", "postgres", or "mysql". Passed to query functions
	// so they can rewrite "?" placeholders to "$N" for PostgreSQL.
	dbType string
}

// New creates a Handler wired to the given database connection.
func New(db *sql.DB, cfg config.Config) *Handler {
	return &Handler{db: db, dbType: cfg.DBType}
}
