package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/happyarch/naviamp-sidecar/internal/config"
	"github.com/happyarch/naviamp-sidecar/internal/handlers"

	// Open an in-memory SQLite DB for handler tests.
	"database/sql"
	_ "modernc.org/sqlite"
)

// newTestDB opens an in-memory SQLite database seeded with a minimal
// Navidrome schema so the handler tests can exercise real SQL queries.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE artist (
			id         TEXT PRIMARY KEY,
			name       TEXT,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE album (
			id         TEXT PRIMARY KEY,
			name       TEXT,
			artist_id  TEXT,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE media_file (
			id              TEXT PRIMARY KEY,
			title           TEXT,
			album           TEXT,
			artist          TEXT,
			track_number    INTEGER,
			year            INTEGER,
			genre           TEXT,
			content_type    TEXT,
			suffix          TEXT,
			size            INTEGER,
			duration        REAL,
			bit_rate        INTEGER,
			bit_depth       INTEGER,
			sample_rate     INTEGER,
			channels        INTEGER,
			cover_art_id    TEXT,
			album_id        TEXT,
			artist_id       TEXT,
			album_artist_id TEXT,
			play_count      INTEGER,
			disc_number     INTEGER,
			mbz_recording_id TEXT,
			updated_at      DATETIME,
			deleted_at      DATETIME
		);
	`)
	if err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	return db
}

// navidromeAuthOK returns a test Navidrome server that always accepts credentials.
func navidromeAuthOK(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"subsonic-response": map[string]any{"status": "ok"},
		})
	}))
}

func TestCapabilities(t *testing.T) {
	nd := navidromeAuthOK(t)
	defer nd.Close()

	db := newTestDB(t)
	cfg := config.Config{NavidromeURL: nd.URL, DBType: "sqlite", Listen: ":0"}
	h := handlers.New(db, cfg)

	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, nd.URL, h)

	req := httptest.NewRequest("GET",
		"/naviamp/capabilities?u=alice&t=tok&s=saltsalt&v=1.16.1&c=test&f=json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	inner, ok := body["subsonic-response"].(map[string]any)
	if !ok {
		t.Fatal("missing subsonic-response key")
	}
	if inner["status"] != "ok" {
		t.Errorf("expected status ok, got %v", inner["status"])
	}

	caps, ok := inner["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("missing capabilities key")
	}
	if caps["version"] == nil {
		t.Error("missing capabilities.version")
	}
	features, ok := caps["features"].([]any)
	if !ok || len(features) == 0 {
		t.Error("expected non-empty features array")
	}
}

func TestChanges_EmptyDB(t *testing.T) {
	nd := navidromeAuthOK(t)
	defer nd.Close()

	db := newTestDB(t)
	cfg := config.Config{NavidromeURL: nd.URL, DBType: "sqlite", Listen: ":0"}
	h := handlers.New(db, cfg)

	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, nd.URL, h)

	req := httptest.NewRequest("GET",
		"/naviamp/changes?since=0&u=alice&t=tok&s=saltsalt&v=1.16.1&c=test&f=json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	inner := body["subsonic-response"].(map[string]any)
	if inner["status"] != "ok" {
		t.Errorf("expected ok, got %v", inner["status"])
	}
	changes := inner["changes"].(map[string]any)
	if changes["artists"] == nil {
		t.Error("expected artists array, got nil")
	}
}

func TestArtistTracks_MissingID(t *testing.T) {
	nd := navidromeAuthOK(t)
	defer nd.Close()

	db := newTestDB(t)
	cfg := config.Config{NavidromeURL: nd.URL, DBType: "sqlite", Listen: ":0"}
	h := handlers.New(db, cfg)

	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, nd.URL, h)

	// Omit the required "id" parameter.
	req := httptest.NewRequest("GET",
		"/naviamp/artistTracks?u=alice&t=tok&s=saltsalt&v=1.16.1&c=test&f=json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	inner := body["subsonic-response"].(map[string]any)
	if inner["status"] != "failed" {
		t.Errorf("expected failed, got %v", inner["status"])
	}
	errObj := inner["error"].(map[string]any)
	if errObj["code"].(float64) != 10 {
		t.Errorf("expected code 10, got %v", errObj["code"])
	}
}

func TestArtistTracks_WithData(t *testing.T) {
	nd := navidromeAuthOK(t)
	defer nd.Close()

	db := newTestDB(t)

	// Insert a track where artist_id = "ar-1" (performing credit).
	_, err := db.Exec(`
		INSERT INTO media_file
			(id, title, album, artist, artist_id, album_artist_id, updated_at)
		VALUES
			('tr-1', 'Guest Track', 'Host Album', 'Guest Artist', 'ar-1', 'ar-2',
			 datetime('now'))
	`)
	if err != nil {
		t.Fatalf("insert track: %v", err)
	}

	cfg := config.Config{NavidromeURL: nd.URL, DBType: "sqlite", Listen: ":0"}
	h := handlers.New(db, cfg)

	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, nd.URL, h)

	req := httptest.NewRequest("GET",
		"/naviamp/artistTracks?id=ar-1&u=alice&t=tok&s=saltsalt&v=1.16.1&c=test&f=json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	inner := body["subsonic-response"].(map[string]any)
	if inner["status"] != "ok" {
		t.Errorf("expected ok, got %v", inner["status"])
	}
	at := inner["artistTracks"].(map[string]any)
	songs := at["song"].([]any)
	if len(songs) != 1 {
		t.Fatalf("expected 1 song, got %d", len(songs))
	}
	song := songs[0].(map[string]any)
	if song["id"] != "tr-1" {
		t.Errorf("expected id tr-1, got %v", song["id"])
	}
}

func TestAuthMiddleware_MissingParams(t *testing.T) {
	nd := navidromeAuthOK(t)
	defer nd.Close()

	db := newTestDB(t)
	cfg := config.Config{NavidromeURL: nd.URL, DBType: "sqlite", Listen: ":0"}
	h := handlers.New(db, cfg)

	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux, nd.URL, h)

	// No auth params at all.
	req := httptest.NewRequest("GET", "/naviamp/capabilities", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
