// Package api is the HTTP surface: the public and admin routers, middleware,
// the generation pipeline shared by chat and tasks, and the handlers.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"local-generative-ai/internal/auth"
	"local-generative-ai/internal/config"
	"local-generative-ai/internal/limits"
	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/store"
	"local-generative-ai/internal/upstream"
)

// Deps are the Server's collaborators.
type Deps struct {
	Cfg      config.Config
	Store    *store.Store
	Upstream *upstream.Client
	Log      *slog.Logger
	Now      func() time.Time // nil → time.Now
}

// Every refusal of a key's generation request by the gateway itself (invalid
// body, a per-key limit, overload) spends a token of a per-key bucket. When
// it is empty, the key's requests are refused right after authentication,
// before the body is read (security-code V2). A student who fixes the request
// waits at most a few seconds; a script looping on errors is held to 30/min.
const (
	rejectRPM   = 30
	rejectBurst = 10
)

// Server holds the shared state of both listeners.
type Server struct {
	cfg     config.Config
	store   *store.Store
	up      *upstream.Client
	log     *slog.Logger
	now     func() time.Time
	started time.Time

	rate    *limits.RateLimiter
	rejects *limits.RateLimiter // refused requests per key (see rejectRPM)
	perKey  *limits.KeyInflight
	gate    *limits.Gate
	quota   *limits.Quota

	chatPolicy  oai.ChatPolicy
	tokens      oai.TokenLimits
	maintenance atomic.Bool // in memory only: off after every restart
	ready       readiness
	calls       atomic.Int64 // generation calls whose usage row is not written yet

	authRoutes []string // patterns registered behind RequireKey (for tests)
}

type readiness struct {
	mu sync.Mutex
	at time.Time
	ok bool
}

// New builds a Server from d.
func New(d Deps) *Server {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	c := d.Cfg
	s := &Server{
		cfg: c, store: d.Store, up: d.Upstream, log: d.Log, now: now, started: now(),
		rate:    limits.NewRateLimiter(now),
		rejects: limits.NewRateLimiter(now),
		perKey:  limits.NewKeyInflight(),
		gate:    limits.NewGate(c.Limits.MaxInflight, c.Limits.QueueSize, c.Limits.QueueTimeout),
		quota:   limits.NewQuota(d.Store, now),
		tokens:  oai.TokenLimits{Cap: c.Gen.MaxTokensCap, NonStreamCap: c.Gen.NonStreamMaxTokens},
	}
	s.chatPolicy = oai.ChatPolicy{
		Resolve:          c.ResolveModel,
		DefaultMaxTokens: c.Gen.ChatDefaultMaxTokens,
		Tokens:           s.tokens,
		MaxInputBytes:    c.Gen.ChatMaxInputBytes,
	}
	return s
}

// WaitCalls waits until every generation call has written its usage row, or
// until ctx ends. main calls it after the listeners shut down and before the
// database closes, so force-closed streams still get their rows (review m2).
func (s *Server) WaitCalls(ctx context.Context) error {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for s.calls.Load() > 0 {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%d generation calls still running: %w", s.calls.Load(), ctx.Err())
		case <-tick.C:
		}
	}
	return nil
}

// PublicHandler serves the public listener. Every route except /healthz and
// /readyz is registered through RequireKey (fail-closed). /admin is mounted
// only with LGAI_ADMIN_ON_PUBLIC=true; otherwise it is an unknown route (404).
func (s *Server) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)

	requireKey := auth.RequireKey(s.store, s.cfg.Limits, s.now, s.log)
	authed := func(pattern string, h http.HandlerFunc) {
		s.authRoutes = append(s.authRoutes, pattern)
		mux.Handle(pattern, requireKey(h))
	}
	authed("GET /v1/models", s.models)
	authed("POST /v1/chat/completions", s.chat)
	authed("POST /v1/tasks/{task}", s.task)

	if s.cfg.AdminOnPublic && s.cfg.AdminEnabled() {
		mux.Handle("/admin/", s.adminRoutes())
	}
	return s.middleware(jsonFallback(mux))
}

// AdminHandler serves the admin listener; the whole mux sits behind
// RequireAdmin (fail-closed).
func (s *Server) AdminHandler() http.Handler {
	return s.middleware(s.adminRoutes())
}

func (s *Server) adminRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/keys", s.createKey)
	mux.HandleFunc("GET /admin/keys", s.listKeys)
	mux.HandleFunc("POST /admin/keys/batch", s.createKeyBatch)
	mux.HandleFunc("POST /admin/keys/revoke", s.revokeKeys)
	mux.HandleFunc("GET /admin/keys/{id}", s.getKey)
	mux.HandleFunc("DELETE /admin/keys/{id}", s.revokeKey)
	mux.HandleFunc("GET /admin/keys/{id}/usage", s.keyUsage)
	mux.HandleFunc("GET /admin/maintenance", s.getMaintenance)
	mux.HandleFunc("PUT /admin/maintenance", s.setMaintenance)
	return auth.RequireAdmin(s.cfg.AdminToken)(jsonFallback(mux))
}

// jsonFallback turns the mux's plain-text 404 and 405 into JSON errors (the 405
// keeps its Allow header). No CORS: OPTIONS is just another 405.
func jsonFallback(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		probe := &headerProbe{h: http.Header{}}
		h.ServeHTTP(probe, r)
		if probe.status == http.StatusMethodNotAllowed {
			w.Header().Set("Allow", probe.h.Get("Allow"))
			oai.WriteError(w, oai.NewError(http.StatusMethodNotAllowed, oai.TypeInvalidRequest,
				oai.CodeMethodNotAllowed, "", "method not allowed; allowed: "+probe.h.Get("Allow")))
			return
		}
		oai.WriteError(w, oai.NewError(http.StatusNotFound, oai.TypeNotFound, oai.CodeNotFound, "", "no such route"))
	})
}

// headerProbe records what the mux's built-in 404/405 handler would send.
type headerProbe struct {
	h      http.Header
	status int
}

func (p *headerProbe) Header() http.Header         { return p.h }
func (p *headerProbe) Write(b []byte) (int, error) { return len(b), nil }
func (p *headerProbe) WriteHeader(code int)        { p.status = code }
