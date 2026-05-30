package handlers

import (
	"net/http"

	"github.com/happyarch/naviamp-sidecar/internal/model"
)

// sidescarVersion is the naviamp-sidecar semantic version reported in the
// capabilities response. This is the sidecar's own version, distinct from the
// Subsonic protocol version ("1.16.1") embedded in the response envelope.
//
// Increment the major version whenever an existing endpoint's response schema
// changes in a breaking way. Adding new features or endpoints is a minor bump.
// The client does NOT gate on this value; it checks feature strings instead.
const sidecarVersion = "1.0.0"

// activeFeatures is the list of capability tokens the sidecar currently
// implements. The Naviamp client performs an exact string match on each element
// to decide which behaviours to enable:
//
//	"delta-sync"         → use GET /naviamp/changes for incremental library sync
//	                       instead of a full Subsonic library rescan on startup.
//
//	"performing-artists" → use GET /naviamp/artistTracks to browse all tracks
//	                       where an artist appears as a performing credit, not
//	                       just tracks on albums where they are the album artist.
//
// To disable a feature without removing its endpoint, remove the string from
// this slice. The client will fall back to standard Subsonic behaviour for
// that feature automatically.
var activeFeatures = []string{
	"delta-sync",
	"performing-artists",
}

// Capabilities handles GET /naviamp/capabilities.
//
// # Purpose
//
// This is the probe endpoint the Naviamp client calls on startup (or after
// login) to detect whether the naviamp-sidecar is running alongside Navidrome.
//
// # Client behaviour
//
// The client expects:
//  1. HTTP 200 status.
//  2. A JSON body with a string "version" field and a []string "features" field.
//
// Any other response — including a non-200 status, a network error, or a
// malformed body — causes the client to silently conclude the sidecar is absent
// and fall back to standard OpenSubsonic-only behaviour. No error is shown to
// the user.
//
// The client caches the probe result in a Riverpod state provider and persists
// it locally. Subsequent API calls read the cached state synchronously; there
// is no per-request network round-trip to the sidecar for feature detection.
//
// # Auth
//
// The capabilities endpoint requires auth (the client includes u/t/s on the
// probe). This serves two purposes:
//  1. It validates that the credentials work end-to-end on first contact.
//  2. It prevents unauthenticated enumeration of the sidecar's capabilities.
//
// # Response (wrapped in Subsonic envelope)
//
//	{
//	  "subsonic-response": {
//	    "status": "ok",
//	    "version": "1.16.1",
//	    "capabilities": {
//	      "version":  "1.0.0",
//	      "features": ["delta-sync", "performing-artists"]
//	    }
//	  }
//	}
func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	resp := model.CapabilitiesResponse{
		Version:  sidecarVersion,
		Features: activeFeatures,
	}
	writeJSON(w, http.StatusOK, model.OKEnvelope("capabilities", resp))
}
