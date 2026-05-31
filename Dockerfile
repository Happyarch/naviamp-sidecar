# Multi-stage build producing a minimal static Linux binary.
#
# Stage 1 (builder): compile with CGO_ENABLED=0 so the binary has zero shared
# library dependencies. This is possible because we use modernc.org/sqlite (a
# pure-Go SQLite port) instead of the CGO-based go-sqlite3.
#
# Stage 2 (runtime): copy only the binary into a scratch image. The final image
# is typically < 15 MB and has no shell, package manager, or OS utilities —
# reducing the attack surface to the single Go binary.

FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -trimpath \
    -o /naviamp-sidecar ./cmd/naviamp-sidecar

# ---------------------------------------------------------------------------

FROM scratch

# Copy the CA bundle so the sidecar can make HTTPS requests to Navidrome when
# TLS is in use. Without this, TLS certificate verification fails in scratch.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /naviamp-sidecar /naviamp-sidecar

# Default listen port — overridable via NAVIAMP_LISTEN env var.
EXPOSE 8090

ENTRYPOINT ["/naviamp-sidecar"]
