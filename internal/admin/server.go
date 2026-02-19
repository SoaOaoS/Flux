package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Server is the management dashboard HTTP server.
type Server struct {
	reg       *Registry
	startTime time.Time
	version   string
	srv       *http.Server
}

// New creates a management dashboard Server. Call Start to begin listening.
func New(reg *Registry, listenAddr string, startTime time.Time, version string) *Server {
	s := &Server{
		reg:       reg,
		startTime: startTime,
		version:   version,
	}

	mux := http.NewServeMux()

	// REST API
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/backends", s.handleListBackends)
	mux.HandleFunc("POST /api/backends", s.handleAddBackend)
	mux.HandleFunc("DELETE /api/backends", s.handleRemoveBackend)
	mux.HandleFunc("POST /api/backends/block", s.handleBlock)
	mux.HandleFunc("POST /api/backends/unblock", s.handleUnblock)

	s.srv = &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return s
}

// Start begins listening in a background goroutine. It returns immediately.
func (s *Server) Start() {
	go func() {
		slog.Info("admin dashboard listening", "addr", s.srv.Addr)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin server error", "error", err)
		}
	}()
}

// Stop gracefully shuts down the admin server within the given context deadline.
func (s *Server) Stop(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// ── Handlers ────────────────────────────────────────────────────────────────

type statsResponse struct {
	Uptime          string `json:"uptime"`
	Version         string `json:"version"`
	TotalRequests   int64  `json:"total_requests"`
	TotalErrors     int64  `json:"total_errors"`
	ActiveConns     int64  `json:"active_conns"`
	BackendsTotal   int    `json:"backends_total"`
	BackendsHealthy int    `json:"backends_healthy"`
	BackendsBlocked int    `json:"backends_blocked"`
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	backends := s.reg.List()

	var totalReqs, totalErrs, activeConns int64
	healthy, blocked := 0, 0
	for _, b := range backends {
		totalReqs += b.TotalRequests
		totalErrs += b.TotalErrors
		activeConns += b.ActiveConns
		if b.Healthy && !b.Blocked {
			healthy++
		}
		if b.Blocked {
			blocked++
		}
	}

	jsonOK(w, statsResponse{
		Uptime:          time.Since(s.startTime).Round(time.Second).String(),
		Version:         s.version,
		TotalRequests:   totalReqs,
		TotalErrors:     totalErrs,
		ActiveConns:     activeConns,
		BackendsTotal:   len(backends),
		BackendsHealthy: healthy,
		BackendsBlocked: blocked,
	})
}

func (s *Server) handleListBackends(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, s.reg.List())
}

func (s *Server) handleAddBackend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string `json:"url"`
		Weight int    `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.URL == "" {
		jsonErr(w, "url is required", http.StatusBadRequest)
		return
	}
	if body.Weight <= 0 {
		body.Weight = 1
	}
	if err := s.reg.Add(body.URL, body.Weight); err != nil {
		jsonErr(w, err.Error(), http.StatusConflict)
		return
	}
	slog.Info("admin: backend added", "url", body.URL, "weight", body.Weight)
	jsonOK(w, map[string]string{"status": "added"})
}

func (s *Server) handleRemoveBackend(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		jsonErr(w, "url query parameter is required", http.StatusBadRequest)
		return
	}
	if err := s.reg.Remove(u); err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}
	slog.Info("admin: backend removed", "url", u)
	jsonOK(w, map[string]string{"status": "removed"})
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		jsonErr(w, "url query parameter is required", http.StatusBadRequest)
		return
	}
	if err := s.reg.Block(u); err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}
	slog.Info("admin: backend blocked", "url", u)
	jsonOK(w, map[string]string{"status": "blocked"})
}

func (s *Server) handleUnblock(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		jsonErr(w, "url query parameter is required", http.StatusBadRequest)
		return
	}
	if err := s.reg.Unblock(u); err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}
	slog.Info("admin: backend unblocked", "url", u)
	jsonOK(w, map[string]string{"status": "unblocked"})
}

// ── helpers ─────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}
