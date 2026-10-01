// Command gateway is the local-generative-ai API gateway: an OpenAI-compatible
// front for one self-hosted model server, with per-person keys, limits, a
// bounded queue, coding-task prompts and content-free usage stats.
//
// Usage:
//
//	gateway                 run the server (configured by LGAI_* env vars)
//	gateway healthcheck     GET /healthz on the local listener (for Docker)
//	gateway gen-secret      print a random secret for LGAI_ADMIN_TOKEN
//	gateway gen-keys ...    mint keys offline for LGAI_KEYS_FILE
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"syscall"
	"time"

	"local-generative-ai/internal/api"
	"local-generative-ai/internal/config"
	"local-generative-ai/internal/store"
	"local-generative-ai/internal/upstream"
)

// usageFlushTimeout bounds the wait for usage rows after a forced shutdown;
// each insert has its own 2 s timeout.
const usageFlushTimeout = 5 * time.Second

func main() {
	if len(os.Args) > 1 {
		os.Exit(subcommand(os.Args[1], os.Args[2:]))
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gateway:", err)
		os.Exit(1)
	}
}

func subcommand(name string, args []string) int {
	switch name {
	case "healthcheck":
		return healthcheck(os.Getenv("LGAI_ADDR"))
	case "gen-secret":
		return genSecret(os.Stdout)
	case "gen-keys":
		return genKeys(args, os.Stderr)
	case "help", "-h", "--help":
		fmt.Println("usage: gateway [healthcheck | gen-secret | gen-keys -n N -prefix P [-expires T] [-out F] [-hashes F]]")
		return 0
	}
	fmt.Fprintf(os.Stderr, "gateway: unknown command %q (try: gateway help)\n", name)
	return 2
}

func run() error {
	setUmask() // DB, -wal, -shm and key files are 0600 (security #21)
	cfg, warnings, err := config.Load(os.Getenv, os.Environ())
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	for _, w := range warnings {
		log.Warn(w)
	}
	// Maintenance mode lives in memory, so it is always off after a restart.
	log.Info("starting", "version", version(), "maintenance", false, "config", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing database", "err", err)
		}
	}()
	if cfg.KeysFile != "" {
		if err := importKeys(ctx, st, cfg.KeysFile, log); err != nil {
			return err
		}
	}

	up := upstream.New(cfg.Upstream, cfg.Limits.MaxInflight, cfg.Limits.OverloadRetryAfter)
	checkModels(ctx, up, cfg, log)
	srv := api.New(api.Deps{Cfg: cfg, Store: st, Upstream: up, Log: log})

	type listener struct {
		name string
		srv  *http.Server
		ln   net.Listener
	}
	var listeners []listener
	add := func(name, addr string, h http.Handler) error {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("%s listener: %w", name, err)
		}
		listeners = append(listeners, listener{name, newServer(cfg, h, log), limitListen(ln, cfg.HTTP.MaxConns)})
		log.Info("listening", "listener", name, "addr", ln.Addr().String())
		return nil
	}
	if err := add("public", cfg.Addr, srv.PublicHandler()); err != nil {
		return err
	}
	switch {
	case !cfg.AdminEnabled():
		log.Info("admin API disabled: LGAI_ADMIN_TOKEN is not set")
	case cfg.AdminOnPublic:
		log.Warn("admin API is mounted on the PUBLIC listener (LGAI_ADMIN_ON_PUBLIC=true); protect the admin token")
	default:
		if err := add("admin", cfg.AdminAddr, srv.AdminHandler()); err != nil {
			return err
		}
	}

	errs := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() {
			if err := l.srv.Serve(l.ln); !errors.Is(err, http.ErrServerClosed) {
				errs <- fmt.Errorf("%s listener: %w", l.name, err)
			}
		}()
	}
	var serveErr error
	select {
	case <-ctx.Done():
		log.Info("shutting down", "timeout", cfg.HTTP.ShutdownTimeout.String())
	case serveErr = <-errs:
		log.Error("server failed; shutting down", "err", serveErr)
	}

	// DECISIONS D4: Shutdown both listeners (in-flight streams may finish),
	// force-close on timeout, then the deferred db.Close.
	shCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	for _, l := range slices.Backward(listeners) {
		if err := l.srv.Shutdown(shCtx); err != nil {
			log.Warn("forcing connections closed", "listener", l.name, "err", err)
			_ = l.srv.Close()
		}
	}
	// Force-closed streams unwind after Close returns: let them write their
	// usage rows before the database closes (review m2).
	waitCtx, cancelWait := context.WithTimeout(context.Background(), usageFlushTimeout)
	defer cancelWait()
	if err := srv.WaitCalls(waitCtx); err != nil {
		log.Warn("usage rows of some requests may be lost", "err", err)
	}
	log.Info("stopped")
	return serveErr
}

// newServer builds an http.Server. ReadTimeout and WriteTimeout stay unset on
// purpose: either would cut long streams (ARCHITECTURE §5.4). Body reads and
// client writes get per-request deadlines instead.
func newServer(cfg config.Config, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

func importKeys(ctx context.Context, st *store.Store, path string, log *slog.Logger) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("LGAI_KEYS_FILE: %w", err)
	}
	defer f.Close()
	now := time.Now()
	keys, expired, err := store.ParseKeysFile(f, now)
	if err != nil {
		return fmt.Errorf("LGAI_KEYS_FILE: %w", err)
	}
	added, err := st.ImportKeys(ctx, keys, now)
	if err != nil {
		return fmt.Errorf("LGAI_KEYS_FILE: %w", err)
	}
	log.Info("keys file imported", "keys", len(keys), "added", added, "expired_skipped", expired)
	return nil
}

// checkModels warns when an alias points at a model the upstream does not
// list, e.g. one that was never pulled. It never blocks startup.
func checkModels(ctx context.Context, up *upstream.Client, cfg config.Config, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ids, err := up.ModelIDs(ctx)
	if err != nil {
		log.Warn("upstream model list unavailable at startup; continuing", "err", err)
		return
	}
	for _, m := range cfg.Models {
		if !slices.Contains(ids, m.Upstream) {
			log.Warn("model alias points at a model the upstream does not list", "alias", m.Alias, "upstream_model", m.Upstream)
		}
	}
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return "dev"
}
