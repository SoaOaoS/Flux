# Architecture

## Overview

GOLB is a single-binary HTTP API Gateway and load balancer written in Go.
Every request passes through a configurable middleware chain before the proxy
layer selects a backend and forwards the request.

```
                          ┌─────────────────────────────────────────────────┐
                          │                   GOLB process                  │
                          │                                                  │
  Client ──── TCP ───────►│  /healthz ──── local handler (no middleware)   │
                          │                                                  │
                          │  /* ─────►  Logger                              │
                          │               └─► RateLimiter (optional)        │
                          │                     └─► JWTAuth (optional)      │
                          │                           └─► Gateway (proxy)   │
                          │                                 │                │
                          │                     ┌───────────┤                │
                          │                     ▼           ▼                │
                          │               Backend A    Backend B   ...       │
                          └─────────────────────────────────────────────────┘
```

## Package structure

```
golb/
├── cmd/
│   ├── gateway/        Entry point: flag parsing, wiring, server lifecycle
│   └── healthcheck/    Tiny probe binary used by Docker HEALTHCHECK
│
└── internal/
    ├── config/         YAML loading (Viper) + hot-reload via fsnotify
    ├── strategy/       Load-balancing algorithms + Backend runtime type
    │   ├── picker.go       Picker interface + New() factory
    │   ├── backend.go      Backend struct (atomic health + conn count)
    │   ├── roundrobin.go   Lock-free round robin
    │   ├── weighted.go     Smooth Weighted Round Robin (nginx algorithm)
    │   └── leastconn.go    Least active connections
    ├── health/         Active health-check monitor
    ├── middleware/     HTTP middleware constructors
    │   ├── logging.go      Structured JSON request logger + X-Request-Id
    │   ├── ratelimit.go    Per-IP token-bucket rate limiter
    │   └── auth.go         HS256 JWT Bearer-token verification
    └── proxy/          httputil.ReverseProxy wrapper + header injection
```

## Request lifecycle

### Happy path

1. **TCP accept** — the Go HTTP server accepts the connection.
2. **ServeMux routing** — `/healthz` is answered locally with `{"status":"ok"}`.
   All other paths proceed to step 3.
3. **Logger middleware** — generates a unique `X-Request-Id`, wraps the
   `ResponseWriter` to capture status + bytes written.
4. **RateLimiter** (if enabled) — looks up the per-IP token bucket; returns
   HTTP 429 if exhausted.
5. **JWTAuth** (if enabled) — validates the `Authorization: Bearer <token>`
   header; returns HTTP 401 on failure. Excluded paths skip this step.
6. **Gateway.director** — calls `picker.Next()` to select a healthy backend;
   rewrites `req.URL` and injects `X-Forwarded-*` headers; stores the selected
   `*Backend` in the request context.
7. **httputil.ReverseProxy** — dials the backend and streams the response.
8. **Gateway.modifyResponse** — retrieves the backend from context, calls
   `picker.Done(b)` to decrement the active-connection counter.
9. **Logger middleware** — emits a JSON log line with method, path, status,
   bytes, and duration.

### Error path (backend unreachable)

Steps 1–6 are the same. At step 7, the TCP dial fails:

7. **Gateway.errorHandler** — retrieves the backend from context, calls
   `picker.Done(b)`, then marks the backend unhealthy (`b.SetHealthy(false)`)
   as a **passive health check**. Returns HTTP 502 to the client.

The active health monitor (`internal/health`) runs concurrently on a timer and
will re-enable the backend once it starts responding to probes.

## Concurrency model

| Component | Synchronisation mechanism |
|---|---|
| `Backend.healthy` | `sync/atomic.Bool` — lock-free reads on every request |
| `Backend.activeConns` | `sync/atomic.Int64` — incremented in director, decremented in modifyResponse/errorHandler |
| `Gateway.picker` | `sync.RWMutex` — many concurrent readers, single writer (hot-reload) |
| `atomicHandler` (middleware chain) | `sync/atomic.Value` — single-word compare-and-swap |
| `health.Monitor.backends` | `sync.RWMutex` — updated by hot-reload, read by probe goroutines |
| `RateLimiter` entries map | `sync.Mutex` — one lock per map operation |

## Hot-reload

Config hot-reload is powered by Viper + fsnotify. When `gateway.yaml` is
saved:

1. `fsnotify` delivers an `WRITE` or `RENAME` event.
2. Viper re-reads the file and calls `config.Watch`'s callback.
3. The callback builds new `[]*strategy.Backend` and a new `strategy.Picker`.
4. `gw.UpdatePicker(newPicker)` atomically swaps the picker under `sync.RWMutex`.
5. `monitor.UpdateBackends(newBackends)` atomically swaps the backend slice
   under its own mutex.
6. `current.Store(buildChain(newCfg))` atomically swaps the full middleware
   chain, applying any rate-limit or auth config changes instantly.

In-flight requests using the old picker and backends complete normally.
No connections are dropped.
