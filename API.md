# naviamp-sidecar API Reference

This document is the complete integration reference for the naviamp-sidecar service.
It is written for client developers (human or LLM) who want to implement support for
the sidecar in a Navidrome music player client.

---

## Table of contents

1. [Overview](#overview)
2. [Client detection — the probe flow](#client-detection--the-probe-flow)
3. [Authentication](#authentication)
4. [Response envelope](#response-envelope)
5. [Endpoints](#endpoints)
   - [GET /naviamp/capabilities](#get-navimampcapabilities)
   - [GET /naviamp/changes](#get-naviampchanges)
   - [GET /naviamp/artistTracks](#get-naviampartisttracks)
6. [Feature flags](#feature-flags)
7. [Error handling](#error-handling)
8. [Versioning policy](#versioning-policy)
9. [Example integrations](#example-integrations)

---

## Overview

The naviamp-sidecar is an optional read-only HTTP service that runs on the same
host as a [Navidrome](https://www.navidrome.org/) music server. It exposes three
endpoints under the `/naviamp/` prefix that fill gaps in the standard
[OpenSubsonic API](https://opensubsonic.netlify.app/):

| Gap | Sidecar endpoint |
|-----|-----------------|
| No incremental library sync | `GET /naviamp/changes` |
| Performing-artist credits not exposed | `GET /naviamp/artistTracks` |

The sidecar is completely passive:
- It never writes to the Navidrome database.
- It never serves audio or exposes file paths.
- It never stores or validates credentials — all auth is delegated to Navidrome.

**Default port:** `:8090` (distinct from Navidrome's `:4533`).

---

## Client detection — the probe flow

When a client starts up (or completes login), it should probe for the sidecar:

```
GET <serverUrl>/naviamp/capabilities?u=<user>&t=<token>&s=<salt>&v=1.16.1&c=<client>&f=json
```

**Decision logic:**

```
HTTP 200 AND valid JSON body with "version" (string) AND "features" ([]string)
  → sidecar present; cache the feature list; enable sidecar features
else (any other status, network error, malformed body)
  → sidecar absent; use standard OpenSubsonic only; no error shown to user
```

**Caching:** The result should be cached in memory and persisted locally. Do NOT
re-probe on every request — check the cached state synchronously instead. Re-probe
only on explicit user action (e.g., server settings change, logout/login).

---

## Authentication

Every request to the sidecar requires the same Subsonic auth parameters as a
standard OpenSubsonic request:

| Param | Description |
|-------|-------------|
| `u` | Navidrome username |
| `t` | MD5 token: `md5(password + salt)`, hex-encoded lowercase |
| `s` | Random salt string (minimum 6 characters) |
| `v` | Protocol version — always `1.16.1` |
| `c` | Client identifier — your app name, e.g. `myapp` |
| `f` | Response format — always `json` for sidecar requests |

**Token calculation example:**

```
password = "sesame"
salt     = "c19b2d"   (random, min 6 chars, generated fresh per request)
token    = md5("sesamec19b2d") = "26719a1196d2a940705a59634eb18eab"
```

The sidecar verifies credentials by forwarding them to Navidrome's
`/rest/ping.view` endpoint. If Navidrome rejects them, the sidecar returns
HTTP 401 with a Subsonic error envelope (see [Error handling](#error-handling)).

---

## Response envelope

All sidecar responses use the Subsonic JSON envelope format. This is the same
envelope Navidrome itself returns, so existing envelope-unwrapping code works
without modification.

**Success:**

```json
{
  "subsonic-response": {
    "status": "ok",
    "version": "1.16.1",
    "<data-key>": { ... }
  }
}
```

`<data-key>` varies by endpoint: `"capabilities"`, `"changes"`, or `"artistTracks"`.

**Failure:**

```json
{
  "subsonic-response": {
    "status": "failed",
    "version": "1.16.1",
    "error": {
      "code": 40,
      "message": "Wrong username or password."
    }
  }
}
```

---

## Endpoints

### GET /naviamp/capabilities

**Purpose:** Probe — detect sidecar presence and retrieve supported features.

**URL:** `GET <sidecarUrl>/naviamp/capabilities`

**Parameters:** [standard auth params](#authentication) only, no endpoint-specific params.

**Success response:**

```json
{
  "subsonic-response": {
    "status": "ok",
    "version": "1.16.1",
    "capabilities": {
      "version": "1.0.0",
      "features": ["delta-sync", "performing-artists"]
    }
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `capabilities.version` | string | Sidecar semantic version (not Subsonic protocol version) |
| `capabilities.features` | []string | Feature tokens — see [Feature flags](#feature-flags) |

---

### GET /naviamp/changes

**Purpose:** Delta sync — retrieve IDs of items changed since a given timestamp.

**URL:** `GET <sidecarUrl>/naviamp/changes`

**Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `since` | integer (Unix ms) | No | Return items changed after this timestamp. If 0 or omitted, returns all items (first-run full sync). |
| `u`, `t`, `s`, `v`, `c`, `f` | string | Yes | Standard Subsonic auth params |

**Success response:**

```json
{
  "subsonic-response": {
    "status": "ok",
    "version": "1.16.1",
    "changes": {
      "artists": [
        { "id": "ar-abc123", "updatedAt": 1716900000000 }
      ],
      "albums": [
        { "id": "al-def456", "updatedAt": 1716900000000 }
      ],
      "songs": [
        { "id": "tr-ghi789", "updatedAt": 1716900000000 }
      ],
      "deletedIds": ["tr-old99", "al-gone88"]
    }
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `changes.artists` | []ChangeEntry | Artists modified after `since` |
| `changes.albums` | []ChangeEntry | Albums modified after `since` |
| `changes.songs` | []ChangeEntry | Tracks modified after `since` |
| `changes.deletedIds` | []string | IDs of items soft-deleted after `since` — remove from local cache |

**ChangeEntry:**

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Navidrome entity ID (string, typically an MD5 hash) |
| `updatedAt` | integer | Unix milliseconds — use as the next `since` value |

**Recommended client sync algorithm:**

```
lastSync = load from local storage (0 on first run)
response = GET /naviamp/changes?since=lastSync
for each id in response.changes.artists  → GET /rest/getArtist.view?id=<id>
for each id in response.changes.albums   → GET /rest/getAlbum.view?id=<id>
for each id in response.changes.songs    → GET /rest/getSong.view?id=<id>
for each id in response.changes.deletedIds → remove from local DB
lastSync = max(all updatedAt values seen) or current time
save lastSync to local storage
```

---

### GET /naviamp/artistTracks

**Purpose:** Return all tracks where an artist appears as a **performing credit**,
not just tracks on albums where they are the album artist.

**URL:** `GET <sidecarUrl>/naviamp/artistTracks`

**Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Navidrome artist ID |
| `u`, `t`, `s`, `v`, `c`, `f` | string | Yes | Standard Subsonic auth params |

**Why this endpoint exists:**

The standard OpenSubsonic `getArtist` endpoint returns albums where the artist is
the *album* artist. It does not return tracks where the artist appears as a
featured/guest performer on another artist's album. Navidrome stores two
separate credits per track:

- `artist_id` = performing artist (the track-level credit)
- `album_artist_id` = the album's primary artist

This endpoint queries `artist_id`, surfacing the full performing catalogue.

**Success response:**

```json
{
  "subsonic-response": {
    "status": "ok",
    "version": "1.16.1",
    "artistTracks": {
      "song": [
        {
          "id": "tr-abc123",
          "isDir": false,
          "title": "Track Title",
          "album": "Album Name",
          "artist": "Performing Artist",
          "track": 3,
          "year": 2021,
          "genre": "Jazz",
          "duration": 245,
          "bitRate": 320,
          "size": 12345678,
          "contentType": "audio/flac",
          "suffix": "flac",
          "coverArt": "al-coverId",
          "albumId": "al-abc123",
          "artistId": "ar-xyz",
          "playCount": 12,
          "type": "music",
          "mediaType": "song",
          "musicBrainzId": "optional-mbz-uuid"
        }
      ]
    }
  }
}
```

The `song` key and all field names match the **OpenSubsonic Child schema**
(`https://opensubsonic.netlify.app/docs/responses/child/`). Any existing code
that parses a `getAlbum` response will work here without modification.

**Empty result:** When the artist has no performing credits, `song` is `[]` (not `null`).

---

## Feature flags

The `features` array in the capabilities response contains string tokens that
identify which sidecar behaviours are available. Clients must check for exact
string equality — do not use prefix matching or version comparisons.

| Token | Meaning | Endpoint to use |
|-------|---------|----------------|
| `"delta-sync"` | Incremental library sync is available | `GET /naviamp/changes` |
| `"performing-artists"` | Performing-artist track browse is available | `GET /naviamp/artistTracks` |

**Integration pattern:**

```
if features.contains("delta-sync") {
    use /naviamp/changes for library sync
} else {
    fall back to full Subsonic library scan
}

if features.contains("performing-artists") {
    show "All tracks by this artist" section using /naviamp/artistTracks
} else {
    hide the section (or show album-artist tracks only via standard getArtist)
}
```

---

## Error handling

All errors return a Subsonic error envelope. The HTTP status code and the
Subsonic error code carry independent information:

| HTTP status | Subsonic code | Meaning |
|-------------|--------------|---------|
| 400 | 10 | Required parameter missing or malformed |
| 401 | 40 | Wrong username or password (forwarded from Navidrome) |
| 503 | 0 | Sidecar could not reach Navidrome for auth verification |
| 500 | 0 | Internal database error |

**Example auth failure:**

```json
{
  "subsonic-response": {
    "status": "failed",
    "version": "1.16.1",
    "error": {
      "code": 40,
      "message": "Wrong username or password."
    }
  }
}
```

---

## Versioning policy

The sidecar uses [semantic versioning](https://semver.org/).

- **Patch** (1.0.x): Bug fixes with no response schema changes.
- **Minor** (1.x.0): New endpoints or fields added. Existing fields unchanged.
  Clients can ignore unknown fields safely.
- **Major** (x.0.0): Breaking change to an existing endpoint's response schema.
  Requires coordinated client update.

Clients should gate on **feature strings**, not on the `version` field. Adding a
new feature in a minor release does not require a client update for existing features.

---

## Example integrations

### curl

```bash
BASE="http://localhost:8090"
U="alice"
S="randomsalt1"
T=$(printf '%s%s' 'mypassword' "$S" | md5sum | cut -d' ' -f1)
AUTH="u=$U&t=$T&s=$S&v=1.16.1&c=myapp&f=json"

# Probe
curl "$BASE/naviamp/capabilities?$AUTH"

# Delta sync (first run)
curl "$BASE/naviamp/changes?since=0&$AUTH"

# Performing-artist tracks
curl "$BASE/naviamp/artistTracks?id=ar-abc123&$AUTH"
```

### JavaScript (fetch)

```js
import { createHash } from 'crypto';

function subsonicAuth(username, password) {
  const salt = Math.random().toString(36).slice(2, 10);
  const token = createHash('md5').update(password + salt).digest('hex');
  return `u=${username}&t=${token}&s=${salt}&v=1.16.1&c=myapp&f=json`;
}

async function probeNaviampSidecar(sidecarUrl, username, password) {
  const auth = subsonicAuth(username, password);
  try {
    const res = await fetch(`${sidecarUrl}/naviamp/capabilities?${auth}`);
    if (!res.ok) return null;
    const body = await res.json();
    const inner = body['subsonic-response'];
    if (inner?.status !== 'ok') return null;
    return inner.capabilities; // { version, features }
  } catch {
    return null; // sidecar not present
  }
}

async function getChanges(sidecarUrl, auth, sinceMs) {
  const res = await fetch(`${sidecarUrl}/naviamp/changes?since=${sinceMs}&${auth}`);
  const body = await res.json();
  return body['subsonic-response'].changes;
}

async function getArtistTracks(sidecarUrl, auth, artistId) {
  const res = await fetch(`${sidecarUrl}/naviamp/artistTracks?id=${artistId}&${auth}`);
  const body = await res.json();
  return body['subsonic-response'].artistTracks.song;
}
```

### Dart / Flutter

```dart
import 'dart:convert';
import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;

String _md5(String input) =>
    md5.convert(utf8.encode(input)).toString();

Map<String, String> _authParams(String username, String password) {
  final salt = DateTime.now().millisecondsSinceEpoch.toRadixString(36);
  final token = _md5(password + salt);
  return {
    'u': username, 't': token, 's': salt,
    'v': '1.16.1', 'c': 'myapp', 'f': 'json',
  };
}

Future<List<String>?> probeFeatures(
    String sidecarUrl, String username, String password) async {
  final params = _authParams(username, password);
  final uri = Uri.parse('$sidecarUrl/naviamp/capabilities')
      .replace(queryParameters: params);
  try {
    final res = await http.get(uri).timeout(const Duration(seconds: 5));
    if (res.statusCode != 200) return null;
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final inner = body['subsonic-response'] as Map<String, dynamic>?;
    if (inner?['status'] != 'ok') return null;
    final caps = inner!['capabilities'] as Map<String, dynamic>;
    return List<String>.from(caps['features'] as List);
  } catch (_) {
    return null; // sidecar not present — silent fallback
  }
}

Future<Map<String, dynamic>> getChanges(
    String sidecarUrl, String username, String password, int sinceMs) async {
  final params = {..._authParams(username, password), 'since': '$sinceMs'};
  final uri = Uri.parse('$sidecarUrl/naviamp/changes')
      .replace(queryParameters: params);
  final res = await http.get(uri);
  final body = jsonDecode(res.body) as Map<String, dynamic>;
  return (body['subsonic-response']['changes']) as Map<String, dynamic>;
}

Future<List<dynamic>> getArtistTracks(
    String sidecarUrl, String username, String password, String artistId) async {
  final params = {..._authParams(username, password), 'id': artistId};
  final uri = Uri.parse('$sidecarUrl/naviamp/artistTracks')
      .replace(queryParameters: params);
  final res = await http.get(uri);
  final body = jsonDecode(res.body) as Map<String, dynamic>;
  return body['subsonic-response']['artistTracks']['song'] as List;
}
```
