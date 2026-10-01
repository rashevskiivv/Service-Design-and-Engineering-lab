package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"local-generative-ai/internal/testupstream"
)

func TestChatNonStreamEndToEnd(t *testing.T) {
	e := newEnv(t, "LGAI_MODEL_DEFAULTS", `coder={"reasoning_effort":"low"}`)
	key, _ := e.mint(`{"name":"lab-01"}`)
	rec := e.user(key, "POST", "/v1/chat/completions", `{"model":"coder","max_tokens":5000,"keep_alive":-1,"messages":[{"role":"user","content":"hi"}]}`)
	expect(t, rec, 200, "")
	if !strings.Contains(rec.Body.String(), `"content":"Hello world"`) || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("response %v %s", rec.Header(), rec.Body)
	}
	b := e.up.Last(t).Body
	if b["model"] != "up-model" || b["max_tokens"] != 768.0 || b["reasoning_effort"] != "low" || b["keep_alive"] != nil {
		t.Errorf("upstream body %v", b)
	}
	u := e.lastUsage()
	if u.Endpoint != "chat" || u.Model != "coder" || u.Status != 200 || u.Prompt != 11 || u.Completion != 2 ||
		u.Stream != 0 || u.TTFT.Valid || !u.Queue.Valid || u.RequestID != rec.Header().Get("X-Request-ID") {
		t.Errorf("usage %+v", u)
	}
}

func TestChatStreamEndToEnd(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	rec := e.user(key, "POST", "/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	expect(t, rec, 200, "")
	body := rec.Body.String()
	if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.HasSuffix(body, "data: [DONE]\n\n") ||
		strings.Contains(body, `"choices":[]`) {
		t.Errorf("stream %q", body)
	}
	if so := e.up.Last(t).Body["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Errorf("include_usage not forced: %v", so)
	}
	u := e.lastUsage()
	if u.Status != 200 || u.Stream != 1 || !u.TTFT.Valid || u.Completion != 2 || u.Estimated != 0 {
		t.Errorf("usage %+v", u)
	}
}

func TestMidStreamFailureRecorded(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{MidError: true})
	rec := e.user(key, "POST", "/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"code":"upstream_error"`) || strings.Contains(rec.Body.String(), "CANARY") {
		t.Errorf("stream %q", rec.Body)
	}
	if u := e.lastUsage(); u.Status != 502 || u.ErrorCode != "upstream_error" {
		t.Errorf("usage %+v", u)
	}
}

func TestTaskEndToEnd(t *testing.T) {
	e := newEnv(t, "LGAI_MODEL_DEFAULTS", `coder={"top_k":20}`)
	key, _ := e.mint(`{"name":"lab-01"}`)
	rec := e.user(key, "POST", "/v1/tasks/review", `{"language":"c","code":"char b[8]; strcpy(b, argv[1]);","stream":true}`)
	expect(t, rec, 200, "")
	b := e.up.Last(t).Body
	keys := make([]string, 0, len(b))
	for k := range b {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"max_tokens", "messages", "model", "stream", "stream_options", "temperature", "top_k"}; !slices.Equal(keys, want) {
		t.Errorf("upstream keys %v, want %v", keys, want)
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 2 || !strings.Contains(msgs[0].(map[string]any)["content"].(string), "C11") ||
		!strings.Contains(msgs[1].(map[string]any)["content"].(string), "strcpy") || b["max_tokens"] != 1024.0 || b["temperature"] != 0.2 {
		t.Errorf("upstream body %v", b)
	}
	if u := e.lastUsage(); u.Endpoint != "review" || u.Language != "c" || u.Model != "coder" || u.Status != 200 {
		t.Errorf("usage %+v", u)
	}

	// Non-streaming tests: default 1024 clamped to LGAI_NONSTREAM_MAX_TOKENS.
	expect(t, e.user(key, "POST", "/v1/tasks/tests", `{"language":"go","code":"func F() {}"}`), 200, "")
	if b := e.up.Last(t).Body; b["max_tokens"] != 768.0 || b["stream_options"] != nil {
		t.Errorf("tests body %v", b)
	}
	n := len(e.usage())
	expect(t, e.user(key, "POST", "/v1/tasks/refactor", `{"language":"go","code":"x"}`), 404, "unknown_task")
	expect(t, e.user(key, "POST", "/v1/tasks/fix", `{"language":"go","code":"x"}`), 400, "missing_field")
	expect(t, e.user(key, "POST", "/v1/tasks/explain", `{"language":"go","code":"x","model":"gpt-4o"}`), 404, "model_not_found")
	if rows := e.usage(); len(rows) != n { // invalid requests are logged, not recorded
		t.Errorf("rows after errors: %+v", rows[n:])
	}
}

func TestUpstreamSeesNoClientHeaders(t *testing.T) {
	e := newEnv(t, "LGAI_UPSTREAM_API_KEY", upstreamKey)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.user(key, "POST", "/v1/chat/completions", chatBody, "Cookie", "c=1", "X-Forwarded-For", "1.2.3.4", "X-Custom", "yes")
	h := e.up.Last(t).Header
	if h.Get("Authorization") != "Bearer "+upstreamKey || h.Get("Cookie") != "" || h.Get("X-Forwarded-For") != "" || h.Get("X-Custom") != "" {
		t.Errorf("upstream headers %v", h)
	}
}

func TestResponseHeaderHygiene(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	h := http.Header{}
	h.Set("Set-Cookie", "s=1")
	h.Set("Server", "vllm")
	h.Set("X-Internal", "x")
	e.up.Set(testupstream.Behaviour{Header: h})
	for _, body := range []string{chatBody, `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`} {
		rec := e.user(key, "POST", "/v1/chat/completions", body)
		for _, name := range []string{"Set-Cookie", "Server", "X-Internal"} {
			if rec.Header().Get(name) != "" {
				t.Errorf("%s leaked to the client", name)
			}
		}
	}
}

func TestNoRedirectsEndToEnd(t *testing.T) {
	e := newEnv(t)
	second := testupstream.New(t)
	e.up.Set(testupstream.Behaviour{Redirect: second.URL + "/v1/chat/completions"})
	key, _ := e.mint(`{"name":"lab-01"}`)
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 502, "upstream_error")
	if len(second.Requests()) != 0 {
		t.Error("redirect followed")
	}
}

func TestBodyLimit(t *testing.T) {
	e := newEnv(t, "LGAI_MAX_BODY_BYTES", "2048")
	key, _ := e.mint(`{"name":"lab-01"}`)
	big := `{"messages":[{"role":"user","content":"` + strings.Repeat("x", 3000) + `"}]}`
	expect(t, e.user(key, "POST", "/v1/chat/completions", big), 413, "request_too_large")
	// Without Content-Length the MaxBytesReader stops the read.
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(big))
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	e.pub.ServeHTTP(rec, r)
	expect(t, rec, 413, "request_too_large")
	if len(e.up.Requests()) != 0 {
		t.Error("oversized request reached upstream")
	}
}

func TestModelsAndModelNotFound(t *testing.T) {
	e := newEnv(t, "LGAI_MODELS", "coder=up-model,fast=other")
	key, _ := e.mint(`{"name":"lab-01"}`)
	rec := e.user(key, "GET", "/v1/models", "")
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	decodeJSON(t, rec, &list)
	if list.Object != "list" || len(list.Data) != 2 || list.Data[0].ID != "coder" || list.Data[1].OwnedBy != "local-generative-ai" {
		t.Errorf("models %+v", list)
	}
	if len(e.usage()) != 0 {
		t.Error("/v1/models recorded in usage")
	}
	expect(t, e.user(key, "POST", "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"x"}]}`), 404, "model_not_found")
	if rows := e.usage(); len(rows) != 0 {
		t.Errorf("usage rows for a refused request: %+v", rows)
	}
}

// ---- limits ----

func TestRateLimit429(t *testing.T) {
	e := newEnv(t, "LGAI_RATE_RPM", "6", "LGAI_RATE_BURST", "1")
	key, _ := e.mint(`{"name":"lab-01"}`)
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 200, "")
	rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
	expect(t, rec, 429, "rate_limit_exceeded")
	if ra, _ := strconv.Atoi(rec.Header().Get("Retry-After")); ra < 1 || ra > 10 {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	if u := e.lastUsage(); u.Status != 429 || u.ErrorCode != "rate_limit_exceeded" {
		t.Errorf("usage %+v", u)
	}
	unlimited, _ := e.mint(`{"name":"loadtest","rpm_limit":0}`)
	for range 5 {
		expect(t, e.user(unlimited, "POST", "/v1/chat/completions", chatBody), 200, "")
	}
}

func TestQuota429(t *testing.T) {
	e := newEnv(t, "LGAI_TOKEN_QUOTA_DAILY", "10")
	key, _ := e.mint(`{"name":"lab-01"}`)
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 200, "") // 13 tokens
	rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
	expect(t, rec, 429, "insufficient_quota")
	ra, _ := strconv.Atoi(rec.Header().Get("Retry-After"))
	if rec.Header().Get("X-Should-Retry") != "false" || ra < 1 || ra > 86400 {
		t.Errorf("headers %v", rec.Header())
	}
}

// holdSlot starts a request whose upstream answer is delayed, and returns
// once it holds its slots.
func (e *env) holdSlot(key string, delay time.Duration) *sync.WaitGroup {
	e.t.Helper()
	e.up.Set(testupstream.Behaviour{HeaderDelay: delay})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.user(key, "POST", "/v1/chat/completions", chatBody)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(e.up.Requests()) == 0 {
		if time.Now().After(deadline) {
			e.t.Fatal("request never reached upstream")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return &wg
}

func TestPerKeyConcurrency429(t *testing.T) {
	e := newEnv(t, "LGAI_KEY_MAX_INFLIGHT", "1")
	key, _ := e.mint(`{"name":"lab-01"}`)
	wg := e.holdSlot(key, 300*time.Millisecond)
	rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
	expect(t, rec, 429, "concurrency_limit_exceeded")
	if rec.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	other, _ := e.mint(`{"name":"lab-02"}`)
	expect(t, e.user(other, "GET", "/v1/models", ""), 200, "")
	wg.Wait()
}

func TestQueueFullAndTimeout503(t *testing.T) {
	e := newEnv(t, "LGAI_MAX_INFLIGHT", "1", "LGAI_QUEUE_SIZE", "0")
	key, _ := e.mint(`{"name":"lab-01"}`)
	wg := e.holdSlot(key, 300*time.Millisecond)
	rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
	expect(t, rec, 503, "server_overloaded")
	if rec.Header().Get("Retry-After") != "10" {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	wg.Wait()

	e = newEnv(t, "LGAI_MAX_INFLIGHT", "1", "LGAI_QUEUE_SIZE", "1", "LGAI_QUEUE_TIMEOUT", "50ms")
	key, _ = e.mint(`{"name":"lab-01"}`)
	wg = e.holdSlot(key, 400*time.Millisecond)
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 503, "server_overloaded")
	if u := e.lastUsage(); u.Status != 503 || !u.Queue.Valid || u.Queue.Int64 < 40 {
		t.Errorf("usage %+v", u)
	}
	wg.Wait()
	if in, q := e.s.gate.Stats(); in != 0 || q != 0 {
		t.Errorf("gate %d/%d after all requests", in, q)
	}
}

func TestUpstreamStatusMapping(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	for _, tc := range []struct {
		b      testupstream.Behaviour
		status int
		code   string
	}{
		{testupstream.Behaviour{Status: 400, ErrorMessage: "maximum context length is 8192"}, 400, "context_length_exceeded"},
		{testupstream.Behaviour{Status: 404}, 502, "upstream_error"},
		{testupstream.Behaviour{Status: 503, RetryAfter: "4"}, 503, "server_overloaded"},
		{testupstream.Behaviour{Status: 500}, 502, "upstream_error"},
	} {
		e.up.Set(tc.b)
		rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
		expect(t, rec, tc.status, tc.code)
		if tc.status == 503 && rec.Header().Get("Retry-After") != "4" {
			t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
		}
	}
}
