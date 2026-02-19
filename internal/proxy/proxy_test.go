package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"golb/internal/proxy"
	"golb/internal/strategy"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func singleBackendGateway(t *testing.T, backendURL string) (*proxy.Gateway, *strategy.Backend) {
	t.Helper()
	b, err := strategy.NewBackend(backendURL, 1)
	require.NoError(t, err)
	p := strategy.NewRoundRobin([]*strategy.Backend{b})
	return proxy.New(p), b
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGateway_ForwardsRequestAndBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from backend"))
	}))
	defer backend.Close()

	gw, _ := singleBackendGateway(t, backend.URL)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "hello from backend", string(body))
}

func TestGateway_InjectsProxyHeaders(t *testing.T) {
	var (
		mu              sync.Mutex
		receivedHeaders http.Header
	)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedHeaders = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	gw, _ := singleBackendGateway(t, backend.URL)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/anything")
	require.NoError(t, err)
	resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	assert.NotEmpty(t, receivedHeaders.Get("X-Forwarded-For"), "X-Forwarded-For must be set")
	assert.NotEmpty(t, receivedHeaders.Get("X-Real-Ip"), "X-Real-IP must be set")
	assert.NotEmpty(t, receivedHeaders.Get("X-Forwarded-Host"), "X-Forwarded-Host must be set")
	assert.Equal(t, "http", receivedHeaders.Get("X-Forwarded-Proto"))
}

func TestGateway_NoHealthyBackend_Returns502(t *testing.T) {
	b, err := strategy.NewBackend("http://127.0.0.1:1", 1)
	require.NoError(t, err)
	b.SetHealthy(false) // explicitly mark unhealthy

	p := strategy.NewRoundRobin([]*strategy.Backend{b})
	gw := proxy.New(p)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestGateway_PassiveHealthCheck_MarksUnhealthy(t *testing.T) {
	// Start a backend, note its URL, then shut it down.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	backendURL := backend.URL
	backend.Close() // backend is now unreachable

	b, err := strategy.NewBackend(backendURL, 1)
	require.NoError(t, err)
	assert.True(t, b.IsHealthy(), "backend should start healthy")

	p := strategy.NewRoundRobin([]*strategy.Backend{b})
	gw := proxy.New(p)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/probe")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode, "dial failure should return 502")
	assert.False(t, b.IsHealthy(), "backend should be marked unhealthy after dial error")
}

func TestGateway_UpdatePicker_SwitchesBackend(t *testing.T) {
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("b1"))
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("b2"))
	}))
	defer backend2.Close()

	// Start with backend1.
	gw, _ := singleBackendGateway(t, backend1.URL)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	body1 := doGet(t, srv.URL+"/")
	assert.Equal(t, "b1", body1)

	// Swap picker to backend2.
	b2, err := strategy.NewBackend(backend2.URL, 1)
	require.NoError(t, err)
	newPicker := strategy.NewRoundRobin([]*strategy.Backend{b2})
	gw.UpdatePicker(newPicker)

	body2 := doGet(t, srv.URL+"/")
	assert.Equal(t, "b2", body2, "after UpdatePicker, traffic must flow to the new backend")
}

func TestGateway_ForwardsStatusCodes(t *testing.T) {
	for _, code := range []int{200, 201, 404, 503} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer backend.Close()

			gw, _ := singleBackendGateway(t, backend.URL)
			srv := httptest.NewServer(gw)
			defer srv.Close()

			resp, err := http.Get(srv.URL + "/")
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, code, resp.StatusCode)
		})
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func doGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}
