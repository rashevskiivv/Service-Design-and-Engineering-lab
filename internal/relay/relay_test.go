package relay

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/testupstream"
)

// flushRecorder records the body at every flush.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes []string
}

func (f *flushRecorder) Flush() {
	f.flushes = append(f.flushes, f.Body.String())
	f.ResponseRecorder.Flush()
}

// open starts a streaming request against a fake upstream, like the gateway
// does (include_usage forced), and returns its context, cancel and body.
func open(t *testing.T, b testupstream.Behaviour) (*testupstream.Server, context.Context, context.CancelCauseFunc, io.Reader) {
	t.Helper()
	up := testupstream.New(t)
	up.Set(b)
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, up.BaseURL()+"/chat/completions",
		strings.NewReader(`{"stream":true,"stream_options":{"include_usage":true}}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return up, ctx, cancel, resp.Body
}

func opts() Options {
	return Options{Start: time.Now(), PromptBytes: 40, IdleTimeout: 2 * time.Second, WriteTimeout: time.Second}
}

func events(body string) []string {
	return strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n")
}

func TestStreamRelaysAndFlushesPerEvent(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{})
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	out := Stream(ctx, cancel, rec, body, opts())

	if out.Status != 200 || out.Usage != (oai.Usage{PromptTokens: 11, CompletionTokens: 2, TotalTokens: 13}) || out.UsageEstimated || out.TTFT <= 0 {
		t.Errorf("outcome %+v", out)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "text/event-stream" || h.Get("X-Accel-Buffering") != "no" || h.Get("Content-Length") != "" {
		t.Errorf("headers %v", h)
	}
	ev := events(rec.Body.String())
	if len(ev) != 5 || ev[4] != "data: [DONE]" || !strings.Contains(ev[1], `"Hello"`) {
		t.Fatalf("events %q", ev)
	}
	if strings.Contains(rec.Body.String(), `"choices":[],"usage"`) {
		t.Error("usage-only chunk forwarded although the client did not ask for it")
	}
	if len(rec.flushes) != 6 { // headers + 5 events
		t.Errorf("%d flushes, want 6", len(rec.flushes))
	}
	for _, f := range rec.flushes[1:] {
		if !strings.HasSuffix(f, "\n\n") {
			t.Errorf("flush mid-event: %q", f[max(0, len(f)-20):])
		}
	}
	if out.Reason != "" {
		t.Errorf("reason %q on success", out.Reason)
	}
}

func TestStreamForwardsUsageChunkWhenAsked(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{})
	rec := httptest.NewRecorder()
	o := opts()
	o.ClientWantsUsage = true
	Stream(ctx, cancel, rec, body, o)
	if !strings.Contains(rec.Body.String(), `"choices":[],"usage"`) {
		t.Error("usage chunk missing")
	}
}

func TestStreamUsageOnEveryChunk(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{UsageAlways: true, Chunks: []string{"a", "b", "c"}})
	out := Stream(ctx, cancel, httptest.NewRecorder(), body, opts())
	if out.Usage.CompletionTokens != 3 || out.UsageEstimated {
		t.Errorf("outcome %+v", out)
	}
}

func TestStreamFallbackEstimate(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{NoUsage: true, Chunks: []string{"a", "b", "c"}})
	out := Stream(ctx, cancel, httptest.NewRecorder(), body, opts())
	if !out.UsageEstimated || out.Usage.CompletionTokens != 3 || out.Usage.PromptTokens != 10 {
		t.Errorf("outcome %+v", out)
	}
}

func TestStreamUpstreamErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		b      testupstream.Behaviour
		reason string
	}{
		"mid-stream error event": {testupstream.Behaviour{MidError: true}, "error event"},
		"raw JSON line":          {testupstream.Behaviour{RawError: true}, "line that is not an SSE field"},
		"EOF without DONE":       {testupstream.Behaviour{NoDone: true}, "stream ended without [DONE]"},
	} {
		t.Run(name, func(t *testing.T) {
			_, ctx, cancel, body := open(t, tc.b)
			rec := httptest.NewRecorder()
			out := Stream(ctx, cancel, rec, body, opts())
			s := rec.Body.String()
			if out.Status != http.StatusBadGateway || out.ErrCode != oai.CodeUpstreamError || out.Reason != tc.reason {
				t.Errorf("outcome %+v", out)
			}
			if !strings.HasSuffix(s, string(errorEvent)) || strings.Contains(s, "[DONE]") || strings.Contains(s, "CANARY") {
				t.Errorf("body %q", s)
			}
		})
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	up, ctx, cancel, body := open(t, testupstream.Behaviour{StallAfter: 1})
	o := opts()
	o.IdleTimeout = 100 * time.Millisecond
	rec := httptest.NewRecorder()
	out := Stream(ctx, cancel, rec, body, o)
	if out.Status != http.StatusGatewayTimeout || out.ErrCode != oai.CodeUpstreamTimeout || out.Reason != "no data within the idle timeout" {
		t.Errorf("outcome %+v", out)
	}
	if !strings.HasSuffix(rec.Body.String(), string(errorEvent)) {
		t.Errorf("no error event: %q", rec.Body.String())
	}
	waitFor(t, func() bool { return up.Cancelled() == 1 })
}

func TestStreamTotalTimeout(t *testing.T) {
	up := testupstream.New(t)
	up.Set(testupstream.Behaviour{StallAfter: 1})
	parent, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx, stop := context.WithTimeoutCause(parent, 150*time.Millisecond, ErrUpstreamTimeout)
	defer stop()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, up.BaseURL()+"/chat/completions", strings.NewReader(`{"stream":true}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := Stream(ctx, cancel, httptest.NewRecorder(), resp.Body, opts())
	if out.Status != http.StatusGatewayTimeout {
		t.Errorf("outcome %+v", out)
	}
}

func TestStreamLineTooLong(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{HugeLine: 4000})
	o := opts()
	o.MaxEventBytes = 1000
	if out := Stream(ctx, cancel, httptest.NewRecorder(), body, o); out.Status != http.StatusBadGateway {
		t.Errorf("outcome %+v", out)
	}
}

func TestStreamEventsSplitAcrossReads(t *testing.T) {
	src := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\r\n\r\n: keep-alive\n\nevent: message\ndata: [DONE]"
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	rec := httptest.NewRecorder()
	out := Stream(ctx, cancel, rec, iotest.OneByteReader(strings.NewReader(src)), opts())
	if out.Status != 200 || out.Usage.CompletionTokens != 1 || !out.UsageEstimated {
		t.Errorf("outcome %+v", out)
	}
	if want := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n: keep-alive\n\nevent: message\ndata: [DONE]\n\n"; rec.Body.String() != want {
		t.Errorf("body %q", rec.Body.String())
	}
}

// TestStreamClientGone uses a real server: the client reads the first event
// and hangs up; the relay reports 499 and the upstream request is cancelled.
func TestStreamClientGone(t *testing.T) {
	up := testupstream.New(t)
	up.Set(testupstream.Behaviour{Chunks: strings.Split(strings.Repeat("x", 200), ""), ChunkDelay: 10 * time.Millisecond})
	outc := make(chan Outcome, 1)
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancelCause(r.Context())
		defer cancel(nil)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, up.BaseURL()+"/chat/completions", strings.NewReader(`{"stream":true}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		outc <- Stream(ctx, cancel, w, resp.Body, opts())
	}))
	defer gw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, gw.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case out := <-outc:
		if out.Status != oai.StatusClientClosed {
			t.Errorf("outcome %+v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not notice the client leaving")
	}
	waitFor(t, func() bool { return up.Cancelled() == 1 })
}

func TestJSONRelay(t *testing.T) {
	up := testupstream.New(t)
	h := http.Header{}
	h.Set("Set-Cookie", "s=1")
	h.Set("X-Internal", "yes")
	up.Set(testupstream.Behaviour{Header: h})
	resp, err := http.Post(up.BaseURL()+"/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rec := httptest.NewRecorder()
	out := JSON(context.Background(), rec, resp, opts())
	if out.Status != 200 || out.Usage.TotalTokens != 13 || out.UsageEstimated || out.TTFT != 0 {
		t.Errorf("outcome %+v", out)
	}
	if rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("X-Internal") != "" || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), `"content":"Hello world"`) {
		t.Errorf("body %s", rec.Body)
	}
}

func TestJSONEstimateAndOversize(t *testing.T) {
	body := `{"choices":[{"message":{"content":"` + strings.Repeat("a", 40) + `"}}]}`
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	out := JSON(context.Background(), httptest.NewRecorder(), resp, opts())
	if !out.UsageEstimated || out.Usage.CompletionTokens != 10 || out.Usage.PromptTokens != 10 {
		t.Errorf("outcome %+v", out)
	}
	big := &http.Response{Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(make([]byte, maxJSONBytes+1)))}
	rec := httptest.NewRecorder()
	if out := JSON(context.Background(), rec, big, opts()); out.Status != http.StatusBadGateway || rec.Code != http.StatusBadGateway {
		t.Errorf("oversize: %+v / %d", out, rec.Code)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestJSONRejectsNonCompletion is review m1: a non-streaming 2xx that is not a
// chat completion becomes a generic 502 instead of a 200 the SDK chokes on.
func TestJSONRejectsNonCompletion(t *testing.T) {
	for _, tc := range []struct {
		body, contentType, reason string
	}{
		{"<html>Bad Gateway</html>", "text/html; charset=utf-8", "body is not a JSON chat completion (content-type text/html)"},
		{`{"error":{"message":"CANARY"}}`, "application/json", "body is an error object (content-type application/json)"},
		{`{"object":"chat.completion","choices":[]}`, "application/json", "body has no choices (content-type application/json)"},
		{`null`, "application/json", "body has no choices (content-type application/json)"},
		{`[{"choices":[1]}]`, "application/json", "body is not a JSON chat completion (content-type application/json)"},
	} {
		resp := &http.Response{Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}
		rec := httptest.NewRecorder()
		out := JSON(context.Background(), rec, resp, opts())
		if out.Status != http.StatusBadGateway || out.ErrCode != oai.CodeUpstreamError || out.Reason != tc.reason {
			t.Errorf("%s: outcome %+v", tc.body, out)
		}
		if rec.Code != http.StatusBadGateway || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
			strings.Contains(rec.Body.String(), "CANARY") || strings.Contains(rec.Body.String(), "html") {
			t.Errorf("%s: response %d %v %s", tc.body, rec.Code, rec.Header(), rec.Body)
		}
	}
	// A completion with null content (tool calls) is valid.
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":null}}]}`))}
	if out := JSON(context.Background(), httptest.NewRecorder(), resp, opts()); out.Status != 200 {
		t.Errorf("null content: %+v", out)
	}
}
