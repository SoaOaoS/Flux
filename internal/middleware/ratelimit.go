package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter returns a per-IP token-bucket rate-limiting middleware.
//
//   - rps   — sustained allowed requests per second per IP.
//   - burst — maximum instantaneous burst above the sustained rate.
//
// The client IP is resolved in order: X-Real-IP header (set by the gateway's
// director), then the TCP remote address. Stale limiter entries are purged
// every 5 minutes to prevent unbounded memory growth.
func RateLimiter(rps float64, burst int) func(http.Handler) http.Handler {
	var mu sync.Mutex
	entries := make(map[string]*ipEntry)

	// Background cleanup goroutine — removes entries idle for >10 minutes.
	go func() {
		for range time.Tick(5 * time.Minute) {
			mu.Lock()
			for ip, e := range entries {
				if time.Since(e.lastSeen) > 10*time.Minute {
					delete(entries, ip)
				}
			}
			mu.Unlock()
		}
	}()

	getLimiter := func(ip string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		e, ok := entries[ip]
		if !ok {
			e = &ipEntry{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
			entries[ip] = e
		}
		e.lastSeen = time.Now()
		return e.limiter
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !getLimiter(ip).Allow() {
				slog.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the real client IP, preferring the X-Real-IP header
// injected by the gateway's director over the raw TCP remote address.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
