// This file contains all SQL queries the sidecar runs against the Navidrome
// database. Every query in this file is read-only and uses parameterised
// placeholders — never string interpolation — to prevent SQL injection.
//
// # Navidrome database schema notes
//
// Navidrome uses GORM with auto-migration. The schema evolves across releases;
// this file was written against Navidrome ≥ 0.52.0 (2024-01). Pin your
// Navidrome version if you notice schema drift.
//
// Relevant tables:
//
//	media_file  — one row per track.
//	              artist_id        = performing artist (the track credit).
//	              album_artist_id  = album-level artist credit.
//	              updated_at       = UTC timestamp set by GORM on every write.
//	              deleted_at       = soft-delete timestamp (NULL if not deleted).
//
//	album       — one row per album.
//	              artist_id = album artist.
//	              updated_at, deleted_at as above.
//
//	artist      — one row per artist.
//	              updated_at, deleted_at as above.
//
// # Timestamp handling
//
// Navidrome stores updated_at as a UTC datetime. The sidecar converts these to
// Unix milliseconds (int64) so the Naviamp client can pass the value straight
// back as the next "since" parameter without any unit conversion.
//
// # Placeholder syntax
//
// SQLite and MySQL use "?" placeholders; PostgreSQL uses "$1", "$2", etc.
// To support all three databases without duplicating queries, this file uses
// the "?" form and relies on placeholder rewriting for PostgreSQL via the
// rewritePlaceholders() helper below. MySQL and SQLite both accept "?" natively.
package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/happyarch/naviamp-sidecar/internal/model"
)

// QueryChangedArtists returns ID + updated_at for every artist whose
// updated_at column is strictly greater than since. Soft-deleted artists
// (deleted_at IS NOT NULL) are excluded because they appear in DeletedIDs
// instead, returned by QueryDeletedIDs.
//
// Returns an empty slice (not nil) when no artists have changed, so the
// client receives [] rather than null in the JSON response.
func QueryChangedArtists(db *sql.DB, since time.Time, dbType string) ([]model.ChangeEntry, error) {
	q := rewrite(`
		SELECT id, updated_at
		FROM artist
		WHERE updated_at > ?
		  AND deleted_at IS NULL
		ORDER BY updated_at ASC
	`, dbType)

	rows, err := db.Query(q, since)
	if err != nil {
		return nil, fmt.Errorf("query changed artists: %w", err)
	}
	defer rows.Close()

	return scanChangeEntries(rows)
}

// QueryChangedAlbums returns ID + updated_at for every album modified after
// since. Same soft-delete exclusion as QueryChangedArtists.
func QueryChangedAlbums(db *sql.DB, since time.Time, dbType string) ([]model.ChangeEntry, error) {
	q := rewrite(`
		SELECT id, updated_at
		FROM album
		WHERE updated_at > ?
		  AND deleted_at IS NULL
		ORDER BY updated_at ASC
	`, dbType)

	rows, err := db.Query(q, since)
	if err != nil {
		return nil, fmt.Errorf("query changed albums: %w", err)
	}
	defer rows.Close()

	return scanChangeEntries(rows)
}

// QueryChangedSongs returns ID + updated_at for every track in media_file
// modified after since. Soft-deleted tracks are excluded.
func QueryChangedSongs(db *sql.DB, since time.Time, dbType string) ([]model.ChangeEntry, error) {
	q := rewrite(`
		SELECT id, updated_at
		FROM media_file
		WHERE updated_at > ?
		  AND deleted_at IS NULL
		ORDER BY updated_at ASC
	`, dbType)

	rows, err := db.Query(q, since)
	if err != nil {
		return nil, fmt.Errorf("query changed songs: %w", err)
	}
	defer rows.Close()

	return scanChangeEntries(rows)
}

// QueryDeletedIDs returns all Navidrome IDs (across artists, albums, and
// tracks) that were soft-deleted after since. The client purges these IDs
// from its local Isar cache without attempting to fetch them.
//
// IDs from all three entity types are pooled into a single []string because
// the client treats them as an opaque set of "things to remove" — it holds
// entity-type metadata in its local DB and can resolve the type from the ID.
func QueryDeletedIDs(db *sql.DB, since time.Time, dbType string) ([]string, error) {
	q := rewrite(`
		SELECT id FROM artist      WHERE deleted_at > ? AND deleted_at IS NOT NULL
		UNION ALL
		SELECT id FROM album       WHERE deleted_at > ? AND deleted_at IS NOT NULL
		UNION ALL
		SELECT id FROM media_file  WHERE deleted_at > ? AND deleted_at IS NOT NULL
	`, dbType)

	rows, err := db.Query(q, since, since, since)
	if err != nil {
		return nil, fmt.Errorf("query deleted ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan deleted id: %w", err)
		}
		ids = append(ids, id)
	}
	if ids == nil {
		ids = []string{} // return [] not null in JSON
	}
	return ids, rows.Err()
}

// QueryArtistTracks returns all tracks in media_file where artist_id matches
// the given artistID, regardless of which album they appear on.
//
// # Why artist_id, not album_artist_id?
//
// Navidrome records two artist credits per track:
//
//	media_file.artist_id       = the performing artist (the track credit)
//	media_file.album_artist_id = the artist the album is credited to
//
// Example: a guest vocalist (A) appears on a track from artist B's album.
//   - artist_id        = A  (A performed on this track)
//   - album_artist_id  = B  (B's album)
//
// OpenSubsonic's getArtist(A) returns albums where A is the album_artist_id
// only, so that guest track would be invisible. This endpoint queries
// artist_id instead, surfacing all performing credits for A.
func QueryArtistTracks(db *sql.DB, artistID string, dbType string) ([]model.Child, error) {
	q := rewrite(`
		SELECT
			mf.id,
			mf.title,
			mf.album,
			mf.artist,
			mf.track_number,
			mf.year,
			mf.genre,
			mf.content_type,
			mf.suffix,
			mf.size,
			mf.duration,
			mf.bit_rate,
			mf.bit_depth,
			mf.sample_rate,
			mf.channels,
			mf.cover_art_id,
			mf.album_id,
			mf.artist_id,
			mf.play_count,
			mf.mbz_recording_id
		FROM media_file mf
		WHERE mf.artist_id = ?
		  AND mf.deleted_at IS NULL
		ORDER BY mf.album, mf.disc_number, mf.track_number
	`, dbType)

	rows, err := db.Query(q, artistID)
	if err != nil {
		return nil, fmt.Errorf("query artist tracks: %w", err)
	}
	defer rows.Close()

	var tracks []model.Child
	for rows.Next() {
		var (
			c             model.Child
			trackNum      sql.NullInt64
			year          sql.NullInt64
			genre         sql.NullString
			contentType   sql.NullString
			suffix        sql.NullString
			size          sql.NullInt64
			duration      sql.NullFloat64
			bitRate       sql.NullInt64
			bitDepth      sql.NullInt64
			sampleRate    sql.NullInt64
			channels      sql.NullInt64
			coverArtID    sql.NullString
			albumID       sql.NullString
			artistID      sql.NullString
			playCount     sql.NullInt64
			mbzRecording  sql.NullString
		)
		err := rows.Scan(
			&c.ID,
			&c.Title,
			&c.Album,
			&c.Artist,
			&trackNum,
			&year,
			&genre,
			&contentType,
			&suffix,
			&size,
			&duration,
			&bitRate,
			&bitDepth,
			&sampleRate,
			&channels,
			&coverArtID,
			&albumID,
			&artistID,
			&playCount,
			&mbzRecording,
		)
		if err != nil {
			return nil, fmt.Errorf("scan artist track: %w", err)
		}

		c.IsDir = false
		c.Type = "music"
		c.MediaType = "song"

		if trackNum.Valid {
			c.Track = int(trackNum.Int64)
		}
		if year.Valid {
			c.Year = int(year.Int64)
		}
		if genre.Valid {
			c.Genre = genre.String
		}
		if contentType.Valid {
			c.ContentType = contentType.String
		}
		if suffix.Valid {
			c.Suffix = suffix.String
		}
		if size.Valid {
			c.Size = size.Int64
		}
		if duration.Valid {
			c.Duration = int(duration.Float64)
		}
		if bitRate.Valid {
			c.BitRate = int(bitRate.Int64)
		}
		if bitDepth.Valid {
			c.BitDepth = int(bitDepth.Int64)
		}
		if sampleRate.Valid {
			c.SamplingRate = int(sampleRate.Int64)
		}
		if channels.Valid {
			c.ChannelCount = int(channels.Int64)
		}
		if coverArtID.Valid {
			c.CoverArt = coverArtID.String
		}
		if albumID.Valid {
			c.AlbumID = albumID.String
		}
		if artistID.Valid {
			c.ArtistID = artistID.String
		}
		if playCount.Valid {
			c.PlayCount = playCount.Int64
		}
		if mbzRecording.Valid {
			c.MusicBrainzID = mbzRecording.String
		}

		tracks = append(tracks, c)
	}
	if tracks == nil {
		tracks = []model.Child{} // return [] not null in JSON
	}
	return tracks, rows.Err()
}

// scanChangeEntries scans a result set of (id, updated_at) rows into
// []model.ChangeEntry, converting each updated_at to Unix milliseconds.
func scanChangeEntries(rows *sql.Rows) ([]model.ChangeEntry, error) {
	var entries []model.ChangeEntry
	for rows.Next() {
		var (
			id        string
			updatedAt time.Time
		)
		if err := rows.Scan(&id, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan change entry: %w", err)
		}
		entries = append(entries, model.ChangeEntry{
			ID:        id,
			UpdatedAt: updatedAt.UnixMilli(),
		})
	}
	if entries == nil {
		entries = []model.ChangeEntry{} // return [] not null in JSON
	}
	return entries, rows.Err()
}

// rewrite converts "?" placeholders to "$1", "$2", ... for PostgreSQL.
// SQLite and MySQL accept "?" natively, so no rewriting is needed for them.
// This avoids duplicating every query as both a ? and a $N variant.
func rewrite(query, dbType string) string {
	if dbType != "postgres" {
		return query
	}
	n := 0
	var b strings.Builder
	for _, ch := range query {
		if ch == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(ch)
		}
	}
	return b.String()
}
