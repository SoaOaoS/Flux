package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Health endpoint ──────────────────────────────────────────────────────────

func TestE2E_HealthEndpoint(t *testing.T) {
	backend := newEchoBackend(t, "ok")
	cfg := gatewayConfig{
		addr:     freeAddr(t),
		backends: []string{backend.URL},
	}
	gw := startGateway(t, cfg.YAML())

	status, body := doGet(t, "http://"+gw.addr+"/healthz")
	assert.Equal(t, 200, status)
	assert.Contains(t, body, `"status":"ok"`)
	assert.Contains(t, body, `"version"`)
}

// ── Basic proxy ──────────────────────────────────────────────────────────────

func TestE2E_BasicProxy_ForwardsRequest(t *testing.T) {
	backend := newEchoBackend(t, "hello-world")
	cfg := gatewayConfig{
		addr:     freeAddr(t),
		backends: []string{backend.URL},
	}
	gw := startGateway(t, cfg.YAML())

	status, body := doGet(t, "http://"+gw.addr+"/anything")
	assert.Equal(t, 200, status)
	assert.Equal(t, "hello-world", body)
}

// ── Round-robin load balancing ───────────────────────────────────────────────

func TestE2E_RoundRobin_DistributesAcrossBackends(t *testing.T) {
	b1 := newEchoBackend(t, "backend-1")
	b2 := newEchoBackend(t, "backend-2")

	cfg := gatewayConfig{
		addr:     freeAddr(t),
		strategy: "round_robin",
		backends: []string{b1.URL, b2.URL},
	}
	gw := startGateway(t, cfg.YAML())

	seen := map[string]int{}
	for i := 0; i < 10; i++ {
		_, body := doGet(t, "http://"+gw.addr+"/")
		seen[strings.TrimSpace(body)]++
	}

	assert.Greater(t, seen["backend-1"], 0, "backend-1 should receive some traffic")
	assert.Greater(t, seen["backend-2"], 0, "backend-2 should receive some traffic")
}

// ── Passive failover ─────────────────────────────────────────────────────────

func TestE2E_PassiveFailover_Returns502OnDeadBackend(t *testing.T) {
	// Start a backend then immediately close it so the gateway cannot connect.
	dead := newEchoBackend(t, "should not see this")
	deadURL := dead.URL
	dead.Close() // close before the gateway uses it

	// Also provide a live backend so the gateway starts up successfully.
	live := newEchoBackend(t, "live")

	cfg := gatewayConfig{
		addr:     freeAddr(t),
		backends: []string{deadURL, live.URL},
	}
	gw := startGateway(t, cfg.YAML())

	// With round-robin over {dead, live}, at least one of the first two
	// requests should hit the dead backend and return 502.
	got502 := false
	for i := 0; i < 4; i++ {
		status, _ := doGet(t, "http://"+gw.addr+"/")
		if status == 502 {
			got502 = true
			break
		}
	}
	assert.True(t, got502, "at least one request to the dead backend must return 502")
}

// ── Rate limiting ─────────────────────────────────────────────────────────────

func TestE2E_RateLimit_Blocks_After_Burst(t *testing.T) {
	backend := newEchoBackend(t, "ok")
	cfg := gatewayConfig{
		addr:     freeAddr(t),
		backends: []string{backend.URL},
		rateLimit: &rateLimitCfg{
			rps:   0.001, // negligible — only burst tokens matter
			burst: 2,
		},
	}
	gw := startGateway(t, cfg.YAML())

	// First 2 requests (burst=2) must pass.
	for i := 0; i < 2; i++ {
		status, _ := doGet(t, "http://"+gw.addr+"/")
		require.Equal(t, 200, status, "request %d within burst must pass", i+1)
	}

	// Third request must be rate-limited.
	status, _ := doGet(t, "http://"+gw.addr+"/")
	assert.Equal(t, 429, status, "request after burst exhaustion must be rate-limited")
}

// ── JWT authentication ───────────────────────────────────────────────────────

func TestE2E_JWTAuth_Enforced(t *testing.T) {
	const secret = "e2e-jwt-secret-32chars-long!!!!!"
	backend := newEchoBackend(t, "protected")
	cfg := gatewayConfig{
		addr:     freeAddr(t),
		backends: []string{backend.URL},
		auth:     &authCfg{secret: secret},
	}
	gw := startGateway(t, cfg.YAML())

	// No token → 401.
	status, _ := doGet(t, "http://"+gw.addr+"/api")
	assert.Equal(t, 401, status, "missing token must return 401")

	// Invalid token → 401.
	status, _ = doGet(t, "http://"+gw.addr+"/api", "Authorization", "Bearer bogus.token.here")
	assert.Equal(t, 401, status, "invalid token must return 401")

	// Valid token → 200.
	token := makeJWT(t, secret)
	status, body := doGet(t, "http://"+gw.addr+"/api", "Authorization", "Bearer "+token)
	assert.Equal(t, 200, status, "valid token must pass")
	assert.Equal(t, "protected", body)
}

func TestE2E_JWTAuth_ExcludedPaths_NoTokenNeeded(t *testing.T) {
	const secret = "e2e-jwt-secret-32chars-long!!!!!"
	backend := newEchoBackend(t, "ok")
	cfg := gatewayConfig{
		addr:     freeAddr(t),
		backends: []string{backend.URL},
		auth: &authCfg{
			secret:  secret,
			exclude: []string{"/public", "/healthz"},
		},
	}
	gw := startGateway(t, cfg.YAML())

	// /public is excluded — no token required.
	status, _ := doGet(t, "http://"+gw.addr+"/public")
	assert.Equal(t, 200, status, "/public is excluded and must pass without a token")

	// /private requires a token.
	status, _ = doGet(t, "http://"+gw.addr+"/private")
	assert.Equal(t, 401, status, "/private is not excluded and must require a token")
}

// ── Hot-reload ───────────────────────────────────────────────────────────────

func TestE2E_HotReload_AddsBackend(t *testing.T) {
	b1 := newEchoBackend(t, "b1")
	b2 := newEchoBackend(t, "b2")

	addr := freeAddr(t)
	initial := gatewayConfig{
		addr:     addr,
		backends: []string{b1.URL},
	}
	gw := startGateway(t, initial.YAML())

	// Before reload — all traffic to b1.
	for i := 0; i < 3; i++ {
		_, body := doGet(t, "http://"+gw.addr+"/")
		assert.Equal(t, "b1", strings.TrimSpace(body))
	}

	// Hot-reload: add b2.
	updated := gatewayConfig{
		addr:     addr, // same listen address — server keeps running
		backends: []string{b1.URL, b2.URL},
	}
	rewriteConfig(t, gw, updated.YAML())
	time.Sleep(500 * time.Millisecond) // allow fsnotify event to fire

	// After reload — traffic should reach both backends.
	seen := map[string]int{}
	for i := 0; i < 20; i++ {
		_, body := doGet(t, "http://"+gw.addr+"/")
		seen[strings.TrimSpace(body)]++
	}

	assert.Greater(t, seen["b1"], 0, "b1 must still receive traffic after reload")
	assert.Greater(t, seen["b2"], 0, "b2 must receive traffic after reload")
}
