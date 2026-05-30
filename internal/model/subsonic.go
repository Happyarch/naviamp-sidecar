// Package model defines the JSON types used in all naviamp-sidecar HTTP responses.
//
// Every response wraps its payload in a Subsonic-compatible envelope so that
// the Naviamp client's existing SubsonicEnvelope.unwrap() logic (in
// lib/services/subsonic_api_helper.dart) can parse sidecar responses with no
// client-side changes.
//
// # Envelope structure
//
//	{
//	  "subsonic-response": {
//	    "status":  "ok" | "failed",
//	    "version": "1.16.1",
//	    "<data-key>": { ... }   // payload varies by endpoint
//	  }
//	}
//
// On auth failure, the same envelope is returned with status "failed" and an
// error object — matching the shape the client expects from a real Navidrome
// error response.
package model

import "encoding/json"

// subsonicVersion is the OpenSubsonic protocol version the sidecar claims.
// Naviamp always sends v=1.16.1 in its requests; the sidecar echoes this
// value back so the client's version checks pass.
const subsonicVersion = "1.16.1"

// SubsonicEnvelope is the outermost wrapper for all sidecar JSON responses.
// The "subsonic-response" key name is required by the Subsonic protocol spec
// and is expected verbatim by SubsonicEnvelope.unwrap() in the client.
type SubsonicEnvelope struct {
	Response SubsonicInner `json:"subsonic-response"`
}

// SubsonicInner holds the status, version, and the endpoint-specific payload.
// Payload is stored as a raw JSON value so each handler can attach its own
// typed struct without going through a second marshal/unmarshal cycle.
type SubsonicInner struct {
	// Status is "ok" on success, "failed" on any error (auth or otherwise).
	Status string `json:"status"`

	// Version is always subsonicVersion ("1.16.1") to satisfy the client's
	// version field check in SubsonicEnvelope.unwrap().
	Version string `json:"version"`

	// Error is non-nil only when Status is "failed". The client renders error
	// code 40 as "wrong credentials" and surfaces other codes as generic errors.
	Error *SubsonicError `json:"error,omitempty"`

	// payload is merged into the JSON output by MarshalJSON so that the data
	// key (e.g. "capabilities", "changes", "artistTracks") appears at the same
	// level as status/version rather than nested under a "payload" key.
	payload map[string]any
}

// MarshalJSON flattens the payload fields into the same object as status and
// version. This is necessary because the Subsonic protocol puts the data key
// at the same level as status/version, not nested inside a sub-object.
func (i SubsonicInner) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"status":  i.Status,
		"version": i.Version,
	}
	if i.Error != nil {
		m["error"] = i.Error
	}
	for k, v := range i.payload {
		m[k] = v
	}
	return json.Marshal(m)
}

// SubsonicError follows the Subsonic error object specification.
// Clients map well-known codes to user-facing messages:
//
//	10  Required parameter missing
//	40  Wrong username or password
//	50  User not authorised for this operation
//	70  Requested data not found
type SubsonicError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// OKEnvelope builds a successful response envelope with the given data key and
// value. The key must match what the client expects for that endpoint (e.g.
// "capabilities", "changes", "artistTracks").
func OKEnvelope(dataKey string, value any) SubsonicEnvelope {
	return SubsonicEnvelope{
		Response: SubsonicInner{
			Status:  "ok",
			Version: subsonicVersion,
			payload: map[string]any{dataKey: value},
		},
	}
}

// ErrorEnvelope builds a failed response envelope with the given Subsonic error
// code and message. Used for auth failures (code 40) and missing parameters
// (code 10). The HTTP status code is set separately by the handler.
func ErrorEnvelope(code int, message string) SubsonicEnvelope {
	return SubsonicEnvelope{
		Response: SubsonicInner{
			Status:  "failed",
			Version: subsonicVersion,
			Error:   &SubsonicError{Code: code, Message: message},
		},
	}
}

// Child is a Subsonic/OpenSubsonic track object. The field names and JSON keys
// match the OpenSubsonic Child schema exactly so that the Naviamp client's
// existing _childToDto() mapper (in subsonic_api_helper.dart) can process
// artistTracks responses without any client-side changes.
//
// Only fields that Navidrome's media_file table actually populates are included.
// Optional fields are omitted from the JSON output when zero/empty to keep
// response payloads compact.
//
// Reference: https://opensubsonic.netlify.app/docs/responses/child/
type Child struct {
	// Required by the OpenSubsonic Child schema.
	ID    string `json:"id"`
	IsDir bool   `json:"isDir"` // always false for tracks
	Title string `json:"title"`

	// Core track metadata stored in media_file.
	Album  string `json:"album,omitempty"`
	Artist string `json:"artist,omitempty"`
	Track  int    `json:"track,omitempty"`
	Year   int    `json:"year,omitempty"`
	Genre  string `json:"genre,omitempty"`

	// File information.
	ContentType string `json:"contentType,omitempty"`
	Suffix      string `json:"suffix,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Path        string `json:"path,omitempty"` // not exposed; retained for schema completeness

	// Audio properties.
	Duration     int `json:"duration,omitempty"`    // seconds
	BitRate      int `json:"bitRate,omitempty"`     // kbps
	BitDepth     int `json:"bitDepth,omitempty"`
	SamplingRate int `json:"samplingRate,omitempty"` // Hz
	ChannelCount int `json:"channelCount,omitempty"`

	// Art and cross-references. CoverArt is a Navidrome artwork ID that the
	// client can pass to /rest/getCoverArt.view.
	CoverArt string `json:"coverArt,omitempty"`
	AlbumID  string `json:"albumId,omitempty"`
	ArtistID string `json:"artistId,omitempty"`

	// Engagement — populated from media_file where available.
	PlayCount int64  `json:"playCount,omitempty"`
	Starred   string `json:"starred,omitempty"` // ISO 8601, set if user starred this track

	// Media type hints used by the client's type switch.
	// For all tracks returned by artistTracks: Type="music", MediaType="song".
	Type      string `json:"type,omitempty"`
	MediaType string `json:"mediaType,omitempty"`

	// MusicBrainz identifier for clients that integrate with MB lookups.
	MusicBrainzID string `json:"musicBrainzId,omitempty"`
}

// ChangeEntry is a single item in the changes response.
// The client uses the ID to decide which items to re-fetch via standard
// Subsonic endpoints, and records UpdatedAt to advance its local sync cursor.
type ChangeEntry struct {
	// ID is the Navidrome item identifier (MD5 hash or UUID string, never integer).
	ID string `json:"id"`

	// UpdatedAt is the modification timestamp in Unix milliseconds — the same
	// unit used by Subsonic's "time" param — so the client can use it directly
	// as the next "since" value.
	UpdatedAt int64 `json:"updatedAt"`
}

// CapabilitiesResponse is the payload for GET /naviamp/capabilities.
// The client performs an exact string match against each element of Features
// to decide which sidecar behaviours to enable.
type CapabilitiesResponse struct {
	// Version is the sidecar's own semantic version, distinct from the
	// Subsonic protocol version in the envelope. The client does not gate
	// on this value; it uses feature strings instead.
	Version string `json:"version"`

	// Features is the list of capability tokens the sidecar supports.
	// Each string maps 1:1 to a behaviour the client will enable:
	//   "delta-sync"         → use /naviamp/changes for incremental library sync
	//   "performing-artists" → use /naviamp/artistTracks for performing-credit browse
	Features []string `json:"features"`
}

// ChangesResponse is the payload for GET /naviamp/changes.
// The client processes Artists, Albums, and Songs lists to determine which
// items to re-fetch from standard Subsonic endpoints, and purges DeletedIDs
// from its local Isar database.
type ChangesResponse struct {
	Artists    []ChangeEntry `json:"artists"`
	Albums     []ChangeEntry `json:"albums"`
	Songs      []ChangeEntry `json:"songs"`

	// DeletedIDs contains opaque Navidrome IDs for items soft-deleted after
	// the requested "since" timestamp. The client removes these from its local
	// cache without attempting to fetch them from Navidrome.
	DeletedIDs []string `json:"deletedIds"`
}

// ArtistTracksResponse is the payload for GET /naviamp/artistTracks.
// The "song" key name matches the Subsonic getAlbum response shape so the
// client's _childToDto() mapper works without modification.
type ArtistTracksResponse struct {
	Song []Child `json:"song"`
}
