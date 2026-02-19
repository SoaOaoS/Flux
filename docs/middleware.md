# Middleware

All middleware follows the standard Go pattern `func(http.Handler) http.Handler`.
The chain is applied in this order (outermost first):

```
Logger → RateLimiter → JWTAuth → Gateway (proxy)
```

The chain is rebuilt atomically on every hot-reload, so changes to rate-limit
or auth settings take effect without restarting the process.

---

## Logger

Always active — cannot be disabled.

### What it does

- Generates a cryptographically random `X-Request-Id` (16 hex characters) for
  each request.
- Injects `X-Request-Id` into both the **inbound request** (forwarded to the
  backend) and the **response** (returned to the client).
- Wraps the `ResponseWriter` to capture the HTTP status code and response
  body size.
- Emits one structured JSON log line per request **after** the response is
  sent.

### Log fields

| Field | Type | Description |
|---|---|---|
| `time` | RFC 3339 | When the request was received. |
| `level` | string | Always `"INFO"`. |
| `msg` | string | Always `"request"`. |
| `request_id` | string | Unique identifier for this request. |
| `method` | string | HTTP method (GET, POST, …). |
| `path` | string | Request path. |
| `remote_addr` | string | Client TCP address. |
| `status` | int | HTTP status code written to the client. |
| `bytes` | int | Response body size in bytes. |
| `duration_ms` | int | Total handler duration in milliseconds. |

### Example

```json
{
  "time": "2026-02-18T21:00:00Z",
  "level": "INFO",
  "msg": "request",
  "request_id": "a1b2c3d4e5f67890",
  "method": "GET",
  "path": "/api/users",
  "remote_addr": "10.0.0.5:54321",
  "status": 200,
  "bytes": 1024,
  "duration_ms": 3
}
```

---

## Rate Limiter

Enable in `gateway.yaml`:

```yaml
rate_limit:
  enabled: true
  rps:     100
  burst:   200
```

### Algorithm: Token Bucket

Each unique client IP gets its own token bucket (via `golang.org/x/time/rate`):

- The bucket holds at most `burst` tokens.
- Tokens refill at `rps` per second.
- Each request consumes one token.
- If the bucket is empty, the request is immediately rejected with HTTP 429.
  There is no queuing — the client must retry with back-off.

### IP resolution

The client IP is resolved in order:
1. `X-Real-IP` header (set by the proxy director for upstream requests).
2. TCP `RemoteAddr` from the connection.

### Memory management

Limiter entries are stored in a `sync.Mutex`-protected `map[string]*ipEntry`.
A background goroutine runs every 5 minutes and deletes entries that have not
been seen for more than 10 minutes, preventing unbounded memory growth.

### Tuning guide

| Use case | `rps` | `burst` |
|---|---|---|
| Public API | 10 | 30 |
| Mobile / SPA | 50 | 100 |
| Internal service | 500 | 1000 |
| CDN origin | 2000 | 5000 |

---

## JWT Authentication

Enable in `gateway.yaml`:

```yaml
auth:
  enabled: true
  secret:  "your-hs256-secret"
  exclude:
    - "/healthz"
```

### Algorithm

Tokens must be signed with **HMAC-SHA256 (HS256)**. On each request:

1. Extract `Authorization: Bearer <token>`.
2. Parse and verify the token signature using `golang-jwt/jwt/v5`.
3. **Reject any non-HMAC algorithm** — prevents the `alg:none` attack where a
   crafted token claims no signature is required.
4. Return HTTP 401 if the header is missing, the token is malformed, or the
   signature is invalid.
5. Forward to the next handler if the token is valid.

### Excluded paths

Paths listed under `auth.exclude` bypass the authentication check entirely.
Use this for:
- `GET /healthz` — Docker / load-balancer health probes.
- `GET /metrics` — Prometheus scrape endpoint.
- Publicly accessible resources.

### Issuing tokens (example)

```go
import jwtlib "github.com/golang-jwt/jwt/v5"

token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, jwtlib.MapClaims{
    "sub": "user-id-123",
    "exp": time.Now().Add(24 * time.Hour).Unix(),
})
signed, _ := token.SignedString([]byte("your-hs256-secret"))
// Use: Authorization: Bearer <signed>
```

### Security notes

- Store the secret in an environment variable or secrets manager.
  Never commit it to version control.
- Minimum recommended secret length: 32 bytes (256 bits).
- GOLB only verifies signatures — it does not enforce `exp`, `iss`, or custom
  claims. Add application-level claim validation in your backends if needed.
