# Load Balancing

GOLB supports three load-balancing algorithms, all of which:
- Skip backends that are currently marked **unhealthy**.
- Return `ErrNoHealthyBackend` when no backends are available (→ HTTP 502).
- Track active connections via lock-free atomics on each `Backend`.

Set the algorithm in `gateway.yaml`:

```yaml
strategy: "round_robin"  # or weighted_round_robin, least_connections
```

---

## Round Robin

**Config value:** `round_robin` (default)

Distributes requests evenly across all healthy backends in a circular order.

### Implementation

A single `atomic.Uint64` counter is incremented on every request. The backend
is selected as `healthy[counter % len(healthy)]`. This is completely lock-free
and safe for high-concurrency workloads.

### When to use

- Backends are homogeneous (same CPU/RAM/capacity).
- Requests have similar durations (e.g. a typical REST API).

### Distribution example (3 backends)

```
Request:  1  2  3  4  5  6  7  8  9
Backend:  A  B  C  A  B  C  A  B  C
```

---

## Weighted Round Robin

**Config value:** `weighted_round_robin`

Distributes requests proportionally to each backend's `weight`. Uses the
**Smooth Weighted Round Robin** algorithm (the same algorithm used by nginx),
which avoids long consecutive runs to a single backend.

### Algorithm

On each request:
1. Add each backend's weight to its `currentWeight`.
2. Select the backend with the highest `currentWeight`.
3. Subtract the total weight (sum of all healthy backends) from the selected
   backend's `currentWeight`.

### When to use

- Backends have different capacities (e.g. one node has 4 CPUs, another has 2).
- You want to send more traffic to newer, faster hosts.

### Distribution example (weight A=3, B=1)

| Step | current_weight before | Selected | current_weight after |
|---|---|---|---|
| 1 | A=3, B=1 | **A** | A=−1, B=1 |
| 2 | A=2, B=2 | **A** | A=−2, B=2 |
| 3 | A=1, B=3 | **B** | A=1, B=−1 |
| 4 | A=4, B=0 | **A** | A=0, B=0 |

→ A receives 3 out of 4 requests (75%), B receives 1 (25%). Distribution
   is interleaved rather than consecutive (A,A,B,A instead of A,A,A,B).

---

## Least Connections

**Config value:** `least_connections`

Routes each new request to the healthy backend with the **fewest active
connections** at that instant.

### Implementation

On `Next()`, iterates all healthy backends and picks the one with the lowest
`activeConns` atomic counter. Ties are broken by order in the list. `IncConns`
is called on selection; `DecConns` is called in `ModifyResponse` (success) or
`ErrorHandler` (failure).

### When to use

- Requests have highly variable durations (e.g. file uploads, streaming,
  WebSockets, long-polling).
- Some backends are slower (high response time keeps connections open longer).

### Distribution example

```
t=0   Request 1 → A  (A=1, B=0)
t=0   Request 2 → B  (A=1, B=1)
t=0   Request 3 → A  (tie → first in list)
t=1   A finishes request 1 → A=1, B=1
t=1   Request 4 → A  (tie → first in list)
```

---

## Choosing an algorithm

| Scenario | Recommended algorithm |
|---|---|
| Homogeneous backends, short requests | `round_robin` |
| Backends with different hardware specs | `weighted_round_robin` |
| Variable request duration / streaming | `least_connections` |
| Mix of fast and slow backends | `least_connections` |
