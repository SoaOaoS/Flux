// Command gateway is the Flux API Gateway entry point.
//
// Usage:
//
//	gateway [-config path/to/gateway.yaml]
//
// The gateway supports zero-downtime hot-reload: edit gateway.yaml while the
// process is running and changes take effect immediately — no restart needed.
// Shutdown is graceful: send SIGINT or SIGTERM and in-flight requests are
// given up to 10 seconds to complete.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"golb/internal/config"
	"golb/internal/health"
	"golb/internal/middleware"
	"golb/internal/proxy"
	"golb/internal/strategy"
)

// Version information — set at build time via -ldflags.
//
//	-X main.version=$(git describe --tags --always)
//	-X main.commit=$(git rev-parse --short HEAD)
//	-X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/gateway.yaml", "path to gateway.yaml")
	flag.Parse()

	startTime := time.Now()

	// Structured JSON logging to stdout — ready for any log aggregator.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	// ── Load initial configuration ────────────────────────────────────────────
	cfg, v, err := config.Load(*configPath)
	if err != nil {
		slog.Warn("could not load config file, using defaults",
			"path", *configPath,
			"error", err,
		)
		cfg = config.Default()
		v = nil
	}

	// ── Build runtime objects ─────────────────────────────────────────────────
	gw, monitor, err := buildGateway(cfg)
	if err != nil {
		slog.Error("failed to initialise gateway", "error", err)
		os.Exit(1)
	}

	if cfg.HealthCheck.Enabled {
		monitor.Start()
	}

	// ── Build middleware chain ────────────────────────────────────────────────
	// The atomicHandler lets us swap the entire chain at runtime (hot-reload
	// of rate-limit or auth settings) without restarting the server.
	var current atomic.Value
	buildChain := func(c config.Config) http.Handler {
		var h http.Handler = gw
		if c.Auth.Enabled {
			h = middleware.JWTAuth(c.Auth.Secret, c.Auth.Exclude)(h)
		}
		if c.RateLimit.Enabled {
			h = middleware.RateLimiter(c.RateLimit.RPS, c.RateLimit.Burst)(h)
		}
		return middleware.Logger(h)
	}
	current.Store(buildChain(cfg))

	atomicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current.Load().(http.Handler).ServeHTTP(w, r)
	})

	// ── Hot-reload ────────────────────────────────────────────────────────────
	if v != nil {
		config.Watch(v, func(newCfg config.Config) {
			newBackends, err := strategy.NewBackends(newCfg.Backends)
			if err != nil {
				slog.Error("hot-reload: invalid backends", "error", err)
				return
			}
			newPicker, err := strategy.New(newCfg.Strategy, newBackends)
			if err != nil {
				slog.Error("hot-reload: failed to rebuild picker", "error", err)
				return
			}
			gw.UpdatePicker(newPicker)
			monitor.UpdateBackends(newBackends)
			current.Store(buildChain(newCfg))

			slog.Info("hot-reload applied",
				"backends", len(newCfg.Backends),
				"strategy", newCfg.Strategy,
				"rate_limit", newCfg.RateLimit.Enabled,
				"auth", newCfg.Auth.Enabled,
			)
		})
	}

	// ── Top-level mux ─────────────────────────────────────────────────────────
	// /healthz is answered locally (no middleware, no backend) so Docker and
	// load-balancers can always determine whether the process is alive.
	// All other paths go through the middleware chain and onto the proxy.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","version":%q,"commit":%q,"build_date":%q,"uptime":%q}`,
			version, commit, buildDate, time.Since(startTime).Round(time.Second).String())
	})
	mux.Handle("/", atomicHandler)

	// ── HTTP server ───────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("gateway listening",
			"addr", cfg.ListenAddr,
			"strategy", cfg.Strategy,
			"backends", len(cfg.Backends),
			"health_check", cfg.HealthCheck.Enabled,
			"rate_limit", cfg.RateLimit.Enabled,
			"auth", cfg.Auth.Enabled,
			"version", version,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down gateway")

	monitor.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("gateway stopped")
}

// buildGateway constructs the Gateway and its associated health Monitor from
// the given Config.
func buildGateway(cfg config.Config) (*proxy.Gateway, *health.Monitor, error) {
	backends, err := strategy.NewBackends(cfg.Backends)
	if err != nil {
		return nil, nil, err
	}

	picker, err := strategy.New(cfg.Strategy, backends)
	if err != nil {
		return nil, nil, err
	}

	gw := proxy.New(picker)

	mon := health.New(backends, health.Config{
		Interval: cfg.HealthCheck.ParsedInterval(),
		Timeout:  cfg.HealthCheck.ParsedTimeout(),
		Path:     cfg.HealthCheck.Path,
	})

	return gw, mon, nil
}
