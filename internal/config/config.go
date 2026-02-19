// Package config handles loading and hot-reloading of the gateway YAML
// configuration via Viper. All struct fields map 1-to-1 with gateway.yaml.
package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// BackendCfg is the YAML representation of a single upstream server.
type BackendCfg struct {
	URL    string `mapstructure:"url"`
	Weight int    `mapstructure:"weight"`
}

// HealthCheckCfg controls active health probing.
type HealthCheckCfg struct {
	Enabled  bool   `mapstructure:"enabled"`
	Interval string `mapstructure:"interval"`
	Timeout  string `mapstructure:"timeout"`
	Path     string `mapstructure:"path"`
}

// ParsedInterval returns the interval as a time.Duration, defaulting to 10s.
func (h HealthCheckCfg) ParsedInterval() time.Duration {
	d, _ := time.ParseDuration(h.Interval)
	if d <= 0 {
		return 10 * time.Second
	}
	return d
}

// ParsedTimeout returns the timeout as a time.Duration, defaulting to 2s.
func (h HealthCheckCfg) ParsedTimeout() time.Duration {
	d, _ := time.ParseDuration(h.Timeout)
	if d <= 0 {
		return 2 * time.Second
	}
	return d
}

// RateLimitCfg controls per-IP token-bucket rate limiting.
type RateLimitCfg struct {
	Enabled bool    `mapstructure:"enabled"`
	RPS     float64 `mapstructure:"rps"`   // sustained requests per second
	Burst   int     `mapstructure:"burst"` // maximum burst size
}

// AuthCfg controls JWT Bearer-token authentication.
type AuthCfg struct {
	Enabled bool     `mapstructure:"enabled"`
	Secret  string   `mapstructure:"secret"`  // HMAC-SHA256 signing secret
	Exclude []string `mapstructure:"exclude"` // exact paths that bypass auth
}

// AdminCfg controls the management dashboard HTTP server.
type AdminCfg struct {
	Enabled    bool   `mapstructure:"enabled"`
	ListenAddr string `mapstructure:"listen_addr"`
}

// Config is the top-level gateway configuration.
type Config struct {
	ListenAddr  string         `mapstructure:"listen_addr"`
	Strategy    string         `mapstructure:"strategy"` // round_robin | weighted_round_robin | least_connections
	Backends    []BackendCfg   `mapstructure:"backends"`
	HealthCheck HealthCheckCfg `mapstructure:"health_check"`
	RateLimit   RateLimitCfg   `mapstructure:"rate_limit"`
	Auth        AuthCfg        `mapstructure:"auth"`
	Admin       AdminCfg       `mapstructure:"admin"`
}

// Default returns a sensible single-backend config for development / Phase 1.
func Default() Config {
	return Config{
		ListenAddr: ":8080",
		Strategy:   "round_robin",
		Backends:   []BackendCfg{{URL: "http://localhost:8081", Weight: 1}},
		HealthCheck: HealthCheckCfg{
			Enabled:  true,
			Interval: "10s",
			Timeout:  "2s",
			Path:     "/healthz",
		},
		RateLimit: RateLimitCfg{Enabled: false, RPS: 100, Burst: 200},
		Auth:      AuthCfg{Enabled: false},
	}
}

// Load reads and parses the YAML file at path using Viper.
// It returns the parsed Config and the Viper instance (needed for Watch).
func Load(path string) (Config, *viper.Viper, error) {
	v := newViper(path)
	if err := v.ReadInConfig(); err != nil {
		return Config{}, nil, fmt.Errorf("config: reading %q: %w", path, err)
	}
	cfg, err := unmarshal(v)
	if err != nil {
		return Config{}, nil, err
	}
	return cfg, v, nil
}

// Watch registers an onChange callback that fires whenever the config file is
// saved. The callback receives a freshly parsed Config. Invalid reloads are
// logged and silently skipped (the previous config stays active).
func Watch(v *viper.Viper, onChange func(Config)) {
	v.WatchConfig()
	v.OnConfigChange(func(_ fsnotify.Event) {
		cfg, err := unmarshal(v)
		if err != nil {
			slog.Error("config hot-reload failed", "error", err)
			return
		}
		slog.Info("config hot-reloaded",
			"backends", len(cfg.Backends),
			"strategy", cfg.Strategy,
		)
		onChange(cfg)
	})
}

func newViper(path string) *viper.Viper {
	v := viper.New()
	v.SetConfigFile(path)

	// Defaults — all overridable by gateway.yaml.
	v.SetDefault("listen_addr", ":8080")
	v.SetDefault("strategy", "round_robin")
	v.SetDefault("health_check.enabled", true)
	v.SetDefault("health_check.interval", "10s")
	v.SetDefault("health_check.timeout", "2s")
	v.SetDefault("health_check.path", "/healthz")
	v.SetDefault("rate_limit.enabled", false)
	v.SetDefault("rate_limit.rps", 100.0)
	v.SetDefault("rate_limit.burst", 200)
	v.SetDefault("auth.enabled", false)
	v.SetDefault("admin.enabled", true)
	v.SetDefault("admin.listen_addr", ":9091")

	return v
}

func unmarshal(v *viper.Viper) (Config, error) {
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: parsing: %w", err)
	}
	if len(cfg.Backends) == 0 {
		return Config{}, fmt.Errorf("config: at least one backend must be defined")
	}
	for i, b := range cfg.Backends {
		if b.URL == "" {
			return Config{}, fmt.Errorf("config: backend[%d] has empty url", i)
		}
		if b.Weight <= 0 {
			cfg.Backends[i].Weight = 1
		}
	}
	return cfg, nil
}
