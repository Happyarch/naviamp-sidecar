// Package db manages the read-only connection to Navidrome's database.
//
// # Supported databases
//
// Navidrome supports SQLite (default for home deployments), PostgreSQL, and
// MySQL. The sidecar supports all three. The driver is selected at startup
// from the NAVIAMP_DB_TYPE environment variable.
//
// # SQLite read-only mode
//
// When connecting to SQLite the sidecar uses the URI file format with
// mode=ro so the OS-level file permissions alone prevent accidental writes.
// WAL journal mode is specified to allow the sidecar to read concurrently
// with Navidrome's write transactions without locking the database file.
//
// # Pure-Go SQLite driver
//
// The sidecar uses modernc.org/sqlite — a pure-Go SQLite port — instead of
// github.com/mattn/go-sqlite3 (which requires CGO). This keeps the build
// fully CGO-free and enables production of a truly static binary with
// GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build.
package db

import (
	"database/sql"
	"fmt"

	"github.com/happyarch/naviamp-sidecar/internal/config"

	// Register the pure-Go SQLite driver under the name "sqlite".
	_ "modernc.org/sqlite"

	// Register the PostgreSQL driver under the name "postgres".
	_ "github.com/lib/pq"

	// Register the MySQL driver under the name "mysql".
	_ "github.com/go-sql-driver/mysql"
)

// Open returns a *sql.DB connected to the Navidrome database described by cfg.
// The connection is opened in read-only mode where the driver supports it.
//
// Callers should defer db.Close() and should not hold the returned *sql.DB
// beyond the lifetime of the server process.
func Open(cfg config.Config) (*sql.DB, error) {
	switch cfg.DBType {
	case "sqlite":
		return openSQLite(cfg.DBPath)
	case "postgres":
		return openPostgres(cfg.DBDSN)
	case "mysql":
		return openMySQL(cfg.DBDSN)
	default:
		// config.Validate() prevents this at startup, but guard defensively.
		return nil, fmt.Errorf("unsupported DB type %q", cfg.DBType)
	}
}

// openSQLite opens the SQLite database at path in read-only WAL mode.
//
// URI parameters:
//   - mode=ro         — OS-level read-only; writes return SQLITE_READONLY
//   - _journal=WAL    — use WAL mode so readers don't block writers and vice
//     versa; critical when Navidrome is actively scanning the library
//   - _busy_timeout=5000 — wait up to 5 s if a write lock is held before
//     returning SQLITE_BUSY
func openSQLite(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal=WAL&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// Limit to a single connection. SQLite handles concurrent reads fine in WAL
	// mode but multiple connections to the same file via the modernc driver can
	// cause "database is locked" errors if not carefully managed.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}
	return db, nil
}

// openPostgres opens a PostgreSQL connection using the provided DSN.
// The DSN follows the libpq format:
//
//	postgres://user:pass@host/dbname?sslmode=disable
func openPostgres(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return db, nil
}

// openMySQL opens a MySQL/MariaDB connection using the provided DSN.
// The DSN follows the go-sql-driver/mysql format:
//
//	user:pass@tcp(host:3306)/dbname?parseTime=true
//
// parseTime=true is required so that Navidrome's DATETIME columns are scanned
// into time.Time values correctly.
func openMySQL(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("mysql ping: %w", err)
	}
	return db, nil
}
