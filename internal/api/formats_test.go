package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"local-generative-ai/internal/testupstream"
)

const reqID = "1a2b3c4d5e6f7a8b" // pins id8 = 1a2b3c4d in filenames

// fetch posts body to the fake upstream directly: what the model server sends.
func fetch(t *testing.T, up *testupstream.Server, body string) string {
	t.Helper()
	resp, err := http.Post(up.BaseURL()+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func (e *env) format(key, accept, path, body string) (int, http.Header, string) {
	e.t.Helper()
	h := []string{"X-Request-ID", reqID}
	if accept != "" {
		h = append(h, "Accept", accept)
	}
	rec := e.user(key, "POST", path, body, h...)
	return rec.Code, rec.Header(), rec.Body.String()
}

// TestFullFormatUnchanged: without a readable Accept type, responses are
// byte-for-byte what the upstream sends (minus the hidden usage chunk), as
// before D9. The non-streaming JSON is only named for download.
func TestFullFormatUnchanged(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	for _, accept := range []string{"", "*/*", "application/json", "text/event-stream"} {
		code, h, got := e.format(key, accept, "/v1/chat/completions", streamBody)
		if want := fetch(t, e.up, `{"stream":true}`); code != 200 || got != want {
			t.Errorf("Accept %q stream:\n got %q\nwant %q", accept, got, want)
		}
		if h.Get("Content-Disposition") != "" || h.Get("Vary") != "Accept" {
			t.Errorf("Accept %q stream headers %v", accept, h)
		}
		code, h, got = e.format(key, accept, "/v1/chat/completions", chatBody)
		if want := fetch(t, e.up, `{}`); code != 200 || got != want || h.Get("Content-Type") != "application/json" {
			t.Errorf("Accept %q JSON:\n got %q\nwant %q", accept, got, want)
		}
		if h.Get("Content-Disposition") != `attachment; filename="chat-1a2b3c4d.json"` {
			t.Errorf("Accept %q JSON disposition %q", accept, h.Get("Content-Disposition"))
		}
	}
	code, _, got := e.format(key, "", "/v1/chat/completions",
		`{"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	if want := fetch(t, e.up, `{"stream":true,"stream_options":{"include_usage":true}}`); code != 200 || got != want {
		t.Errorf("with usage chunk:\n got %q\nwant %q", got, want)
	}
}

func TestTextFormat(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	for _, body := range []string{streamBody, `{"max_tokens":5000,"messages":[{"role":"user","content":"hi"}]}`} {
		code, h, got := e.format(key, "text/plain", "/v1/chat/completions", body)
		if code != 200 || got != "Hello world\n" || h.Get("Content-Type") != "text/plain; charset=utf-8" ||
			h.Get("Content-Disposition") != `attachment; filename="chat-1a2b3c4d.md"` || h.Get("Vary") != "Accept" ||
			h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v %q", body, code, h, got)
		}
		up := e.up.Last(t).Body
		if up["stream"] != true || up["stream_options"].(map[string]any)["include_usage"] != true {
			t.Errorf("upstream not streamed: %v", up)
		}
		if strings.Contains(body, "5000") && up["max_tokens"] != 768.0 {
			t.Errorf("non-stream clamp lost: max_tokens %v", up["max_tokens"])
		}
	}
	if u := e.lastUsage(); u.Status != 200 || u.Stream != 1 || u.Completion != 2 || !u.TTFT.Valid {
		t.Errorf("usage %+v", u)
	}
	if !strings.Contains(e.logs.String(), `"format":"text"`) {
		t.Error("format not logged")
	}
}

func TestReportFormat(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	for _, stream := range []string{"true", "false"} {
		code, h, got := e.format(key, "text/markdown", "/v1/tasks/fix",
			`{"language":"python","code":"print(1/0)","error":"ZeroDivisionError","stream":`+stream+`}`)
		want := "# Fix (python)\n\n- Endpoint: `POST /v1/tasks/fix`\n- Language: python\n- Model: coder (`up-model`)\n- Time: "
		if code != 200 || !strings.HasPrefix(got, want) || h.Get("Content-Type") != "text/markdown; charset=utf-8" ||
			h.Get("Content-Disposition") != `attachment; filename="fix-python-1a2b3c4d.md"` {
			t.Fatalf("stream %s: %d %v\n%s", stream, code, h, got)
		}
		for _, part := range []string{"## Input\n\n### Code\n\n```python\nprint(1/0)\n```\n",
			"### Error output\n\n```text\nZeroDivisionError\n```\n", "## Answer\n\nHello world\n\n---\n\nTokens: 11 prompt + 2 completion = 13. Duration: "} {
			if !strings.Contains(got, part) {
				t.Errorf("stream %s: report lacks %q:\n%s", stream, part, got)
			}
		}
	}
	_, _, chat := e.format(key, "text/markdown", "/v1/chat/completions",
		`{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi"}]}`)
	if !strings.HasPrefix(chat, "# Chat\n") || !strings.Contains(chat, "### system\n\n```text\nbe brief\n```\n\n### user\n\n```text\nhi\n```\n") {
		t.Errorf("chat report:\n%s", chat)
	}
}

func TestCodeFormat(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{Chunks: []string{"Cause.\n```python\n", "print(1)\n", "```\nDone.\n"}})
	for _, stream := range []string{"true", "false"} {
		code, h, got := e.format(key, "text/x-code", "/v1/tasks/fix",
			`{"language":"python","code":"x","error":"E","stream":`+stream+`}`)
		if code != 200 || got != "print(1)\n" || h.Get("Content-Type") != "text/x-python; charset=utf-8" ||
			h.Get("Content-Disposition") != `attachment; filename="fix-python-1a2b3c4d.py"` ||
			h.Get("X-LGAI-Code-Blocks") != "1" || h.Get("Content-Length") != "9" {
			t.Errorf("stream %s: %d %v %q", stream, code, h, got)
		}
	}
	e.up.Set(testupstream.Behaviour{Chunks: []string{"```java\npublic class CalcTest {}\n```\n", "```java\nclass Helper {}\n```"}})
	_, h, got := e.format(key, "text/x-code", "/v1/tasks/tests", `{"language":"java","code":"class Calc {}"}`)
	if got != "public class CalcTest {}\n\nclass Helper {}\n" || h.Get("Content-Disposition") != `attachment; filename="CalcTest.java"` ||
		h.Get("X-LGAI-Code-Blocks") != "2" {
		t.Errorf("java tests: %v %q", h, got)
	}
	e.up.Set(testupstream.Behaviour{Chunks: []string{"No code, sorry."}})
	_, h, got = e.format(key, "text/x-code", "/v1/chat/completions", chatBody)
	if got != "No code, sorry." || h.Get("Content-Type") != "text/plain; charset=utf-8" ||
		h.Get("Content-Disposition") != `attachment; filename="chat-1a2b3c4d.txt"` || h.Get("X-LGAI-Code-Blocks") != "0" {
		t.Errorf("no block: %v %q", h, got)
	}
}

func TestNotAcceptable(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	for _, path := range []string{"/v1/chat/completions", "/v1/tasks/explain"} {
		rec := e.user(key, "POST", path, chatBody, "Accept", "image/png, text/html;q=0.5")
		expect(t, rec, http.StatusNotAcceptable, "not_acceptable")
		if rec.Header().Get("Vary") != "Accept" || rec.Header().Get("Content-Disposition") != "" {
			t.Errorf("headers %v", rec.Header())
		}
	}
	if len(e.up.Requests()) != 0 || len(e.usage()) != 0 {
		t.Error("a 406 reached the upstream or wrote a usage row")
	}
}

// TestMidStreamErrorPerFormat: live formats append the error line; buffered
// ones answer a JSON 502. Nothing upstream leaks; the row says 502.
func TestMidStreamErrorPerFormat(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{MidError: true})
	errLine := "\n\n[error: the model server failed during the stream; retry the request]\n"
	tests := []struct {
		accept, body string
		status       int
		check        func(h http.Header, got string) bool
	}{
		{"", streamBody, 200, func(_ http.Header, got string) bool { return strings.Contains(got, `"code":"upstream_error"`) }},
		{"text/plain", streamBody, 200, func(_ http.Header, got string) bool { return got == "Hello"+errLine }},
		{"text/markdown", streamBody, 200, func(_ http.Header, got string) bool {
			return strings.HasPrefix(got, "# Chat\n") && strings.HasSuffix(got, "## Answer\n\nHello"+errLine)
		}},
		{"text/plain", chatBody, 502, jsonError},
		{"text/x-code", streamBody, 502, jsonError},
	}
	for _, tc := range tests {
		code, h, got := e.format(key, tc.accept, "/v1/chat/completions", tc.body)
		if code != tc.status || !tc.check(h, got) || strings.Contains(got, "CANARY") {
			t.Errorf("Accept %q %s: %d %v %q", tc.accept, tc.body, code, h, got)
		}
		if u := e.lastUsage(); u.Status != 502 {
			t.Errorf("Accept %q: usage %+v", tc.accept, u)
		}
	}
}

func jsonError(h http.Header, got string) bool {
	return h.Get("Content-Type") == "application/json; charset=utf-8" && h.Get("Content-Disposition") == "" &&
		strings.Contains(got, `"code":"upstream_error"`)
}
