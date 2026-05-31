// naviamp-sidecar is a lightweight read-only HTTP service that runs alongside
// a Navidrome music server and exposes additional endpoints used by the Naviamp
// Flutter client (https://github.com/Happyarch/naviamp).
//
// # What it does
//
// The standard OpenSubsonic protocol that Navidrome implements has two gaps
// relative to what the Naviamp client needs:
//
//  1. No delta-sync mechanism — clients must rescan the entire library on every
//     launch. The sidecar's /naviamp/changes endpoint returns only items whose
//     updated_at timestamp advanced since the client's last sync.
//
//  2. getArtist only surfaces album artists — tracks where an artist appears as
//     a performing credit on another artist's album are invisible. The sidecar's
//     /naviamp/artistTracks endpoint queries media_file.artist_id (performing
//     credit) rather than album_artist_id (album credit).
//
// # What it does NOT do
//
//   - It never writes to the Navidrome database.
//   - It never serves audio files or exposes file paths.
//   - It never stores or validates credentials — all auth is delegated to
//     Navidrome via a forwarded ping request.
//
// # Quick start
//
//	export NAVIAMP_NAVIDROME_URL=http://localhost:4533
//	export NAVIAMP_DB_PATH=/var/lib/navidrome/navidrome.db
//	./naviamp-sidecar
//
// See README.md and API.md for full configuration and client integration docs.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/happyarch/naviamp-sidecar/internal/config"
	"github.com/happyarch/naviamp-sidecar/internal/db"
	"github.com/happyarch/naviamp-sidecar/internal/handlers"
)

func main() {
	// Structured logging to stderr so container log collectors (Docker, journald)
	// can parse severity levels without additional configuration.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg)
	if err != nil {
		slog.Error("database error", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	slog.Info("naviamp-sidecar starting",
		"listen", cfg.Listen,
		"navidrome", cfg.NavidromeURL,
		"dbType", cfg.DBType,
	)

	h := handlers.New(database, cfg)
	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, cfg.NavidromeURL, h)

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Run the server in a goroutine and listen for SIGINT/SIGTERM so the
	// process shuts down cleanly inside Docker Compose (SIGTERM from `docker
	// compose stop`) or systemd (SIGTERM from `systemctl stop`).
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
