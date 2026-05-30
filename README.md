# naviamp-sidecar

A lightweight read-only Go service that runs alongside a
[Navidrome](https://www.navidrome.org/) music server and exposes additional
HTTP endpoints for the [Naviamp](https://github.com/Happyarch/naviamp) music
player client.

## What it does

The standard OpenSubsonic protocol Navidrome implements has two gaps the
Naviamp client needs filled:

| Gap | Sidecar endpoint |
|-----|-----------------|
| No incremental library sync (clients must rescan everything on startup) | `GET /naviamp/changes` |
| `getArtist` only surfaces album artists, not performing credits | `GET /naviamp/artistTracks` |

The sidecar queries Navidrome's database directly to fill these gaps. It is
completely passive: **no writes, no credential storage, no file access**.

## Quick start

### Binary

```bash
export NAVIAMP_NAVIDROME_URL=http://localhost:4533
export NAVIAMP_DB_PATH=/var/lib/navidrome/navidrome.db
./naviamp-sidecar
```

### Docker Compose

Copy `docker-compose.example.yml` into your Navidrome deployment and adjust
the volume paths. The sidecar mounts the Navidrome data directory read-only.

## Configuration

All configuration is via environment variables (or an optional `naviamp.yaml`
file). Environment variables take precedence over the file.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `NAVIAMP_NAVIDROME_URL` | Yes | — | Navidrome base URL (no trailing slash) |
| `NAVIAMP_DB_TYPE` | No | `sqlite` | `sqlite`, `postgres`, or `mysql` |
| `NAVIAMP_DB_PATH` | If sqlite | — | Path to `navidrome.db` |
| `NAVIAMP_DB_DSN` | If postgres/mysql | — | Connection string |
| `NAVIAMP_LISTEN` | No | `:8090` | Bind address |
| `NAVIAMP_CONFIG_FILE` | No | `naviamp.yaml` | Optional YAML config file path |

### PostgreSQL example

```bash
NAVIAMP_NAVIDROME_URL=http://localhost:4533
NAVIAMP_DB_TYPE=postgres
NAVIAMP_DB_DSN=postgres://navidrome:pass@localhost/navidrome?sslmode=disable
```

### MySQL example

```bash
NAVIAMP_NAVIDROME_URL=http://localhost:4533
NAVIAMP_DB_TYPE=mysql
NAVIAMP_DB_DSN=navidrome:pass@tcp(localhost:3306)/navidrome?parseTime=true
```

## Building from source

Requires Go 1.22+. No CGO needed (uses a pure-Go SQLite driver).

```bash
go build -o naviamp-sidecar ./cmd/naviamp-sidecar
```

Static Linux binary (for Docker):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags="-s -w" -trimpath \
  -o naviamp-sidecar ./cmd/naviamp-sidecar
```

## API reference

See [API.md](API.md) for the complete client integration guide with request/response
examples in curl, JavaScript, and Dart.

See [openapi.yaml](openapi.yaml) for the machine-readable OpenAPI 3.1 spec.

## Client integration

The Naviamp client detects the sidecar automatically on startup. For other
client implementations, see [API.md — Client detection](API.md#client-detection--the-probe-flow).

## Reverse proxy setup

To expose both Navidrome and the sidecar at the same public hostname, route by
path prefix in your reverse proxy:

```nginx
# nginx example
location /naviamp/ {
    proxy_pass http://localhost:8090;
}
location / {
    proxy_pass http://localhost:4533;
}
```

```
# Caddy example
example.com {
    handle /naviamp/* {
        reverse_proxy localhost:8090
    }
    handle {
        reverse_proxy localhost:4533
    }
}
```

## Security

- **Read-only**: The sidecar opens the Navidrome SQLite database with `mode=ro`
  and uses parameterised queries exclusively.
- **No credential storage**: Credentials are verified by forwarding a ping to
  Navidrome — the sidecar never inspects or stores passwords.
- **Minimal image**: The Docker image is built `FROM scratch` with only the
  static binary, giving a ~12 MB image with no shell or package manager.

## Tested against

Navidrome ≥ 0.52.0 (schema version as of 2024-01). If you encounter schema
errors with a different Navidrome version, please open an issue.

## License

MIT
