// Package handlers implements the three naviamp-sidecar HTTP endpoints and the
// auth middleware that protects them. All handlers produce JSON responses using
// the Subsonic envelope format so the Naviamp client's existing
// SubsonicEnvelope.unwrap() logic works without modification.
//
// # Route layout
//
//	GET /naviamp/capabilities   — sidecar probe; returns version + feature list
//	GET /naviamp/changes        — delta sync; returns changed IDs since a timestamp
//	GET /naviamp/artistTracks   — performing-artist track browse
//
// All routes are protected by the authMiddleware, which extracts u/t/s params
// from the query string and forwards them to Navidrome for verification.
package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/happyarch/naviamp-sidecar/internal/auth"
	"github.com/happyarch/naviamp-sidecar/internal/model"
)

// writeJSON serialises v to w with Content-Type: application/json and the
// given HTTP status code. A JSON marshal error is logged and results in a
// 500 response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// writeSubsonicError writes a Subsonic error envelope with the given code and
// message and sets the HTTP status to httpStatus. Used uniformly across all
// handlers so the client always receives a parseable Subsonic response even on
// errors.
func writeSubsonicError(w http.ResponseWriter, httpStatus, code int, message string) {
	writeJSON(w, httpStatus, model.ErrorEnvelope(code, message))
}

// authMiddleware extracts the Subsonic auth query parameters from the request
// and verifies them against Navidrome before calling the next handler.
//
// Required query parameters (same as the OpenSubsonic API spec):
//
//	u  — username
//	t  — MD5 token = md5(password + salt), hex-encoded lowercase
//	s  — random salt used to compute t (min 6 characters)
//
// On auth failure the middleware returns HTTP 401 with a Subsonic error
// envelope (code 40 for wrong credentials, code 10 for missing parameters).
// The next handler is only called if auth succeeds.
//
// navidromeURL must be the base URL of the Navidrome instance, e.g.
// "http://localhost:4533", with no trailing slash.
func authMiddleware(navidromeURL string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		u := q.Get("u")
		t := q.Get("t")
		s := q.Get("s")

		// Code 10 = required parameter missing (Subsonic spec §Error handling).
		if u == "" || t == "" || s == "" {
			writeSubsonicError(w, http.StatusUnauthorized, 10,
				"Required parameter is missing: u, t, and s are required.")
			return
		}

		if err := auth.Verify(navidromeURL, u, t, s); err != nil {
			var ve *auth.VerifyError
			if errors.As(err, &ve) {
				writeSubsonicError(w, http.StatusUnauthorized, ve.Code, ve.Message)
			} else {
				// Network or parse error reaching Navidrome — return 503 so the
				// client can distinguish "bad credentials" (401) from "sidecar
				// can't reach Navidrome" (503).
				slog.Error("auth verification error", "error", err)
				writeSubsonicError(w, http.StatusServiceUnavailable, 0,
					"Could not verify credentials with Navidrome.")
			}
			return
		}

		next(w, r)
	}
}

// RegisterRoutes wires all naviamp-sidecar routes onto mux and returns it.
// Every route is protected by authMiddleware except /naviamp/capabilities,
// which also requires auth (the client includes credentials in the probe
// so the sidecar can validate them on first contact).
//
// navidromeURL is passed through to authMiddleware for credential forwarding.
func RegisterRoutes(mux *http.ServeMux, navidromeURL string, h *Handler) {
	protect := func(fn http.HandlerFunc) http.HandlerFunc {
		return authMiddleware(navidromeURL, fn)
	}

	mux.HandleFunc("GET /naviamp/capabilities", protect(h.Capabilities))
	mux.HandleFunc("GET /naviamp/changes", protect(h.Changes))
	mux.HandleFunc("GET /naviamp/artistTracks", protect(h.ArtistTracks))
}
