package handlers

import (
	"log/slog"
	"net/http"

	"github.com/happyarch/naviamp-sidecar/internal/db"
	"github.com/happyarch/naviamp-sidecar/internal/model"
)

// ArtistTracks handles GET /naviamp/artistTracks?id=<artistId>.
//
// # Purpose
//
// Returns all tracks in Navidrome's library where the given artist appears as
// a performing credit — that is, where media_file.artist_id matches the
// requested artist ID.
//
// # Why this endpoint is necessary
//
// The OpenSubsonic protocol's getArtist endpoint returns only albums where the
// artist is the ALBUM artist (album_artist_id). It does not surface tracks
// where the artist appears as a featured/guest performer on another artist's
// album.
//
// Navidrome's database stores both credits separately:
//
//	media_file.artist_id       = performing artist (the track-level credit)
//	media_file.album_artist_id = album artist (the album-level credit)
//
// Example: jazz pianist A appears as a sideman on an album credited to B.
//   - Navidrome stores: artist_id=A, album_artist_id=B on that track.
//   - getArtist(A) via standard Subsonic returns no albums (A has none).
//   - This endpoint returns that track, making A's catalogue fully browsable.
//
// # Query parameters
//
//	id  (required) — the Navidrome artist ID (a string, typically an MD5 hash)
//
// # Response (wrapped in Subsonic envelope)
//
//	{
//	  "subsonic-response": {
//	    "status": "ok",
//	    "version": "1.16.1",
//	    "artistTracks": {
//	      "song": [
//	        {
//	          "id": "tr-1",
//	          "isDir": false,
//	          "title": "Track Title",
//	          "album": "Album Name",
//	          "artist": "Artist Name",
//	          "track": 3,
//	          "year": 2021,
//	          "duration": 245,
//	          "bitRate": 320,
//	          "coverArt": "al-abc123",
//	          "albumId": "al-abc123",
//	          "artistId": "ar-xyz",
//	          "type": "music",
//	          "mediaType": "song"
//	        },
//	        ...
//	      ]
//	    }
//	  }
//	}
//
// The "song" key and all song field names match the OpenSubsonic Child schema
// and the Naviamp client's SubsonicChild model (in subsonic_models.dart) so
// that the client's existing _childToDto() mapper processes these responses
// without modification.
//
// Returns an empty song array (not null) when the artist has no performing
// credits, so the client receives [] and renders an empty state rather than
// crashing on a null.
func (h *Handler) ArtistTracks(w http.ResponseWriter, r *http.Request) {
	artistID := r.URL.Query().Get("id")
	if artistID == "" {
		writeSubsonicError(w, http.StatusBadRequest, 10,
			"Required parameter is missing: id.")
		return
	}

	tracks, err := db.QueryArtistTracks(h.db, artistID, h.dbType)
	if err != nil {
		slog.Error("query artist tracks", "artistId", artistID, "error", err)
		writeSubsonicError(w, http.StatusInternalServerError, 0, "Internal error.")
		return
	}

	resp := model.ArtistTracksResponse{Song: tracks}
	writeJSON(w, http.StatusOK, model.OKEnvelope("artistTracks", resp))
}
