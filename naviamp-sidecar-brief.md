# Naviamp Companion Sidecar — Project Brief

## What this project is

This is a standalone Go service (sidecar) that runs alongside a [Navidrome](https://www.navidrome.org/) music server and exposes additional read-only HTTP endpoints that the Naviamp client (a Flutter/Dart Navidrome music player app) can query. The standard OpenSubsonic protocol that Navidrome implements has several gaps relative to Jellyfin; this sidecar bridges those gaps without modifying Navidrome itself.

The Naviamp client already has the full detection and routing infrastructure in place. When this sidecar is running on the same host as Navidrome, the client detects it automatically on startup and uses its endpoints. When it is absent, the client falls back gracefully to standard Subsonic behavior.

## Your job

Architect and implement this sidecar. The client side is already done — your job is to build the server. At the end, produce a single static Go binary that a Navidrome user can drop onto their server alongside Navidrome, configure with a short config file or env vars, and have it "just work."

---

## Client detection protocol

When the Naviamp client starts up (or logs in), it makes this request:

```
GET <serverUrl>/naviamp/capabilities?u=<username>&t=<md5token>&s=<salt>&v=1.16.1&c=naviamp&f=json
```

Auth parameters follow the [OpenSubsonic / Subsonic API auth spec](https://opensubsonic.netlify.app/docs/api/):
- `u` = username
- `s` = random salt string
- `t` = MD5 hash of `password + salt`
- `v`, `c`, `f` = version/client/format, always these values from the client

Expected response (HTTP 200, `application/json`):
```json
{
  "version": "1.0.0",
  "features": ["delta-sync", "performing-artists"]
}
```

Any non-200 response, network error, or malformed body → client silently concludes the plugin is not present and uses standard Subsonic only. There is no error shown to the user.

The client caches this result in a Riverpod state provider and persists it locally. Subsequent API calls check the cached state synchronously (no network I/O per request).

---

## Authentication

Every request to the sidecar includes the same Subsonic auth params (`u`, `t`, `s`). The sidecar must verify these before responding:

1. Forward a `GET <navidrome_base_url>/rest/ping.view?u=...&t=...&s=...&v=1.16.1&c=naviamp&f=json` to the configured Navidrome server.
2. If Navidrome returns `status: "ok"` → request is authenticated; proceed.
3. If Navidrome returns an error (code 40 = wrong creds, code 10 = missing params, etc.) → return HTTP 401.

This ensures the sidecar never manages credentials itself and cannot be used to bypass Navidrome's access controls.

---

## Security requirements

- **Read-only.** No writes to the Navidrome database under any circumstances.
- **Parameterised queries only.** No string interpolation into SQL. Every user-supplied value (IDs, timestamps) is bound as a query parameter.
- **No filesystem access.** The sidecar serves data from the database only. It does not list directories, serve file content, or expose file paths. All streaming is handled by Navidrome directly.
- **Scope = authenticated user's library.** Query results must respect the user's library access. The simplest correct approach: Navidrome's database schema uses `music_folder` relationships; constrain queries to folders accessible to the authenticated user. If uncertain, join against Navidrome's existing access tables.
- **No admin-only data.** Do not expose user password hashes, admin config, other users' data, or anything not already accessible to the user via the standard Subsonic API.

---

## API endpoints (v1.0.0)

All endpoints require Subsonic auth params. All responses are JSON and wrap content in a Subsonic-compatible envelope so the client can use its existing `SubsonicEnvelope.unwrap()` logic:

```json
{
  "subsonic-response": {
    "status": "ok",
    "version": "1.16.1",
    "<data-key>": { ... }
  }
}
```

On auth failure, return a Subsonic error envelope:
```json
{
  "subsonic-response": {
    "status": "failed",
    "version": "1.16.1",
    "error": { "code": 40, "message": "Wrong username or password." }
  }
}
```

### `GET /naviamp/capabilities`

Returns the version string and feature list. This is the probe endpoint.

Response:
```json
{
  "version": "1.0.0",
  "features": ["delta-sync", "performing-artists"]
}
```

### `GET /naviamp/changes?since=<unix-milliseconds>`

Returns all items modified since the given timestamp. Used for delta sync: the client will only fetch full metadata for items in this list, avoiding a full library rescan.

`since` is a Unix timestamp in milliseconds (same unit as Subsonic's `time` param).

Response:
```json
{
  "changes": {
    "artists":    [{ "id": "ar-1", "updatedAt": 1716900000000 }],
    "albums":     [{ "id": "al-1", "updatedAt": 1716900000000 }],
    "songs":      [{ "id": "tr-1", "updatedAt": 1716900000000 }],
    "deletedIds": ["tr-99", "al-88"]
  }
}
```

- `artists`, `albums`, `songs`: items where `updated_at > since` in Navidrome's DB. Only IDs and timestamps — not full metadata (the client fetches full metadata via standard Subsonic for items in this list).
- `deletedIds`: opaque ID strings for items that were deleted after `since`. The client purges these from its local Isar database.

If `since` is omitted or 0, return all items (equivalent to a full sync — the client uses this on first install).

### `GET /naviamp/artistTracks?id=<artistId>`

Returns all tracks where the given artist appears as a **performing credit**, not just as album artist. This addresses the "performing artist browse" gap: Navidrome stores track-level artist credits internally but OpenSubsonic's `getArtist` only surfaces album artists.

Response — a list of `SubsonicChild`-compatible track objects (same schema as songs in `getAlbum` response):
```json
{
  "artistTracks": {
    "song": [ { "id": "...", "title": "...", "album": "...", "artist": "...", ... } ]
  }
}
```

The song objects must match the Subsonic `Child` schema so the client's existing `_childToDto` mapper can process them without modification.

---

## Configuration

The sidecar needs two pieces of information:

1. **Navidrome base URL** — for auth forwarding. Example: `http://localhost:4533`
2. **Navidrome database connection** — to query library data directly.

Navidrome supports SQLite (default), PostgreSQL, and MySQL. The sidecar should support all three (SQLite is the most common for home users).

Suggested config (env vars or a YAML/TOML file):
```
NAVIAMP_NAVIDROME_URL=http://localhost:4533
NAVIAMP_DB_TYPE=sqlite              # sqlite | postgres | mysql
NAVIAMP_DB_PATH=/var/lib/navidrome/navidrome.db   # for sqlite
NAVIAMP_DB_DSN=postgres://...       # for postgres/mysql
NAVIAMP_LISTEN=:8090                # address to bind on
```

The listen port is intentionally separate from Navidrome's port (4533). Users can put both behind a reverse proxy (nginx, Caddy) and expose `/naviamp/` routes from the sidecar at the same public hostname.

---

## Navidrome database schema notes

Navidrome uses [GORM](https://gorm.io/) with auto-migration. The relevant tables (subject to change across Navidrome versions — pin to a known-good version and document it):

- `media_file` — tracks. Fields of interest: `id`, `title`, `album_id`, `artist_id`, `album_artist_id`, `updated_at`, `deleted_at`.
- `album` — albums. Fields: `id`, `name`, `artist_id`, `updated_at`.
- `artist` — artists. Fields: `id`, `name`, `updated_at`.
- `annotation` — user-specific data (stars, play counts). Not needed for v1.0.

For performing artist browse: Navidrome stores track-level `artist_id` and `album_artist_id` separately. A track by artist A on an album by artist B will have `artist_id = A` and `album_artist_id = B`. Querying `WHERE artist_id = <id>` returns all tracks where A appears as performing artist, including those on other artists' albums.

---

## Deployment example (Docker Compose)

A typical Navidrome deployment uses Docker Compose. The sidecar should be trivial to add:

```yaml
services:
  navidrome:
    image: deluan/navidrome:latest
    ports: ["4533:4533"]
    volumes:
      - ./data:/data
      - ./music:/music:ro

  naviamp-plugin:
    image: naviamp/plugin:latest        # to be created
    environment:
      NAVIAMP_NAVIDROME_URL: http://navidrome:4533
      NAVIAMP_DB_TYPE: sqlite
      NAVIAMP_DB_PATH: /data/navidrome.db
      NAVIAMP_LISTEN: :8090
    volumes:
      - ./data:/data:ro                 # read-only access to the DB
    ports: ["8090:8090"]
    depends_on: [navidrome]
```

---

## Relationship to the Naviamp client

The client-side code lives at: https://github.com/Happyarch/naviamp (branch: `redesign`)

Relevant client files:
- `lib/services/naviamp_plugin_state.dart` — `NaviampPluginState` sealed class + Riverpod provider
- `lib/services/naviamp_plugin_helper.dart` — `NaviampPluginHelper.probe()` and `runNaviampPluginProbe()`
- `lib/screens/naviamp_server_settings_screen.dart` — settings UI

The client is already written to call your endpoints. The feature string constants it checks are:
- `"delta-sync"` — enables delta sync in the downloads service
- `"performing-artists"` — enables performing artist track browse

Return these in the `features` array from `/naviamp/capabilities` once those endpoints are implemented.

---

## Versioning and compatibility

Use semantic versioning. The client checks for specific features by string, not by version number, so adding new features in a minor version is safe. Breaking changes to existing endpoint response schemas require a major version bump and client coordination.

The sidecar should log the Navidrome DB schema version it was built against, and warn (not crash) if the running Navidrome version has a different schema hash on startup.
