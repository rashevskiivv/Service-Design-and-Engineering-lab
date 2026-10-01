package api

import (
	"context"
	"net/http"
	"time"

	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/tasks"
)

const readyCacheFor = 2 * time.Second

// chat serves POST /v1/chat/completions.
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	c := s.newCall(w, r, "chat")
	defer c.finish()
	if !c.begin() {
		return
	}
	body, e := s.readBody(w, r)
	if e != nil {
		c.refuse(e)
		return
	}
	n, e := oai.NormalizeChat(body, s.chatPolicy)
	c.row.Model, c.row.Stream = n.PublicModel, n.Request.Stream
	if e != nil {
		c.refuse(e)
		return
	}
	c.run(n.Request, n.ClientWantsUsage, n.PromptBytes)
}

// task serves POST /v1/tasks/{task}.
func (s *Server) task(w http.ResponseWriter, r *http.Request) {
	k, ok := tasks.ParseKind(r.PathValue("task"))
	if !ok {
		oai.WriteError(w, oai.NewError(http.StatusNotFound, oai.TypeNotFound, oai.CodeUnknownTask, "task",
			"unknown task; one of explain, review, tests, fix"))
		return
	}
	c := s.newCall(w, r, string(k))
	defer c.finish()
	if !c.begin() {
		return
	}
	body, e := s.readBody(w, r)
	if e != nil {
		c.refuse(e)
		return
	}
	req, e := tasks.Decode(body)
	c.row.Stream = req.Stream
	if e == nil {
		e = req.Validate(k, s.cfg.Gen.TaskMaxInputBytes)
	}
	if e != nil {
		c.refuse(e)
		return
	}
	c.row.Language = req.Language
	model, ok := s.cfg.ResolveModel(req.Model)
	if !ok {
		c.refuse(oai.ModelNotFound(req.Model))
		return
	}
	c.row.Model = model.Alias

	requested, _ := req.RequestedMaxTokens() // validated above
	d := tasks.DefaultsFor(k)
	temp := d.Temperature
	if req.Temperature != nil {
		temp = *req.Temperature
	}
	msgs := tasks.Messages(k, req)
	cr := oai.ChatRequest{
		Model:       model.Upstream,
		Messages:    msgs,
		Stream:      req.Stream,
		MaxTokens:   s.tokens.Clamp(requested, d.MaxTokens, req.Stream),
		Temperature: &temp,
	}
	wantsUsage := false
	if req.Stream {
		cr.StreamOptions = &oai.StreamOptions{IncludeUsage: true}
		wantsUsage = req.StreamOptions != nil && req.StreamOptions.IncludeUsage
	}
	model.Defaults.Apply(&cr)
	promptBytes := 0
	for _, m := range msgs {
		promptBytes += len(*m.Content)
	}
	c.run(cr, wantsUsage, promptBytes)
}

type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// models serves GET /v1/models from config; it is not rate limited or recorded.
func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	data := make([]modelEntry, len(s.cfg.Models))
	for i, m := range s.cfg.Models {
		data[i] = modelEntry{ID: m.Alias, Object: "model", Created: s.started.Unix(), OwnedBy: "local-generative-ai"}
	}
	oai.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// healthz reports that the process is alive; it never calls the upstream.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	oai.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz reports whether the upstream and the database answer. The probe
// result is cached for 2 s and computed by one caller at a time, so the
// unauthenticated endpoint cannot amplify load on the upstream; the answer is
// generic (no hosts or ports; security #24).
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if !s.probeReady(r.Context()) {
		e := oai.NewError(http.StatusServiceUnavailable, oai.TypeServer, oai.CodeUpstreamUnavailable, "", "not ready")
		e.RetryAfter = s.cfg.Limits.OverloadRetryAfter
		oai.WriteError(w, e)
		return
	}
	oai.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready", "upstream": "ok", "db": "ok"})
}

func (s *Server) probeReady(ctx context.Context) bool {
	s.ready.mu.Lock()
	defer s.ready.mu.Unlock()
	if !s.ready.at.IsZero() && time.Since(s.ready.at) < readyCacheFor {
		return s.ready.ok
	}
	ctx = context.WithoutCancel(ctx) // a client hanging up must not poison the cache
	err := s.up.Ready(ctx)
	if err == nil {
		ctx, cancel := context.WithTimeout(ctx, readyCacheFor)
		err = s.store.Ping(ctx)
		cancel()
	}
	if err != nil {
		s.log.Warn("readiness probe failed", "error", err)
	}
	s.ready.ok, s.ready.at = err == nil, time.Now()
	return s.ready.ok
}
