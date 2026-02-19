package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"golb/internal/middleware"
)

// ── Logger ───────────────────────────────────────────────────────────────────

func TestLogger_AddsRequestID(t *testing.T) {
	var capturedReqID string

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReqID = r.Header.Get("X-Request-Id")
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.Logger(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	handler.ServeHTTP(rec, req)

	assert.NotEmpty(t, capturedReqID, "Logger must set X-Request-Id on the inbound request")
	assert.Equal(t, capturedReqID, rec.Header().Get("X-Request-Id"),
		"X-Request-Id in response must match the one injected into the request")
}

func TestLogger_CapturesDownstreamStatus(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	handler := middleware.Logger(inner)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/items", nil))

	// The recorder captures the status written by the downstream handler.
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestLogger_UniqueRequestIDs(t *testing.T) {
	ids := map[string]struct{}{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids[r.Header.Get("X-Request-Id")] = struct{}{}
	})
	handler := middleware.Logger(inner)

	for i := 0; i < 50; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	assert.Len(t, ids, 50, "every request should receive a unique X-Request-Id")
}

// ── RateLimiter ──────────────────────────────────────────────────────────────

func TestRateLimiter_AllowsBurst(t *testing.T) {
	// rps=0.001 (negligible) ensures only the burst token pool is used.
	handler := middleware.RateLimiter(0.001, 3)(ok200())

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newReq("192.168.1.1:1234"))
		assert.Equal(t, http.StatusOK, rec.Code, "request %d within burst should pass", i+1)
	}
}

func TestRateLimiter_BlocksAfterBurst(t *testing.T) {
	handler := middleware.RateLimiter(0.001, 3)(ok200())

	// Exhaust the burst.
	for i := 0; i < 3; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), newReq("10.0.0.1:9999"))
	}

	// The 4th request must be rejected.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newReq("10.0.0.1:9999"))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "4th request must be rate-limited")
}

func TestRateLimiter_IndependentPerIP(t *testing.T) {
	handler := middleware.RateLimiter(0.001, 2)(ok200())

	// Exhaust the quota for IP A.
	for i := 0; i < 2; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), newReq("1.2.3.4:1111"))
	}

	// IP B should still have its own full burst available.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newReq("5.6.7.8:2222"))
	assert.Equal(t, http.StatusOK, rec.Code, "a different IP must have its own bucket")
}

// ── JWTAuth ──────────────────────────────────────────────────────────────────

const testSecret = "test-signing-secret-256bits-long!"

func TestJWTAuth_MissingToken_Returns401(t *testing.T) {
	handler := middleware.JWTAuth(testSecret, nil)(ok200())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestJWTAuth_InvalidToken_Returns401(t *testing.T) {
	handler := middleware.JWTAuth(testSecret, nil)(ok200())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.token")
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestJWTAuth_WrongSecret_Returns401(t *testing.T) {
	handler := middleware.JWTAuth(testSecret, nil)(ok200())

	token := signedToken(t, "different-secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestJWTAuth_ValidToken_Passes(t *testing.T) {
	handler := middleware.JWTAuth(testSecret, nil)(ok200())

	token := signedToken(t, testSecret)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestJWTAuth_ExcludedPath_NoTokenNeeded(t *testing.T) {
	handler := middleware.JWTAuth(testSecret, []string{"/healthz", "/public"})(ok200())

	for _, path := range []string{"/healthz", "/public"} {
		rec := httptest.NewRecorder()
		// No Authorization header — but path is excluded.
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, "excluded path %s must pass without token", path)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func ok200() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func newReq(remoteAddr string) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remoteAddr
	return req
}

func signedToken(t *testing.T, secret string) string {
	t.Helper()
	tok := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, jwtlib.MapClaims{
		"sub": "test-user",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	s, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}
