package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"local-generative-ai/internal/testupstream"
)

// TestRejectedRequestsKeepRateTokens is review M1 on the laptop profile
// (6 rpm, burst 3, 1 in flight): a second call while a stream runs is refused
// three times (the openai SDK retries twice) without spending tokens, so the
// key is not rate-limited once the first request is done.
func TestRejectedRequestsKeepRateTokens(t *testing.T) {
	e := newEnv(t, "LGAI_RATE_RPM", "6", "LGAI_RATE_BURST", "3", "LGAI_KEY_MAX_INFLIGHT", "1")
	key, _ := e.mint(`{"name":"lab-01"}`)
	wg := e.holdSlot(key, 400*time.Millisecond) // token 1 of 3
	for attempt := range 3 {                    // original call + 2 SDK retries
		rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
		expect(t, rec, 429, "concurrency_limit_exceeded")
		if rec.Header().Get("Retry-After") != "1" {
			t.Errorf("attempt %d: Retry-After %q", attempt, rec.Header().Get("Retry-After"))
		}
	}
	wg.Wait()
	e.up.Set(testupstream.Behaviour{})
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 200, "") // token 2
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 200, "") // token 3
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 429, "rate_limit_exceeded")
}

// TestGateRefusalRefundsRateToken: a request the global gate refuses (queue
// full, queue timeout, or client gone while queued) gets its token back.
// Burst 1 makes any lost token visible as a 429.
func TestGateRefusalRefundsRateToken(t *testing.T) {
	for _, tc := range []struct {
		name         string
		queue        string
		queueTimeout string
		cancelAfter  time.Duration // > 0: the client leaves while queued
		status       int
		code         string
	}{
		{"queue full", "0", "10s", 0, 503, "server_overloaded"},
		{"queue timeout", "1", "50ms", 0, 503, "server_overloaded"},
		{"client gone", "1", "10s", 50 * time.Millisecond, 499, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "LGAI_MAX_INFLIGHT", "1", "LGAI_QUEUE_SIZE", tc.queue, "LGAI_QUEUE_TIMEOUT", tc.queueTimeout,
				"LGAI_RATE_RPM", "6", "LGAI_RATE_BURST", "1")
			holder, _ := e.mint(`{"name":"lab-01"}`)
			key, _ := e.mint(`{"name":"lab-02"}`)
			wg := e.holdSlot(holder, 400*time.Millisecond)
			if tc.cancelAfter > 0 {
				ctx, cancel := context.WithTimeout(context.Background(), tc.cancelAfter)
				defer cancel()
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody)).WithContext(ctx)
				r.Header.Set("Authorization", "Bearer "+key)
				e.pub.ServeHTTP(httptest.NewRecorder(), r)
			} else {
				for range 3 {
					rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
					expect(t, rec, tc.status, tc.code)
					if rec.Header().Get("Retry-After") != "10" {
						t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
					}
				}
			}
			if u := e.lastUsage(); u.Status != tc.status {
				t.Errorf("usage status %d, want %d", u.Status, tc.status)
			}
			wg.Wait()
			e.up.Set(testupstream.Behaviour{})
			expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 200, "")
		})
	}
}
