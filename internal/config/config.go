// Package config loads naviamp-sidecar configuration from environment variables
// and an optional YAML file. Environment variables always take precedence over
// file values, following the twelve-factor app convention.
//
// # Minimal setup (SQLite, which covers most home Navidrome deployments)
//
//	NAVIAMP_NAVIDROME_URL=http://localhost:4533
//	NAVIAMP_DB_PATH=/var/lib/navidrome/navidrome.db
//
// # PostgreSQL or MySQL
//
//	NAVIAMP_DB_TYPE=postgres
//	NAVIAMP_DB_DSN=postgres://navidrome:pass@localhost/navidrome?sslmode=disable
//
// # Optional YAML file (naviamp.yaml next to the binary or at the path in
// NAVIAMP_CONFIG_FILE)
//
//	navidromeUrl: http://localhost:4533
//	dbType: sqlite
//	dbPath: /var/lib/navidrome/navidrome.db
//	listen: :8090
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration for the sidecar. Every field can be
// set via environment variable; the YAML file provides an alternative for users
// who prefer file-based configuration.
type Config struct {
	// NavidromeURL is the base URL of the Navidrome instance the sidecar
	// authenticates against. Every incoming request is verified by forwarding
	// a ping to <NavidromeURL>/rest/ping.view with the same credentials.
	// Must NOT have a trailing slash.
	// Env: NAVIAMP_NAVIDROME_URL (required)
	NavidromeURL string `yaml:"navidromeUrl" env:"NAVIAMP_NAVIDROME_URL"`

	// DBType selects the Navidrome database driver.
	// Valid values: "sqlite" (default), "postgres", "mysql".
	// Env: NAVIAMP_DB_TYPE
	DBType string `yaml:"dbType" env:"NAVIAMP_DB_TYPE"`

	// DBPath is the filesystem path to the Navidrome SQLite database file.
	// Only used when DBType is "sqlite".
	// The sidecar opens the file in read-only WAL mode to avoid conflicting
	// with Navidrome's own write transactions.
	// Env: NAVIAMP_DB_PATH
	DBPath string `yaml:"dbPath" env:"NAVIAMP_DB_PATH"`

	// DBDSN is the data source name for PostgreSQL or MySQL connections.
	// Only used when DBType is "postgres" or "mysql".
	// Examples:
	//   postgres://navidrome:pass@localhost/navidrome?sslmode=disable
	//   navidrome:pass@tcp(localhost:3306)/navidrome?parseTime=true
	// Env: NAVIAMP_DB_DSN
	DBDSN string `yaml:"dbDsn" env:"NAVIAMP_DB_DSN"`

	// Listen is the TCP address the sidecar binds to.
	// Use the same form as Go's net.Listen ("host:port" or ":port").
	// Default: ":8090" — deliberately different from Navidrome's 4533 so both
	// can run on the same host without conflict. A reverse proxy (nginx, Caddy)
	// can then expose both at the same public hostname.
	// Env: NAVIAMP_LISTEN
	Listen string `yaml:"listen" env:"NAVIAMP_LISTEN"`

	// ConfigFile is the optional path to a YAML config file.
	// Defaults to "naviamp.yaml" next to the binary if the file exists.
	// Env: NAVIAMP_CONFIG_FILE
	ConfigFile string `yaml:"-" env:"NAVIAMP_CONFIG_FILE"`
}

// Load returns a Config populated first from the optional YAML file, then
// overridden by any environment variables that are set. This means operators
// can use the YAML file as a base and override individual values via env
// (useful in Docker Compose where env vars are the natural override mechanism).
func Load() (Config, error) {
	cfg := defaults()

	// Determine config file path: explicit env var, then "naviamp.yaml" in CWD.
	cfgFile := os.Getenv("NAVIAMP_CONFIG_FILE")
	if cfgFile == "" {
		cfgFile = "naviamp.yaml"
	}

	if err := loadYAML(cfgFile, &cfg); err != nil {
		return Config{}, fmt.Errorf("config file %q: %w", cfgFile, err)
	}

	// Environment variables override whatever the file set.
	applyEnv(&cfg)

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// defaults returns a Config pre-populated with safe, sensible values so that
// a minimal deployment only needs to set NavidromeURL and DBPath.
func defaults() Config {
	return Config{
		DBType: "sqlite",
		Listen: ":8090",
	}
}

// loadYAML reads a YAML file into cfg. If the file does not exist the function
// is a no-op — the file is optional. Any other read or parse error is returned.
func loadYAML(path string, cfg *Config) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true) // reject unknown keys so typos are caught early
	return dec.Decode(cfg)
}

// applyEnv overrides cfg fields with environment variables when they are set.
// Only non-empty env values override; unset variables leave the file/default
// value intact.
func applyEnv(cfg *Config) {
	if v := os.Getenv("NAVIAMP_NAVIDROME_URL"); v != "" {
		cfg.NavidromeURL = v
	}
	if v := os.Getenv("NAVIAMP_DB_TYPE"); v != "" {
		cfg.DBType = v
	}
	if v := os.Getenv("NAVIAMP_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("NAVIAMP_DB_DSN"); v != "" {
		cfg.DBDSN = v
	}
	if v := os.Getenv("NAVIAMP_LISTEN"); v != "" {
		cfg.Listen = v
	}
}

// validate checks that all required and mutually-exclusive fields are present
// and consistent. Returns a human-readable error that can be printed directly.
func validate(cfg Config) error {
	if cfg.NavidromeURL == "" {
		return fmt.Errorf("NAVIAMP_NAVIDROME_URL is required")
	}
	if strings.HasSuffix(cfg.NavidromeURL, "/") {
		return fmt.Errorf("NAVIAMP_NAVIDROME_URL must not have a trailing slash")
	}

	switch cfg.DBType {
	case "sqlite":
		if cfg.DBPath == "" {
			return fmt.Errorf("NAVIAMP_DB_PATH is required when NAVIAMP_DB_TYPE=sqlite")
		}
	case "postgres", "mysql":
		if cfg.DBDSN == "" {
			return fmt.Errorf("NAVIAMP_DB_DSN is required when NAVIAMP_DB_TYPE=%s", cfg.DBType)
		}
	default:
		return fmt.Errorf("NAVIAMP_DB_TYPE must be sqlite, postgres, or mysql (got %q)", cfg.DBType)
	}

	return nil
}
