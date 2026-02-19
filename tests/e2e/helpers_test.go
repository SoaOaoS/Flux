// Package e2e contains end-to-end tests that compile and run the real gateway
// binary as a subprocess. Each test spins up in-process mock backends
// (httptest.Server), writes a temporary gateway.yaml, starts the binary, and
// exercises the full HTTP path.
package e2e

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// gatewayBin is the path to the compiled gateway binary, set by TestMain.
var gatewayBin string

// TestMain builds the gateway binary once before all E2E tests run.
// Set E2E_GATEWAY_BIN to skip the build step (useful in CI with a pre-built binary).
func TestMain(m *testing.M) {
	if bin := os.Getenv("E2E_GATEWAY_BIN"); bin != "" {
		gatewayBin = bin
	} else {
		tmp, err := os.MkdirTemp("", "golb-e2e-*")
		if err != nil {
			log.Fatalf("e2e: create temp dir: %v", err)
		}
		defer os.RemoveAll(tmp)

		gatewayBin = filepath.Join(tmp, "gateway")

		// Build from the module root (two directories above this file).
		root, err := filepath.Abs("../..")
		if err != nil {
			log.Fatalf("e2e: resolve module root: %v", err)
		}

		cmd := exec.Command("go", "build", "-o", gatewayBin, "./cmd/gateway")
		cmd.Dir = root
		cmd.Stdout = os.Stderr // surface build errors in test output
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("e2e: build gateway binary: %v", err)
		}
	}

	os.Exit(m.Run())
}

// gatewayProcess holds a running gateway subprocess and its listen address.
type gatewayProcess struct {
	addr    string
	cmd     *exec.Cmd
	cfgFile string
}

// startGateway writes configYAML to a temp file and starts the gateway binary.
// The gateway is stopped and the temp file removed when the test ends.
func startGateway(t *testing.T, configYAML string) *gatewayProcess {
	t.Helper()

	// Write config to a temp file.
	f, err := os.CreateTemp(t.TempDir(), "gateway-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(configYAML)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	gw := &gatewayProcess{
		cfgFile: f.Name(),
		cmd:     exec.Command(gatewayBin, "-config", f.Name()),
	}
	// Discard gateway logs unless TEST_VERBOSE is set (reduces noise).
	if os.Getenv("TEST_VERBOSE") != "" {
		gw.cmd.Stdout = os.Stdout
		gw.cmd.Stderr = os.Stderr
	}

	require.NoError(t, gw.cmd.Start())

	// Extract listen address from the config YAML (simple parse).
	gw.addr = extractListenAddr(configYAML)

	t.Cleanup(func() {
		_ = gw.cmd.Process.Signal(syscall.SIGTERM)
		_ = gw.cmd.Wait()
	})

	waitReady(t, gw.addr)
	return gw
}

// rewriteConfig atomically replaces the gateway's config file, triggering a
// hot-reload. Call time.Sleep(≥200ms) afterwards to let the watcher fire.
func rewriteConfig(t *testing.T, gw *gatewayProcess, configYAML string) {
	t.Helper()
	require.NoError(t, os.WriteFile(gw.cfgFile, []byte(configYAML), 0o644))
}

// waitReady polls GET /healthz on addr until it returns 200 or times out.
func waitReady(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("gateway at %s did not become ready within 8 seconds", addr)
}

// freeAddr returns an unused "127.0.0.1:PORT" address by briefly binding to
// port 0 and then closing the listener.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// newEchoBackend starts an httptest.Server that always responds with body.
func newEchoBackend(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// makeJWT creates a signed HS256 JWT token with a 1-hour expiry.
func makeJWT(t *testing.T, secret string) string {
	t.Helper()
	tok := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, jwtlib.MapClaims{
		"sub": "e2e-test",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	s, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

// doGet performs a GET request and returns the status code and body.
func doGet(t *testing.T, url string, headers ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// gatewayConfig builds the gateway YAML for a test.
type gatewayConfig struct {
	addr        string
	strategy    string
	backends    []string
	healthCheck bool
	rateLimit   *rateLimitCfg
	auth        *authCfg
}

type rateLimitCfg struct {
	rps   float64
	burst int
}

type authCfg struct {
	secret  string
	exclude []string
}

func (c gatewayConfig) YAML() string {
	strat := c.strategy
	if strat == "" {
		strat = "round_robin"
	}
	hcEnabled := "false"
	if c.healthCheck {
		hcEnabled = "true"
	}

	out := fmt.Sprintf(`listen_addr: %q
strategy: %q
health_check:
  enabled: %s
  interval: "1s"
  timeout: "500ms"
  path: "/healthz"
`, c.addr, strat, hcEnabled)

	out += "backends:\n"
	for _, b := range c.backends {
		out += fmt.Sprintf("  - url: %q\n    weight: 1\n", b)
	}

	if c.rateLimit != nil {
		out += fmt.Sprintf(`rate_limit:
  enabled: true
  rps: %g
  burst: %d
`, c.rateLimit.rps, c.rateLimit.burst)
	} else {
		out += "rate_limit:\n  enabled: false\n"
	}

	if c.auth != nil {
		out += fmt.Sprintf("auth:\n  enabled: true\n  secret: %q\n", c.auth.secret)
		if len(c.auth.exclude) > 0 {
			out += "  exclude:\n"
			for _, p := range c.auth.exclude {
				out += fmt.Sprintf("    - %q\n", p)
			}
		}
	} else {
		out += "auth:\n  enabled: false\n"
	}

	return out
}

// extractListenAddr parses the listen_addr from a YAML string.
// It looks for the pattern: listen_addr: "127.0.0.1:PORT"
func extractListenAddr(yaml string) string {
	var addr string
	for _, line := range splitLines(yaml) {
		if len(line) > 14 && line[:13] == "listen_addr: " {
			// Strip surrounding quotes.
			addr = line[13:]
			if len(addr) >= 2 && addr[0] == '"' {
				addr = addr[1 : len(addr)-1]
			}
			break
		}
	}
	return addr
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
