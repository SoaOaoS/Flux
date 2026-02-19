# Deployment

## Binary

### Build

```bash
# Simple build
go build -o bin/gateway ./cmd/gateway

# Production build — stripped, with version info embedded
make build

# Or manually:
CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags="-s -w \
    -X main.version=$(git describe --tags --always) \
    -X main.commit=$(git rev-parse --short HEAD) \
    -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o bin/gateway ./cmd/gateway
```

### Run

```bash
./bin/gateway -config configs/gateway.yaml
```

### Signals

| Signal | Action |
|---|---|
| `SIGTERM` | Graceful shutdown — stops accepting new connections, waits up to 10 s for in-flight requests to complete. |
| `SIGINT` | Same as SIGTERM (triggered by Ctrl+C in a terminal). |

### Verify version

```bash
curl -s http://localhost:8080/healthz | jq .
# {
#   "status": "ok",
#   "version": "v1.2.0",
#   "commit": "abc1234",
#   "build_date": "2026-02-18T21:00:00Z",
#   "uptime": "5m30s"
# }
```

---

## Docker

### Build the image

```bash
docker build \
  --build-arg VERSION=$(git describe --tags --always) \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -f deployments/Dockerfile \
  -t golb:latest .
```

Or with the Makefile:

```bash
make docker-build
```

### Run the container

```bash
# Use the built-in default config
docker run -p 8080:8080 golb:latest

# Mount your own config
docker run -p 8080:8080 \
  -v /path/to/gateway.yaml:/etc/golb/gateway.yaml:ro \
  golb:latest

# Pass environment variables (e.g. JWT secret)
docker run -p 8080:8080 \
  -e JWT_SECRET=my-secret \
  -v /path/to/gateway.yaml:/etc/golb/gateway.yaml:ro \
  golb:latest
```

### Image properties

| Property | Value |
|---|---|
| Base image | `gcr.io/distroless/static-debian12:nonroot` |
| Runtime user | `nonroot` (uid 65532) |
| Exposed port | `8080` |
| HEALTHCHECK | `GET /healthz` every 10 s |
| CA certificates | Included (TLS to backends works) |
| Shell | None (distroless) |

---

## Docker Compose (local development)

```bash
# Start gateway + two echo backends
make docker-run
# or
docker compose -f deployments/docker-compose.yml up --build

# Test
curl http://localhost:8080/
# hello from backend-1   (or backend-2)

# Check gateway health
curl http://localhost:8080/healthz

# Stop
make docker-stop
```

---

## Kubernetes

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: golb
spec:
  replicas: 2
  selector:
    matchLabels:
      app: golb
  template:
    metadata:
      labels:
        app: golb
    spec:
      containers:
        - name: gateway
          image: your-registry/golb:latest
          ports:
            - containerPort: 8080
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 2
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          volumeMounts:
            - name: config
              mountPath: /etc/golb
              readOnly: true
          env:
            - name: JWT_SECRET
              valueFrom:
                secretKeyRef:
                  name: golb-secrets
                  key: jwt-secret
      volumes:
        - name: config
          configMap:
            name: golb-config
---
apiVersion: v1
kind: Service
metadata:
  name: golb
spec:
  selector:
    app: golb
  ports:
    - port: 80
      targetPort: 8080
  type: LoadBalancer
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: golb-config
data:
  gateway.yaml: |
    listen_addr: ":8080"
    strategy: "round_robin"
    backends:
      - url: "http://app-service:8080"
        weight: 1
    health_check:
      enabled: true
      interval: "10s"
      path: "/healthz"
    rate_limit:
      enabled: false
    auth:
      enabled: true
      secret: "${JWT_SECRET}"
      exclude:
        - "/healthz"
```

> **Note on hot-reload in Kubernetes:** Kubernetes updates ConfigMaps
> eventually (kubelet syncs every ~1 minute). Mount the ConfigMap as a volume
> (not env var) so fsnotify can detect the change when the symlink is updated.

---

## Log aggregation

GOLB writes structured JSON to stdout. Pipe it into any aggregator:

```bash
# Loki (via Promtail / alloy)
# Add labels in promtail.yaml:
# - job: golb
# - env: production

# Datadog
# Set DD_LOGS_ENABLED=true and collect from stdout.

# Elasticsearch / OpenSearch
# Use Filebeat or Fluentd with a JSON codec.

# Local pretty-printing
./bin/gateway | jq '.'
```

### Key log fields for dashboards

| Field | Use |
|---|---|
| `status` | HTTP status distribution, 5xx rate alerts |
| `duration_ms` | Latency percentiles (p50, p95, p99) |
| `path` | Top endpoints by volume / error rate |
| `remote_addr` | Traffic by client IP / region |
| `request_id` | Distributed tracing across services |
