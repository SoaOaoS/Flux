package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// JWTAuth returns a middleware that enforces Bearer JWT authentication using
// HMAC-SHA256 (HS256). Tokens must be present in the Authorization header as
// "Bearer <token>".
//
//   - secret  — the shared HMAC signing secret.
//   - exclude — exact URL paths that bypass authentication (e.g. "/healthz").
//
// Returns 401 Unauthorized when the header is missing or the token is invalid.
//
// ⚠  In production the secret should come from an environment variable or a
// secrets manager, not from the config file on disk.
func JWTAuth(secret string, exclude []string) func(http.Handler) http.Handler {
	key := []byte(secret)

	excludeSet := make(map[string]struct{}, len(exclude))
	for _, p := range exclude {
		excludeSet[p] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow excluded paths through without any token check.
			if _, ok := excludeSet[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				slog.Warn("auth: missing or malformed Authorization header",
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
				)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
				// Reject any algorithm that is not HMAC to prevent the "alg:none" attack.
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return key, nil
			})

			if err != nil || !token.Valid {
				slog.Warn("auth: invalid JWT",
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
					"error", err,
				)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
