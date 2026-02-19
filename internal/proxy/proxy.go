// Package proxy is the core request-forwarding layer of GOLB.
//
// Gateway wraps net/http/httputil.ReverseProxy and adds:
//   - Dynamic backend selection via a pluggable strategy.Picker.
//   - Standard proxy header injection (X-Forwarded-For, X-Real-IP, …).
//   - Active connection tracking (IncConns/DecConns on Backend).
//   - Passive health checks: a backend is marked unhealthy on any dial or
//     protocol error, and the active health monitor re-enables it later.
//   - Atomic picker swap for zero-downtime config hot-reloads.
package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"golb/internal/strategy"
)

// ctxKey is the unexported type used as the context key for the selected
// backend, preventing accidental collisions with other packages.
type ctxKey struct{}

// Gateway is the central http.Handler. It is safe for concurrent use.
type Gateway struct {
	mu     sync.RWMutex
	picker strategy.Picker
	rp     *httputil.ReverseProxy
}

// New creates a Gateway using the given Picker. The returned Gateway is ready
// to be wrapped in middleware and passed to http.Server.
func New(p strategy.Picker) *Gateway {
	gw := &Gateway{picker: p}
	gw.rp = &httputil.ReverseProxy{
		Director:       gw.director,
		ModifyResponse: gw.modifyResponse,
		ErrorHandler:   gw.errorHandler,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	return gw
}

// UpdatePicker atomically swaps the active Picker. In-flight requests using
// the old picker complete normally; new requests use the new picker immediately.
func (gw *Gateway) UpdatePicker(p strategy.Picker) {
	gw.mu.Lock()
	gw.picker = p
	gw.mu.Unlock()
}

// ServeHTTP satisfies http.Handler.
func (gw *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gw.rp.ServeHTTP(w, r)
}

// director rewrites the incoming request to target a backend chosen by the
// current Picker. The chosen Backend is stored in the request context so that
// modifyResponse and errorHandler can call Done on it.
func (gw *Gateway) director(req *http.Request) {
	gw.mu.RLock()
	picker := gw.picker
	gw.mu.RUnlock()

	b, err := picker.Next()
	if err != nil {
		slog.Error("no healthy backend available", "error", err)
		// Point at an unreachable address so ReverseProxy triggers its
		// ErrorHandler via a dial error rather than panicking.
		req.URL.Scheme = "http"
		req.URL.Host = "0.0.0.0:0"
		return
	}

	originalHost := req.Host

	req.URL.Scheme = b.URL.Scheme
	req.URL.Host = b.URL.Host
	req.Host = b.URL.Host

	// Strip hop-by-hop headers that must not be forwarded upstream.
	req.Header.Del("Te")
	req.Header.Del("Trailers")

	// Inject standard proxy headers so backends can reconstruct the original
	// request context (real client IP, original host, original scheme).
	if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
		req.Header.Set("X-Forwarded-For", prior+", "+req.RemoteAddr)
	} else {
		req.Header.Set("X-Forwarded-For", req.RemoteAddr)
	}
	req.Header.Set("X-Real-IP", req.RemoteAddr)
	req.Header.Set("X-Forwarded-Host", originalHost)
	req.Header.Set("X-Forwarded-Proto", requestScheme(req))

	slog.Debug("proxying request",
		"method", req.Method,
		"path", req.URL.Path,
		"backend", b.RawURL,
	)

	// Attach the selected backend to the request context so downstream hooks
	// can retrieve it without sharing mutable state across goroutines.
	newReq := req.WithContext(context.WithValue(req.Context(), ctxKey{}, b))
	*req = *newReq
}

// modifyResponse is called on every successful upstream response.
// It releases the active-connection count for the selected backend.
func (gw *Gateway) modifyResponse(resp *http.Response) error {
	if b := backendFromCtx(resp.Request.Context()); b != nil {
		gw.mu.RLock()
		picker := gw.picker
		gw.mu.RUnlock()
		picker.Done(b)
		b.IncRequests()
	}
	return nil
}

// errorHandler is called when ReverseProxy cannot reach the backend (dial
// error, timeout, etc.). It performs a passive health check by marking the
// backend unhealthy so the strategy stops sending traffic to it until the
// active monitor revives it.
func (gw *Gateway) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if b := backendFromCtx(r.Context()); b != nil {
		gw.mu.RLock()
		picker := gw.picker
		gw.mu.RUnlock()
		picker.Done(b)

		// Passive health check — mark unhealthy immediately.
		// The health.Monitor will clear this flag once the backend recovers.
		b.SetHealthy(false)
		b.IncRequests()
		b.IncErrors()

		slog.Error("backend error — marked unhealthy",
			"backend", b.RawURL,
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
	} else {
		slog.Error("backend error",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
	}
	http.Error(w, "bad gateway", http.StatusBadGateway)
}

func backendFromCtx(ctx context.Context) *strategy.Backend {
	b, _ := ctx.Value(ctxKey{}).(*strategy.Backend)
	return b
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
