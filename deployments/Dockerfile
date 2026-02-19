# ─────────────────────────────────────────────────────────────────────────────
# GOLB — Multi-stage Dockerfile
#
# Build args (all optional, injected by CI / Makefile):
#   VERSION    — git tag or "dev"
#   COMMIT     — short git SHA
#   BUILD_DATE — RFC 3339 timestamp
#
# Usage:
#   docker build \
#     --build-arg VERSION=$(git describe --tags --always) \
#     --build-arg COMMIT=$(git rev-parse --short HEAD) \
#     --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     -f deployments/Dockerfile -t golb:latest .
# ─────────────────────────────────────────────────────────────────────────────

ARG GO_VERSION=1.24

# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:${GO_VERSION}-alpine AS builder

# git is needed for VCS stamping; ca-certificates for TLS during build.
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache module downloads separately from source so they are only re-fetched
# when go.mod / go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# Build the main gateway binary with version info embedded via ldflags.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildDate=${BUILD_DATE}" \
    -o /out/gateway ./cmd/gateway

# Build the Docker HEALTHCHECK probe.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/healthcheck ./cmd/healthcheck

# ── Runtime stage ─────────────────────────────────────────────────────────────
# distroless/static provides:
#   • CA certificates  (TLS to backends works out of the box)
#   • Timezone data
#   • nonroot user uid/gid 65532  (no shell — minimal attack surface)
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/gateway     /bin/gateway
COPY --from=builder /out/healthcheck /bin/healthcheck

# Default configuration — override at runtime with a volume mount:
#   -v /host/gateway.yaml:/etc/golb/gateway.yaml:ro
COPY configs/gateway.yaml /etc/golb/gateway.yaml

USER nonroot:nonroot

EXPOSE 8080

# Docker marks the container unhealthy if /healthz stops responding.
HEALTHCHECK \
    --interval=10s \
    --timeout=3s \
    --start-period=5s \
    --retries=3 \
    CMD ["/bin/healthcheck", "http://localhost:8080/healthz"]

ENTRYPOINT ["/bin/gateway"]
CMD ["-config", "/etc/golb/gateway.yaml"]
