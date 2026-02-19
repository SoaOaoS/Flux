# GOLB — Go API Gateway & Load Balancer

![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green)
![Tests](https://img.shields.io/badge/tests-35%20passing-brightgreen)
![Docker](https://img.shields.io/badge/Docker-distroless-blue?logo=docker)

A production-ready HTTP **API Gateway** and **Load Balancer** built in pure Go.
Zero external runtime dependencies. Single static binary. Hot-reloadable config.

---

## Architecture

```
                    ┌──────────────────────────────────────────────┐
                    │                  GOLB binary                 │
                    │                                              │
Client ── HTTP ────►│  /healthz ── local JSON response            │
                    │                                              │
                    │  /* ──► Logger                               │
                    │           └─► RateLimiter  (per-IP bucket)  │
                    │                 └─► JWTAuth (HS256)          │
                    │                       └─► Reverse Proxy      │
                    │                             │                │
                    │              ┌──────────────┼─────────┐      │
                    │              ▼              ▼         ▼      │
                    │          Backend A      Backend B   ...      │
                    └──────────────────────────────────────────────┘
                                  ▲              ▲
                    Health Monitor│ (background) │
                    ──────────────┴──────────────┘
```

---

## Features

| Feature | Status |
|---|---|
| HTTP/1.1 reverse proxy | ✓ |
| Round Robin | ✓ |
| Weighted Round Robin (Smooth, nginx algorithm) | ✓ |
| Least Connections | ✓ |
| Active health checks (periodic probing) | ✓ |
| Passive health checks (mark unhealthy on dial error) | ✓ |
| Zero-downtime hot-reload (YAML file watcher) | ✓ |
| Per-IP rate limiting (token bucket) | ✓ |
| JWT authentication (HS256) with exclude list | ✓ |
| Structured JSON logs to stdout | ✓ |
| `X-Forwarded-For`, `X-Real-IP`, `X-Request-Id` headers | ✓ |
| Graceful shutdown (SIGTERM drain) | ✓ |
| Docker image (distroless, non-root, HEALTHCHECK) | ✓ |
| Version info embedded at build time | ✓ |

---

## Quick start

**Prerequisites:** Go 1.24+, or Docker.

### 1 — Clone and build

```bash
git clone https://github.com/your-username/go-gateway-lb
cd go-gateway-lb
make build
```

### 2 — Start a dummy backend

```bash
# In a second terminal — a simple echo server
go run -v . &   # or any HTTP server on port 8081
python3 -m http.server 8081
```

### 3 — Run the gateway

```bash
./bin/gateway -config configs/gateway.yaml
```

### 4 — Send a request

```bash
curl http://localhost:8080/
```

### 5 — Check gateway health

```bash
curl -s http://localhost:8080/healthz | jq .
# {
#   "status": "ok",
#   "version": "dev",
#   "commit": "abc1234",
#   "build_date": "unknown",
#   "uptime": "12s"
# }
```

---

## Docker

```bash
# Build
make docker-build

# Run with two echo backends
make docker-run

# Test
curl http://localhost:8080/
# hello from backend-1
curl http://localhost:8080/
# hello from backend-2
```

The image is based on `gcr.io/distroless/static-debian12:nonroot`:
- **Non-root** user (uid 65532)
- **No shell** (minimal attack surface)
- **CA certificates** included (TLS to backends works out of the box)
- **HEALTHCHECK** built in

---

## Configuration

All settings live in `gateway.yaml`. Edit and save the file while the gateway
is running — changes take effect within one second.

```yaml
listen_addr: ":8080"
strategy: "round_robin"   # round_robin | weighted_round_robin | least_connections

backends:
  - url: "http://app-1:8080"
    weight: 2
  - url: "http://app-2:8080"
    weight: 1

health_check:
  enabled:  true
  interval: "10s"
  timeout:  "2s"
  path:     "/healthz"

rate_limit:
  enabled: true
  rps:     100
  burst:   200

auth:
  enabled: true
  secret:  "your-256-bit-secret"
  exclude:
    - "/healthz"
    - "/public"
```

See [`docs/configuration.md`](docs/configuration.md) for the full reference.

### Example configs

| File | Description |
|---|---|
| [`configs/examples/minimal.yaml`](configs/examples/minimal.yaml) | One backend, no auth, no rate limiting |
| [`configs/examples/production.yaml`](configs/examples/production.yaml) | 3 backends, all features enabled |
| [`configs/examples/jwt-auth.yaml`](configs/examples/jwt-auth.yaml) | JWT authentication with excluded paths |
| [`configs/examples/rate-limited.yaml`](configs/examples/rate-limited.yaml) | Rate limiting with tuning comments |

---

## Testing

```bash
# All tests (vet + unit + e2e)
make test

# Unit and functional tests only (fast, no subprocess)
make unit-test

# End-to-end tests (compiles the binary, starts real processes)
make e2e-test
```

### Test coverage

| Package | Tests | Type |
|---|---|---|
| `internal/config` | 7 | Unit |
| `internal/strategy` | 12 | Unit |
| `internal/middleware` | 9 | Functional (httptest) |
| `internal/proxy` | 6 | Integration (httptest) |
| `tests/e2e` | 8 | End-to-end (real binary) |

E2E tests cover: basic proxy, round-robin distribution, passive failover,
rate limiting, JWT auth, excluded paths, and YAML hot-reload.

---

## Documentation

| Document | Description |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | System design, request lifecycle, concurrency model |
| [`docs/configuration.md`](docs/configuration.md) | Full YAML reference with field descriptions |
| [`docs/load-balancing.md`](docs/load-balancing.md) | Algorithm explanations with step-by-step examples |
| [`docs/health-checks.md`](docs/health-checks.md) | Active and passive health check mechanics |
| [`docs/middleware.md`](docs/middleware.md) | Logger, rate limiter, and JWT auth details |
| [`docs/deployment.md`](docs/deployment.md) | Binary, Docker, Kubernetes, log aggregation |

---

## Project structure

```
go-gateway-lb/
├── cmd/
│   ├── gateway/            Main entry point
│   └── healthcheck/        Docker HEALTHCHECK probe
├── internal/
│   ├── config/             Viper YAML loader + hot-reload
│   ├── strategy/           Load-balancing algorithms
│   ├── health/             Active health monitor
│   ├── middleware/         Logger, RateLimiter, JWTAuth
│   └── proxy/              Reverse proxy core
├── tests/e2e/              End-to-end test suite
├── configs/
│   ├── gateway.yaml        Default configuration
│   └── examples/           Ready-to-use example configs
├── deployments/
│   ├── Dockerfile          Multi-stage, distroless image
│   └── docker-compose.yml  Local dev environment
├── docs/                   Technical documentation
├── scripts/build.sh        Build helper script
└── Makefile                Developer workflow
```

---

## Make targets

```
make build        Compile gateway binary into bin/
make test         vet + unit-test + e2e-test
make unit-test    Unit and functional tests with race detector
make e2e-test     End-to-end tests against compiled binary
make docker-build Build Docker image with version labels
make docker-run   Start gateway + backends via Docker Compose
make docker-stop  Stop Docker Compose stack
make vet          Run go vet
make tidy         Run go mod tidy
make clean        Remove build artifacts
```

---

## Roadmap

- [x] Phase 1 — Static reverse proxy, structured logging
- [x] Phase 2 — YAML config, hot-reload, Round Robin
- [x] Phase 3 — Active + passive health checks, Least Connections
- [x] Phase 4 — Rate limiting, JWT auth, `/healthz` endpoint
- [ ] Path-based routing (`/api/v1` → Backend A, `/static` → Backend B)
- [ ] Host-based routing (virtual hosts)
- [ ] Prometheus metrics endpoint
- [ ] TLS termination
- [ ] Connection pooling per backend

---

## License

MIT — see [LICENSE](LICENSE) for details.
# Flux
