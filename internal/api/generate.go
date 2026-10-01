package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"local-generative-ai/internal/auth"
	"local-generative-ai/internal/limits"
	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/relay"
	"local-generative-ai/internal/store"
)

const usageWriteTimeout = 2 * time.Second

// call is one authenticated generation request. It holds the key's in-flight
// slot from before the body is read until finish, and owns the usage row.
type call struct {
	s          *Server
	w          http.ResponseWriter
	r          *http.Request
	p          auth.Principal
	start      time.Time
	row        store.UsageRow
	releaseKey func() // the per-key in-flight slot; nil until begin takes it
	record     bool   // write a usage row: only for requests that passed validation
}

// newCall starts a generation call; the caller must defer finish. The row
// starts as a 500 so a panic is never recorded as a success (review m6);
// every normal path overwrites it.
func (s *Server) newCall(w http.ResponseWriter, r *http.Request, endpoint string) *call {
	s.calls.Add(1)
	p, _ := auth.PrincipalFrom(r.Context())
	now := s.now()
	return &call{s: s, w: w, r: r, p: p, start: now, row: store.UsageRow{
		TS: now, RequestID: requestIDFrom(r.Context()), KeyID: p.KeyID, Endpoint: endpoint,
		Status: http.StatusInternalServerError, ErrorCode: oai.CodeInternal,
	}}
}

// begin runs the checks that need no body, right after authentication:
// maintenance, the key's rejected-request bucket, and the key's in-flight slot.
// Taking the slot before the body is read caps the slow body reads one key can
// hold open at LGAI_KEY_MAX_INFLIGHT (security-code V1). On false the answer
// has been sent; finish must still run.
func (c *call) begin() bool {
	s, p := c.s, c.p
	if s.maintenance.Load() {
		c.fail(s.maintenanceError())
		return false
	}
	if ok, wait := s.rejects.Peek(p.KeyID, rejectRPM, rejectBurst); !ok {
		e := oai.NewError(http.StatusTooManyRequests, oai.TypeRateLimit, oai.CodeRateLimit, "",
			fmt.Sprintf("too many refused requests from this key; check your requests and retry in %ds", oai.RetryAfterSeconds(wait)))
		e.RetryAfter = wait
		c.fail(e)
		return false
	}
	release, ok := s.perKey.TryAcquire(p.KeyID, p.MaxInflight)
	if !ok {
		e := oai.NewError(http.StatusTooManyRequests, oai.TypeRateLimit, oai.CodeConcurrency, "",
			fmt.Sprintf("at most %d concurrent requests per key", p.MaxInflight))
		e.RetryAfter = time.Second
		c.refuse(e)
		return false
	}
	c.releaseKey = release
	return true
}

// fail sends e (unless the client is already gone) and records it as the
// outcome.
func (c *call) fail(e *oai.Error) {
	if e.Status != oai.StatusClientClosed {
		oai.WriteError(c.w, e)
	}
	c.row.Status, c.row.ErrorCode = e.Status, e.Code
	if e.Status == oai.StatusClientClosed {
		c.row.ErrorCode = ""
	}
}

// refuse is fail for the gateway's own refusals (invalid request, a per-key
// limit, overload). Each spends a token of the key's rejected-request bucket,
// so one key cannot flood the gateway with requests it refuses
// (security-code V2).
func (c *call) refuse(e *oai.Error) {
	c.s.rejects.Allow(c.p.KeyID, rejectRPM, rejectBurst)
	c.fail(e)
}

// finish releases the key's slot, fills the access-log line and, for a request
// that passed validation, records the usage row with a context that survives
// the client going away. Refusals before that are only logged
// (security-code V2), as 401s are.
func (c *call) finish() {
	defer c.s.calls.Add(-1)
	if c.releaseKey != nil {
		c.releaseKey()
	}
	c.row.Latency = c.s.now().Sub(c.start)
	info := infoFrom(c.r)
	info.hasGeneration = true
	info.keyID, info.endpoint, info.model, info.lang = c.row.KeyID, c.row.Endpoint, c.row.Model, c.row.Language
	info.stream, info.status, info.errCode = c.row.Stream, c.row.Status, c.row.ErrorCode
	info.promptTok, info.complTok, info.estimated = c.row.PromptTokens, c.row.CompletionTokens, c.row.Estimated
	if c.row.Queue != nil {
		info.queueMS = c.row.Queue.Milliseconds()
	}
	if c.row.TTFT != nil {
		info.ttftMS = c.row.TTFT.Milliseconds()
	}
	if !c.record {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.r.Context()), usageWriteTimeout)
	defer cancel()
	if err := c.s.store.InsertUsage(ctx, c.row); err != nil {
		c.s.log.Error("usage insert failed", "request_id", c.row.RequestID, "error", err)
	}
}

func (s *Server) maintenanceError() *oai.Error {
	e := oai.NewError(http.StatusServiceUnavailable, oai.TypeServer, oai.CodeMaintenance, "",
		"the service is in maintenance; retry later")
	e.RetryAfter = s.cfg.Limits.OverloadRetryAfter
	return e
}

// admit applies the per-key rate limit, the daily quota and the global gate,
// in that order (the key's in-flight slot is already held, see begin). The
// rate check comes first so a rate-limited request never touches SQLite
// (security-code V2). The rate token is refunded when the gate refuses or the
// client leaves, so the openai SDK's automatic retries cannot drain the bucket
// (review M1); a quota 429 keeps it, since the SDK does not retry that. On
// success it returns the gate slot's release func.
func (c *call) admit() (release func(), ok bool) {
	s, p, ctx := c.s, c.p, c.r.Context()
	if allowed, wait := s.rate.Allow(p.KeyID, p.RPM, p.Burst); !allowed {
		e := oai.NewError(http.StatusTooManyRequests, oai.TypeRateLimit, oai.CodeRateLimit, "",
			fmt.Sprintf("rate limit of %d requests/min exceeded; retry in %ds", p.RPM, oai.RetryAfterSeconds(wait)))
		e.RetryAfter = wait
		c.refuse(e)
		return nil, false
	}
	refund := func() { s.rate.Refund(p.KeyID, p.RPM, p.Burst) }
	if wait, err := s.quota.Check(ctx, p.KeyID, p.DailyQuota); err != nil {
		switch {
		case errors.Is(err, limits.ErrQuotaExceeded):
			e := oai.NewError(http.StatusTooManyRequests, oai.TypeQuota, oai.CodeQuota, "",
				fmt.Sprintf("daily token quota of %d exhausted; resets at 00:00 UTC", p.DailyQuota))
			e.RetryAfter, e.NoRetry = wait, true
			c.refuse(e)
		case ctx.Err() != nil:
			refund()
			c.fail(clientClosed())
		default:
			refund()
			s.log.Error("quota check failed", "request_id", c.row.RequestID, "error", err)
			c.fail(oai.Internal())
		}
		return nil, false
	}
	info := infoFrom(c.r)
	info.inflight, info.queued = s.gate.Stats()
	releaseSlot, waited, err := s.gate.Acquire(ctx)
	c.row.Queue = &waited
	if err != nil {
		refund()
		if errors.Is(err, limits.ErrQueueFull) || errors.Is(err, limits.ErrQueueTimeout) {
			e := oai.NewError(http.StatusServiceUnavailable, oai.TypeServer, oai.CodeOverloaded, "",
				"server busy: "+err.Error()+"; retry later")
			e.RetryAfter = s.cfg.Limits.OverloadRetryAfter
			c.refuse(e)
		} else {
			c.fail(clientClosed())
		}
		return nil, false
	}
	return releaseSlot, true
}

func clientClosed() *oai.Error {
	return oai.NewError(oai.StatusClientClosed, oai.TypeServer, "", "", "client closed request")
}

// run admits a validated request, sends it upstream and relays the answer.
// From here on the outcome is recorded in a usage row.
func (c *call) run(req oai.ChatRequest, clientWantsUsage bool, promptBytes int) {
	c.record = true
	release, ok := c.admit()
	if !ok {
		return
	}
	defer release()
	s := c.s

	body, err := json.Marshal(req)
	if err != nil {
		c.fail(oai.Internal())
		return
	}
	ctx, cancel := context.WithCancelCause(c.r.Context())
	defer cancel(nil)
	ctx, stop := context.WithTimeoutCause(ctx, s.cfg.Upstream.TotalTimeout, relay.ErrUpstreamTimeout)
	defer stop()

	upStart := time.Now()
	resp, e := s.up.ChatCompletions(ctx, body, c.row.RequestID, req.Stream)
	if e != nil {
		if e.Status != oai.StatusClientClosed {
			level := slog.LevelInfo // 4xx: the client's own input
			if e.Status >= http.StatusInternalServerError {
				level = slog.LevelWarn
			}
			s.log.Log(ctx, level, "upstream request failed", "request_id", c.row.RequestID, "status", e.Status,
				"code", e.Code, "upstream_status", e.UpstreamStatus, "upstream_ms", time.Since(upStart).Milliseconds())
		}
		c.fail(e)
		return
	}
	defer resp.Body.Close()

	opts := relay.Options{
		Start: c.start, ClientWantsUsage: clientWantsUsage, PromptBytes: promptBytes,
		IdleTimeout: s.cfg.Upstream.IdleTimeout, WriteTimeout: s.cfg.HTTP.WriteTimeout, Now: s.now,
	}
	var out relay.Outcome
	if req.Stream {
		out = relay.Stream(ctx, cancel, c.w, resp.Body, opts)
	} else {
		out = relay.JSON(ctx, c.w, resp, opts)
	}
	c.row.Status, c.row.ErrorCode = out.Status, out.ErrCode
	c.row.PromptTokens, c.row.CompletionTokens = out.Usage.PromptTokens, out.Usage.CompletionTokens
	c.row.Estimated = out.UsageEstimated
	if out.TTFT > 0 {
		c.row.TTFT = &out.TTFT
	}
	if out.Status >= http.StatusBadGateway {
		s.log.Warn("upstream response failed", "request_id", c.row.RequestID, "status", out.Status, "code", out.ErrCode,
			"reason", out.Reason, "upstream_status", resp.StatusCode, "upstream_ms", time.Since(upStart).Milliseconds())
	}
}
