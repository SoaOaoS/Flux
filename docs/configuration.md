# Configuration Reference

All gateway settings live in a single `gateway.yaml` file. The file is
**hot-reloadable**: save it while the gateway is running and changes take
effect within one second, without restarting the process.

## Top-level fields

| Key | Type | Default | Description |
|---|---|---|---|
| `listen_addr` | string | `":8080"` | TCP address the gateway listens on. |
| `strategy` | string | `"round_robin"` | Load-balancing algorithm. See [load-balancing.md](load-balancing.md). |
| `backends` | list | — | **Required.** At least one backend must be defined. |

## `backends[]`

| Key | Type | Default | Description |
|---|---|---|---|
| `url` | string | — | **Required.** Full URL of the upstream server, e.g. `http://app:8080`. HTTPS backends are supported. |
| `weight` | int | `1` | Relative weight used by `weighted_round_robin`. Ignored by other strategies. |

## `health_check`

Controls **active** health probing. See [health-checks.md](health-checks.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Set to `false` to disable active probing. |
| `interval` | duration | `"10s"` | How often each backend is probed. |
| `timeout` | duration | `"2s"` | HTTP timeout per probe request. |
| `path` | string | `"/healthz"` | Path appended to each backend URL for the probe GET request. |

### Duration format

Durations use Go's `time.ParseDuration` format:
`"300ms"`, `"1.5s"`, `"2m"`, `"1h30m"`.

## `rate_limit`

Controls **per-IP** token-bucket rate limiting. See [middleware.md](middleware.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable or disable rate limiting. |
| `rps` | float | `100` | Sustained requests per second per client IP. |
| `burst` | int | `200` | Maximum burst size (bucket capacity). |

## `auth`

Controls **JWT Bearer-token** authentication. See [middleware.md](middleware.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Enable or disable JWT authentication. |
| `secret` | string | — | HMAC-SHA256 signing secret. **Must match the issuer's secret.** |
| `exclude` | list of strings | `[]` | Exact URL paths that bypass authentication (e.g. `"/healthz"`). |

## Complete annotated example

```yaml
listen_addr: ":8080"
strategy: "weighted_round_robin"

backends:
  - url: "http://app-1:8080"
    weight: 3
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
  burst:   300

auth:
  enabled: true
  secret:  "change-me-32-chars-minimum-length"
  exclude:
    - "/healthz"
    - "/public"
```

## Command-line flags

| Flag | Default | Description |
|---|---|---|
| `-config` | `configs/gateway.yaml` | Path to the YAML configuration file. |

```bash
gateway -config /etc/golb/gateway.yaml
```

## Signals

| Signal | Effect |
|---|---|
| `SIGTERM` | Graceful shutdown (10 s drain window). |
| `SIGINT` | Same as SIGTERM (Ctrl+C). |
