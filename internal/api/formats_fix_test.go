package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-generative-ai/internal/testupstream"
)

// Tests for the D9 review fixes (docs/reviews/code-review.md, "D9 review"):
// truncation markers (M1), the buffer cap (m1), dropped connections after a
// live failure (m2) and per-sink idle timeouts (m4).

const truncLine = "\n[truncated: max_tokens reached]\n"

// TestTruncationPerFormat: an answer cut at max_tokens is marked in every
// readable format and left untouched in the full format.
func TestTruncationPerFormat(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{Chunks: []string{"```python\n", "def f():\n", "    return 1\n"}, FinishReason: "length"})
	tests := []struct {
		accept, body string
		check        func(h http.Header, got string) bool
	}{
		{"", streamBody, func(h http.Header, got string) bool {
			return strings.Contains(got, `"finish_reason":"length"`) && !strings.Contains(got, "[truncated")
		}},
		{"text/plain", streamBody, func(_ http.Header, got string) bool { return strings.HasSuffix(got, truncLine) }},
		{"text/plain", chatBody, func(h http.Header, got string) bool {
			return strings.HasSuffix(got, truncLine) && h.Get("X-LGAI-Finish-Reason") == "length" &&
				h.Get("X-LGAI-Truncated") == "true"
		}},
		{"text/markdown", streamBody, func(_ http.Header, got string) bool {
			return strings.Contains(got, "⚠ truncated at max_tokens")
		}},
		{"text/x-code", chatBody, func(h http.Header, got string) bool {
			return h.Get("X-LGAI-Truncated") == "true" && h.Get("X-LGAI-Finish-Reason") == "length" &&
				got == "def f():\n    return 1\n# [truncated: the answer reached max_tokens, so this file is incomplete]\n"
		}},
	}
	for _, tc := range tests {
		code, h, got := e.format(key, tc.accept, "/v1/chat/completions", tc.body)
		if code != 200 || !tc.check(h, got) {
			t.Errorf("Accept %q %s: %d %v %q", tc.accept, tc.body, code, h, got)
		}
	}
}

// TestCodeTruncationUsesLanguageComment: the note in a task's code file is a
// comment in the task's language, so the file still parses up to the cut.
func TestCodeTruncationUsesLanguageComment(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{Chunks: []string{"```python\n", "x = 1\n"}, FinishReason: "length"})
	code, h, got := e.format(key, "text/x-code", "/v1/tasks/fix",
		`{"language":"python","code":"x = 1/0","error":"ZeroDivisionError"}`)
	want := "x = 1\n# [truncated: the answer reached max_tokens, so this file is incomplete]\n"
	if code != 200 || got != want || h.Get("X-LGAI-Truncated") != "true" {
		t.Errorf("%d %v %q, want %q", code, h, got, want)
	}
}

// TestNotTruncatedHasNoMarker: a normal stop gets the finish reason header and
// no marker.
func TestNotTruncatedHasNoMarker(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	code, h, got := e.format(key, "text/plain", "/v1/chat/completions", chatBody)
	if code != 200 || got != "Hello world\n" || h.Get("X-LGAI-Finish-Reason") != "stop" || h.Get("X-LGAI-Truncated") != "" {
		t.Errorf("%d %v %q", code, h, got)
	}
}

// TestBufferedAnswerCap: an upstream that ignores max_tokens cannot grow a
// buffered answer past max_tokens × 16 bytes; the answer ends as truncated.
func TestBufferedAnswerCap(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	chunks := make([]string, 50)
	for i := range chunks {
		chunks[i] = "0123456789"
	}
	e.up.Set(testupstream.Behaviour{Chunks: chunks})
	body := `{"model":"coder","max_tokens":2,"messages":[{"role":"user","content":"hi"}]}` // cap: 32 bytes
	code, h, got := e.format(key, "text/plain", "/v1/chat/completions", body)
	answer := strings.TrimSuffix(got, truncLine)
	if code != 200 || answer == got || len(strings.TrimSuffix(answer, "\n")) > 32 || h.Get("X-LGAI-Truncated") != "true" {
		t.Errorf("%d %v %d bytes: %q", code, h, len(got), got)
	}
	if u := e.lastUsage(); u.Status != 200 {
		t.Errorf("usage %+v", u)
	}
}

// TestLiveFailureDropsConnection: over a real connection, a live text answer
// that fails midway ends with the error line and a broken body, so curl and
// scripts see a failure instead of a clean end (review D9-m2).
func TestLiveFailureDropsConnection(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{MidError: true})
	srv := httptest.NewServer(e.pub)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/chat/completions", strings.NewReader(streamBody))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || err == nil || !strings.Contains(string(b), "[error: ") {
		t.Errorf("status %d, read error %v, body %q: want 200, a read error and the error line", resp.StatusCode, err, b)
	}
}

// TestIdleTimeoutPerSink: an upstream that goes silent mid-answer ends live
// text with the error line and a dropped connection, and a buffered format
// with a JSON 504.
func TestIdleTimeoutPerSink(t *testing.T) {
	e := newEnv(t, "LGAI_UPSTREAM_IDLE_TIMEOUT", "1s")
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{StallAfter: 1})

	code, _, got, aborted := e.formatAbort(key, "text/plain", "/v1/chat/completions", streamBody)
	if code != 200 || !aborted || !strings.HasPrefix(got, "Hello") || !strings.Contains(got, "[error: ") {
		t.Errorf("live: %d aborted=%v %q", code, aborted, got)
	}
	if u := e.lastUsage(); u.Status != http.StatusGatewayTimeout {
		t.Errorf("live usage %+v", u)
	}

	code, h, got, aborted := e.formatAbort(key, "text/x-code", "/v1/chat/completions", chatBody)
	if code != http.StatusGatewayTimeout || aborted || !jsonError504(h, got) {
		t.Errorf("buffered: %d aborted=%v %v %q", code, aborted, h, got)
	}
}

func jsonError504(h http.Header, got string) bool {
	return h.Get("Content-Type") == "application/json; charset=utf-8" && h.Get("Content-Disposition") == "" &&
		strings.Contains(got, `"error"`)
}
