package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"golb/internal/config"
)

func TestDefault_ReturnsUsableConfig(t *testing.T) {
	cfg := config.Default()

	assert.Equal(t, ":8080", cfg.ListenAddr)
	assert.Equal(t, "round_robin", cfg.Strategy)
	require.Len(t, cfg.Backends, 1)
	assert.Equal(t, "http://localhost:8081", cfg.Backends[0].URL)
	assert.Equal(t, 1, cfg.Backends[0].Weight)
	assert.True(t, cfg.HealthCheck.Enabled)
	assert.False(t, cfg.RateLimit.Enabled)
	assert.False(t, cfg.Auth.Enabled)
}

func TestLoad_ValidYAML(t *testing.T) {
	yaml := `
listen_addr: ":9090"
strategy: "least_connections"
backends:
  - url: "http://backend-a:8000"
    weight: 2
  - url: "http://backend-b:8001"
    weight: 1
health_check:
  enabled: true
  interval: "5s"
  timeout: "1s"
  path: "/ping"
rate_limit:
  enabled: true
  rps: 50
  burst: 100
auth:
  enabled: true
  secret: "supersecret"
  exclude:
    - "/public"
`
	f := writeTempYAML(t, yaml)
	cfg, _, err := config.Load(f)
	require.NoError(t, err)

	assert.Equal(t, ":9090", cfg.ListenAddr)
	assert.Equal(t, "least_connections", cfg.Strategy)
	require.Len(t, cfg.Backends, 2)
	assert.Equal(t, "http://backend-a:8000", cfg.Backends[0].URL)
	assert.Equal(t, 2, cfg.Backends[0].Weight)
	assert.True(t, cfg.HealthCheck.Enabled)
	assert.Equal(t, "5s", cfg.HealthCheck.Interval)
	assert.Equal(t, "/ping", cfg.HealthCheck.Path)
	assert.True(t, cfg.RateLimit.Enabled)
	assert.Equal(t, 50.0, cfg.RateLimit.RPS)
	assert.True(t, cfg.Auth.Enabled)
	assert.Equal(t, "supersecret", cfg.Auth.Secret)
	assert.Contains(t, cfg.Auth.Exclude, "/public")
}

func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	_, _, err := config.Load("/nonexistent/path/gateway.yaml")
	assert.Error(t, err)
}

func TestLoad_EmptyBackends_ReturnsError(t *testing.T) {
	yaml := `
listen_addr: ":8080"
backends: []
`
	f := writeTempYAML(t, yaml)
	_, _, err := config.Load(f)
	assert.Error(t, err, "a config with no backends should be rejected")
}

func TestLoad_MissingWeightDefaultsToOne(t *testing.T) {
	yaml := `
backends:
  - url: "http://backend:8080"
`
	f := writeTempYAML(t, yaml)
	cfg, _, err := config.Load(f)
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Backends[0].Weight)
}

func TestHealthCheckCfg_ParsedInterval(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
	}{
		{"5s", 5 * time.Second},
		{"2m", 2 * time.Minute},
		{"", 10 * time.Second}, // default when empty
		{"0s", 10 * time.Second}, // default when zero
	}
	for _, tc := range cases {
		hc := config.HealthCheckCfg{Interval: tc.input}
		assert.Equal(t, tc.expected, hc.ParsedInterval(), "input: %q", tc.input)
	}
}

func TestHealthCheckCfg_ParsedTimeout(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
	}{
		{"3s", 3 * time.Second},
		{"", 2 * time.Second}, // default
	}
	for _, tc := range cases {
		hc := config.HealthCheckCfg{Timeout: tc.input}
		assert.Equal(t, tc.expected, hc.ParsedTimeout(), "input: %q", tc.input)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "gateway-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}
