// Package middleware provides composable HTTP middleware constructors that
// follow the standard func(http.Handler) http.Handler pattern.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// responseRecorder wraps http.ResponseWriter to capture the status code and
// number of bytes written by the downstream handler.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	n, err := rr.ResponseWriter.Write(b)
	rr.bytes += n
	return n, err
}

// Logger returns a middleware that emits one structured JSON log line per
// request, including method, path, status, response size, and latency.
// It also generates a unique X-Request-Id header that is forwarded upstream
// and returned in the response for end-to-end tracing.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := newRequestID()

		r.Header.Set("X-Request-Id", reqID)
		w.Header().Set("X-Request-Id", reqID)

		rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)

		slog.Info("request",
			"request_id", reqID,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"status", rr.status,
			"bytes", rr.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
