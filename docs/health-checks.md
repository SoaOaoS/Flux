# Health Checks

GOLB uses two complementary health-check mechanisms to prevent traffic from
reaching unavailable backends.

---

## Active health checks

The `health.Monitor` runs a background goroutine that periodically sends a
`GET` request to each backend's health endpoint and updates its health state.

### How it works

1. A `time.Ticker` fires every `health_check.interval` (default 10 s).
2. For each backend, a goroutine is spawned concurrently.
3. The goroutine sends `GET <backend_url><health_check.path>` with a timeout
   of `health_check.timeout`.
4. **2xx or 3xx response** → backend is marked **healthy**.
5. **4xx, 5xx, or network error** → backend is marked **unhealthy**.
6. State transitions are logged at WARN (unhealthy) or INFO (recovered).

### Configuration

```yaml
health_check:
  enabled:  true
  interval: "10s"     # probe cadence
  timeout:  "2s"      # per-probe timeout
  path:     "/healthz" # appended to each backend URL
```

### Startup behaviour

The monitor sends an **immediate probe** to all backends when `Start()` is
called — before the first ticker fires. This ensures backends are classified
quickly and the gateway does not send traffic to an already-unhealthy backend
during the initial interval.

### Hot-reload

When the config is hot-reloaded with a different backend list,
`monitor.UpdateBackends()` atomically replaces the backend slice. Probes in
flight at the time of the update complete against the old backends; the next
ticker cycle uses the new list.

---

## Passive health checks

Passive checks happen automatically during normal request processing — no
configuration required.

### How it works

When `httputil.ReverseProxy` cannot connect to a backend (TCP dial failure,
connection refused, timeout), the `proxy.Gateway.errorHandler` is called. It:

1. Calls `picker.Done(b)` to release the active-connection counter.
2. Calls `b.SetHealthy(false)` immediately — **without waiting for the next
   active probe cycle**.
3. Returns HTTP 502 to the client.

### Recovery

Once a backend is marked unhealthy by a passive check, the next active probe
cycle will attempt to reach it. If the backend has recovered (responds with
2xx/3xx), the active monitor calls `b.SetHealthy(true)` and traffic resumes.

---

## Health state in the load-balancing strategies

All three strategies call `b.IsHealthy()` — an `atomic.Bool` load — on every
call to `Next()`. Unhealthy backends are excluded from the candidate set
instantly, with no locking overhead.

If **all** backends are unhealthy, `Next()` returns `ErrNoHealthyBackend` and
the gateway responds with HTTP 502.

---

## Backend health endpoint requirements

Your backend's health endpoint should:

- Respond on `GET <path>`.
- Return HTTP **200** when the service is ready to accept traffic.
- Return HTTP **5xx** or refuse connections when it is not.
- Respond within `health_check.timeout` (default 2 s).

A minimal Go health handler:

```go
http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
})
```
