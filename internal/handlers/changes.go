package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/happyarch/naviamp-sidecar/internal/db"
	"github.com/happyarch/naviamp-sidecar/internal/model"
)

// Changes handles GET /naviamp/changes?since=<unix-milliseconds>.
//
// # Purpose
//
// Returns all artists, albums, and tracks whose updated_at timestamp is
// strictly greater than the "since" value, plus a list of item IDs that were
// soft-deleted after that timestamp. The client uses this response to perform
// a targeted (delta) sync instead of re-fetching the entire library.
//
// # Query parameters
//
//	since  (required for incremental sync, optional for full sync)
//	       Unix timestamp in milliseconds. Items modified after this point
//	       are included in the response.
//	       If 0 or omitted, all non-deleted items are returned, which is
//	       equivalent to a first-run full sync.
//
// # Client usage pattern
//
//  1. On first install the client calls /naviamp/changes (no "since") to
//     build its initial local library.
//  2. On subsequent launches it passes its locally stored "last sync time"
//     as "since" to fetch only what changed.
//  3. For each ID in artists/albums/songs the client calls the corresponding
//     standard Subsonic endpoint (getArtist, getAlbum, getSong) to refresh
//     the full metadata — the sidecar returns IDs and timestamps only, not
//     full metadata, to keep response sizes manageable.
//  4. IDs in deletedIds are removed from the client's local Isar database
//     without making any additional requests.
//
// # Response (wrapped in Subsonic envelope)
//
//	{
//	  "subsonic-response": {
//	    "status": "ok",
//	    "version": "1.16.1",
//	    "changes": {
//	      "artists":    [{ "id": "ar-1", "updatedAt": 1716900000000 }],
//	      "albums":     [{ "id": "al-1", "updatedAt": 1716900000000 }],
//	      "songs":      [{ "id": "tr-1", "updatedAt": 1716900000000 }],
//	      "deletedIds": ["tr-99", "al-88"]
//	    }
//	  }
//	}
//
// updatedAt values are Unix milliseconds — the same unit as Subsonic's "time"
// parameter — so the client can use any returned updatedAt as its next "since".
func (h *Handler) Changes(w http.ResponseWriter, r *http.Request) {
	sinceParam := r.URL.Query().Get("since")

	// Parse the "since" millisecond timestamp. A missing or zero value means
	// "return everything" (first-run full sync).
	var sinceMS int64
	if sinceParam != "" {
		var err error
		sinceMS, err = strconv.ParseInt(sinceParam, 10, 64)
		if err != nil {
			writeSubsonicError(w, http.StatusBadRequest, 10,
				"'since' must be a Unix timestamp in milliseconds.")
			return
		}
	}

	since := time.UnixMilli(sinceMS).UTC()

	artists, err := db.QueryChangedArtists(h.db, since, h.dbType)
	if err != nil {
		slog.Error("query changed artists", "error", err)
		writeSubsonicError(w, http.StatusInternalServerError, 0, "Internal error.")
		return
	}

	albums, err := db.QueryChangedAlbums(h.db, since, h.dbType)
	if err != nil {
		slog.Error("query changed albums", "error", err)
		writeSubsonicError(w, http.StatusInternalServerError, 0, "Internal error.")
		return
	}

	songs, err := db.QueryChangedSongs(h.db, since, h.dbType)
	if err != nil {
		slog.Error("query changed songs", "error", err)
		writeSubsonicError(w, http.StatusInternalServerError, 0, "Internal error.")
		return
	}

	deleted, err := db.QueryDeletedIDs(h.db, since, h.dbType)
	if err != nil {
		slog.Error("query deleted ids", "error", err)
		writeSubsonicError(w, http.StatusInternalServerError, 0, "Internal error.")
		return
	}

	changes := model.ChangesResponse{
		Artists:    artists,
		Albums:     albums,
		Songs:      songs,
		DeletedIDs: deleted,
	}

	writeJSON(w, http.StatusOK, model.OKEnvelope("changes", changes))
}
