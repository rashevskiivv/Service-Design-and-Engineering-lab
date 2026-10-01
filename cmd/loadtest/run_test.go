package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "lgai_TESTtestTESTtestTESTtestTESTtestTESTtes"

// fakeGateway records every request and delegates the response to handle.
type fakeGateway struct {
	mu     sync.Mutex
	reqs   []recorded
	handle func(w http.ResponseWriter, r *http.Request, body map[string]any)
}

type recorded struct {
	path string
	auth string
	body map[string]any
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	g.mu.Lock()
	g.reqs = append(g.reqs, recorded{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: body})
	g.mu.Unlock()
	g.handle(w, r, body)
}

func (g *fakeGateway) recorded() []recorded {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]recorded(nil), g.reqs...)
}

func newFake(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, body map[string]any)) (*fakeGateway, *httptest.Server) {
	t.Helper()
	g := &fakeGateway{handle: handle}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, srv
}

type sseWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
}

func startSSE(w http.ResponseWriter) *sseWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	s := &sseWriter{w: w, fl: w.(http.Flusher)}
	s.fl.Flush()
	return s
}

func (s *sseWriter) data(v string) {
	fmt.Fprintf(s.w, "data: %s\n\n", v)
	s.fl.Flush()
}

func (s *sseWriter) content(text string) {
	b, _ := json.Marshal(text)
	s.data(`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":` + string(b) + `},"finish_reason":null}]}`)
}

// streamAnswer streams a normal answer: role chunk, content pieces, finish,
// a usage chunk when the client asked for it, [DONE].
func streamAnswer(w http.ResponseWriter, body map[string]any, pieces ...string) {
	s := startSSE(w)
	s.data(roleChunk)
	for _, p := range pieces {
		s.content(p)
	}
	s.data(finishChunk)
	if so, _ := body["stream_options"].(map[string]any); so != nil && so["include_usage"] == true {
		s.data(fmt.Sprintf(`{"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":42,"completion_tokens":%d,"total_tokens":%d}}`, len(pieces), 42+len(pieces)))
	}
	s.data("[DONE]")
}

func writeError(w http.ResponseWriter, status int, retryAfter, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"message":"%s for test","type":"server_error","code":"%s","param":null}}`, code, code)
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		if k == "LGAI_KEY" {
			return testKey
		}
		return ""
	}
	code = run(context.Background(), args, &out, &errb, getenv)
	return code, out.String(), errb.String()
}

func readReport(t *testing.T, path string) report {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(testKey)) {
		t.Fatal("the API key leaked into the JSON report")
	}
	var rep report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	return rep
}

func TestLoadRunSendsContractBodies(t *testing.T) {
	g, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		streamAnswer(w, body, "Looks ", "fine.")
	})
	jsonPath := filepath.Join(t.TempDir(), "run.json")
	code, stdout, stderr := runCLI(t, "-url", srv.URL+"/v1/", "-c", "3", "-n", "25", "-json", jsonPath)
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"requests  25", "ok 25", "TTFC s (first content)", "aggregate"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	rep := readReport(t, jsonPath)
	if len(rep.Results) != 25 || rep.Summary.OK != 25 || rep.Summary.TokensFromUsage != 25 {
		t.Fatalf("results=%d ok=%d usage=%d", len(rep.Results), rep.Summary.OK, rep.Summary.TokensFromUsage)
	}
	seen := map[string]bool{}
	for _, rq := range g.recorded() {
		if rq.auth != "Bearer "+testKey {
			t.Errorf("Authorization = %q", rq.auth)
		}
		if rq.body["model"] != "coder" || rq.body["stream"] != true {
			t.Errorf("%s body: model=%v stream=%v", rq.path, rq.body["model"], rq.body["stream"])
		}
		if so, _ := rq.body["stream_options"].(map[string]any); so["include_usage"] != true {
			t.Errorf("%s: stream_options = %v", rq.path, rq.body["stream_options"])
		}
		if _, ok := rq.body["max_tokens"]; ok {
			t.Errorf("%s: max_tokens sent without -max-tokens", rq.path)
		}
		switch rq.path {
		case "/v1/chat/completions":
			seen["chat"] = true
			msgs, _ := rq.body["messages"].([]any)
			if len(msgs) != 2 {
				t.Errorf("chat messages = %v", msgs)
			}
		case "/v1/tasks/explain", "/v1/tasks/review", "/v1/tasks/tests", "/v1/tasks/fix":
			task := strings.TrimPrefix(rq.path, "/v1/tasks/")
			seen[task] = true
			lang, _ := rq.body["language"].(string)
			if code, _ := rq.body["code"].(string); code == "" || lang == "" {
				t.Errorf("%s: language=%q code empty=%v", rq.path, lang, code == "")
			}
			errText, hasErr := rq.body["error"].(string)
			if (task == "fix") != hasErr || (hasErr && errText == "") {
				t.Errorf("%s: error field present=%v", rq.path, hasErr)
			}
		default:
			t.Errorf("unexpected path %s", rq.path)
		}
	}
	if len(seen) != 5 {
		t.Errorf("mix covered only %v", seen)
	}
}

func TestTTFCMeasuredOverHTTP(t *testing.T) {
	_, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		s := startSSE(w)
		s.data(roleChunk) // at ~0 ms: headers and a role-only chunk
		time.Sleep(150 * time.Millisecond)
		s.data(`{"choices":[{"index":0,"delta":{"reasoning":"thinking"}}]}`)
		time.Sleep(150 * time.Millisecond)
		s.content("visible")
		s.data(finishChunk)
		s.data("[DONE]")
	})
	jsonPath := filepath.Join(t.TempDir(), "ttfc.json")
	code, stdout, stderr := runCLI(t, "-url", srv.URL, "-endpoint", "chat", "-n", "1", "-json", jsonPath)
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	r := readReport(t, jsonPath).Results[0]
	if r.TTFBMs == nil || r.TTFTMs == nil || r.TTFCMs == nil {
		t.Fatalf("missing timings: %+v", r)
	}
	if *r.TTFBMs >= 140 {
		t.Errorf("TTFB = %.1f ms; headers were sent immediately", *r.TTFBMs)
	}
	if *r.TTFTMs < 140 || *r.TTFCMs < 290 || *r.TTFCMs-*r.TTFTMs < 100 {
		t.Errorf("TTFT = %.1f ms, TTFC = %.1f ms; want ~150 and ~300", *r.TTFTMs, *r.TTFCMs)
	}
	if r.TokensSource != tokensFromChunks || r.CompletionTokens != 2 {
		t.Errorf("tokens = %d from %s, want 2 from chunks (no usage chunk sent)", r.CompletionTokens, r.TokensSource)
	}
}

func TestRateLimitAndOverloadAreReportedNotFailed(t *testing.T) {
	_, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			writeError(w, http.StatusTooManyRequests, "7", "rate_limit_exceeded")
		case "/v1/tasks/explain":
			writeError(w, http.StatusServiceUnavailable, "10", "server_overloaded")
		default:
			writeError(w, http.StatusServiceUnavailable, "30", "maintenance")
		}
	})
	dir := t.TempDir()
	for _, tc := range []struct {
		endpoint, status, retryAfter, kind string
		n                                  int
	}{
		{"chat", "429", "7", "http_429:rate_limit_exceeded", 3},
		{"explain", "503", "10", "http_503:server_overloaded", 2},
		{"review", "503", "30", "http_503:maintenance", 1},
	} {
		jsonPath := filepath.Join(dir, tc.endpoint+".json")
		code, stdout, _ := runCLI(t, "-url", srv.URL, "-endpoint", tc.endpoint, "-n", fmt.Sprint(tc.n), "-json", jsonPath)
		if code != exitOK {
			t.Errorf("%s: exit %d; 429/503 are expected under load, not errors", tc.endpoint, code)
		}
		s := readReport(t, jsonPath).Summary
		if s.StatusHistogram[tc.status] != tc.n || s.Errors != 0 || s.OK != 0 {
			t.Errorf("%s: histogram=%v errors=%d", tc.endpoint, s.StatusHistogram, s.Errors)
		}
		if s.RetryAfter[tc.status][tc.retryAfter] != tc.n {
			t.Errorf("%s: retry_after_seen = %v", tc.endpoint, s.RetryAfter)
		}
		if s.NonOK[tc.kind] != tc.n {
			t.Errorf("%s: non_ok = %v", tc.endpoint, s.NonOK)
		}
		if !strings.Contains(stdout, "retry-after  "+tc.status+": "+tc.retryAfter+"s x") {
			t.Errorf("%s: stdout lacks the Retry-After line:\n%s", tc.endpoint, stdout)
		}
	}
}

func TestUnauthorizedAndMidStreamErrorsFail(t *testing.T) {
	_, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch r.URL.Path {
		case "/v1/tasks/review":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"invalid or revoked API key","type":"authentication_error","code":"invalid_api_key","param":null}}`)
		case "/v1/tasks/explain": // stream dies: one error event, no [DONE]
			s := startSSE(w)
			s.content("partial answer")
			s.data(`{"error":{"message":"upstream stopped responding","type":"server_error","code":"upstream_timeout"}}`)
		default: // EOF without [DONE]
			s := startSSE(w)
			s.content("partial answer")
		}
	})
	dir := t.TempDir()
	for _, tc := range []struct{ endpoint, class, code string }{
		{"review", classHTTP401, "invalid_api_key"},
		{"explain", classStreamError, "upstream_timeout"},
		{"tests", classTruncated, ""},
	} {
		jsonPath := filepath.Join(dir, tc.endpoint+".json")
		code, _, _ := runCLI(t, "-url", srv.URL, "-endpoint", tc.endpoint, "-n", "2", "-json", jsonPath)
		if code != exitFailed {
			t.Errorf("%s: exit %d, want %d", tc.endpoint, code, exitFailed)
		}
		rep := readReport(t, jsonPath)
		if rep.Summary.Errors != 2 {
			t.Errorf("%s: errors = %d", tc.endpoint, rep.Summary.Errors)
		}
		r := rep.Results[0]
		if r.Class != tc.class || r.ErrorCode != tc.code {
			t.Errorf("%s: class=%s code=%s, want %s/%s", tc.endpoint, r.Class, r.ErrorCode, tc.class, tc.code)
		}
	}
}

func TestNonStreamingJSON(t *testing.T) {
	g, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
	})
	jsonPath := filepath.Join(t.TempDir(), "ns.json")
	code, stdout, stderr := runCLI(t, "-url", srv.URL, "-stream=false", "-endpoint", "fix", "-max-tokens", "300", "-n", "2", "-json", jsonPath)
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, rq := range g.recorded() {
		if rq.body["stream"] != false || rq.body["stream_options"] != nil || rq.body["max_tokens"] != float64(300) {
			t.Errorf("body stream=%v stream_options=%v max_tokens=%v", rq.body["stream"], rq.body["stream_options"], rq.body["max_tokens"])
		}
	}
	r := readReport(t, jsonPath).Results[0]
	if r.Class != classOK || r.CompletionTokens != 3 || r.TTFCMs == nil || r.DecodeTokPerSec != nil {
		t.Errorf("result = %+v", r)
	}
}

func TestBurstStartsAllClientsTogether(t *testing.T) {
	const clients = 6
	var inflight, peak atomic.Int32
	allIn := make(chan struct{})
	var once sync.Once
	_, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if n == clients {
			once.Do(func() { close(allIn) })
		}
		select { // hold every request until all clients are in flight
		case <-allIn:
		case <-time.After(3 * time.Second):
		}
		streamAnswer(w, body, "ok")
	})
	code, stdout, stderr := runCLI(t, "-url", srv.URL, "-burst", "-c", fmt.Sprint(clients))
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if peak.Load() != clients {
		t.Errorf("peak concurrency = %d, want %d", peak.Load(), clients)
	}
	if !strings.Contains(stdout, fmt.Sprintf("requests  %d", clients)) {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestDurationModeStopsStartingRequests(t *testing.T) {
	_, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		time.Sleep(40 * time.Millisecond)
		streamAnswer(w, body, "ok")
	})
	jsonPath := filepath.Join(t.TempDir(), "d.json")
	code, stdout, stderr := runCLI(t, "-url", srv.URL, "-c", "2", "-d", "300ms", "-json", jsonPath)
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	rep := readReport(t, jsonPath)
	if n := len(rep.Results); n < 4 || n > 20 {
		t.Errorf("%d requests in 300 ms with 2 clients at ~40 ms each", n)
	}
	for _, r := range rep.Results {
		if r.StartMs > 320 {
			t.Errorf("request %d started at %.0f ms, after -d", r.Index, r.StartMs)
		}
	}
}

// smokeFake answers every task with a fenced code block, except the
// (task, language) pairs listed in empty, which get reasoning only and
// finish_reason=length: the gpt-oss failure mode critic M3 describes.
func smokeFake(t *testing.T, empty map[string]bool) (*fakeGateway, *httptest.Server) {
	return newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		task := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
		lang, _ := body["language"].(string)
		if empty[task+"/"+lang] {
			s := startSSE(w)
			s.data(roleChunk)
			s.data(`{"choices":[{"index":0,"delta":{"reasoning":"The user wants tests. Let me think about edge cases..."}}]}`)
			s.data(`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
			s.data(`{"choices":[],"usage":{"prompt_tokens":300,"completion_tokens":1024,"total_tokens":1324}}`)
			s.data("[DONE]")
			return
		}
		streamAnswer(w, body, "Root cause: ...\n\n", "```"+lang+"\n", "fixed();\n", "```\n")
	})
}

func TestSmokeWritesAnswersAndFailsOnEmptyContent(t *testing.T) {
	g, srv := smokeFake(t, map[string]bool{"tests/cpp": true})
	out := filepath.Join(t.TempDir(), "smoke")
	jsonPath := filepath.Join(t.TempDir(), "smoke.json")
	code, stdout, stderr := runCLI(t, "-url", srv.URL, "-smoke", "-out", out, "-json", jsonPath)
	if code != exitFailed {
		t.Fatalf("exit %d, want %d\n%s\n%s", code, exitFailed, stdout, stderr)
	}
	if n := len(g.recorded()); n != 20 {
		t.Errorf("smoke sent %d requests, want 20", n)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 21 { // 4 tasks x 5 languages + index.md
		t.Errorf("%d files in %s, want 21", len(entries), out)
	}
	bad, _ := os.ReadFile(filepath.Join(out, "tests-cpp.md"))
	if !bytes.Contains(bad, []byte("**FAIL**")) || !bytes.Contains(bad, []byte("empty content")) || !bytes.Contains(bad, []byte("finish_reason=length")) {
		t.Errorf("tests-cpp.md does not explain the failure:\n%s", bad)
	}
	good, _ := os.ReadFile(filepath.Join(out, "fix-python.md"))
	for _, want := range []string{"**PASS**", "## Input: error output", "KeyError: 'the'", "## Answer", "fixed();"} {
		if !bytes.Contains(good, []byte(want)) {
			t.Errorf("fix-python.md lacks %q:\n%s", want, good)
		}
	}
	if !strings.Contains(stdout, "PASS 19  WARN 0  FAIL 1") {
		t.Errorf("stdout:\n%s", stdout)
	}
	rep := readReport(t, jsonPath)
	if len(rep.Smoke) != 20 || rep.Summary.EmptyContent != 1 {
		t.Errorf("json smoke rows = %d, empty = %d", len(rep.Smoke), rep.Summary.EmptyContent)
	}
	if b, _ := os.ReadFile(jsonPath); bytes.Contains(b, []byte("fixed();")) {
		t.Error("answer text must not be written to the JSON report")
	}
}

func TestSmokeAllPass(t *testing.T) {
	_, srv := smokeFake(t, nil)
	code, stdout, stderr := runCLI(t, "-url", srv.URL, "-smoke", "-out", t.TempDir(), "-size", "medium")
	if code != exitOK || !strings.Contains(stdout, "PASS 20  WARN 0  FAIL 0") {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
}

func TestSmokeRetriesAfterRetryAfter(t *testing.T) {
	var calls atomic.Int32
	_, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch calls.Add(1) {
		case 1:
			writeError(w, http.StatusTooManyRequests, "3", "rate_limit_exceeded")
		case 2:
			writeError(w, http.StatusServiceUnavailable, "", "server_overloaded") // no Retry-After: default wait
		default:
			streamAnswer(w, body, "It prints the words.")
		}
	})
	cfg, err := parseFlags([]string{"-url", srv.URL, "-smoke", "-endpoint", "explain", "-lang", "python", "-out", t.TempDir()}, io.Discard, func(string) string { return testKey })
	if err != nil {
		t.Fatal(err)
	}
	var stderr, stdout bytes.Buffer
	r, err := newRunner(cfg, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	r.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	if code := r.runSmoke(context.Background(), &stdout); code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	if len(slept) != 2 || slept[0] != 3*time.Second || slept[1] != smokeDefaultWait {
		t.Errorf("slept %v, want [3s %v]", slept, smokeDefaultWait)
	}
}

func TestSmokeDoesNotRetryExhaustedQuota(t *testing.T) {
	var calls atomic.Int32
	_, srv := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		calls.Add(1)
		w.Header().Set("X-Should-Retry", "false")
		writeError(w, http.StatusTooManyRequests, "40000", "insufficient_quota")
	})
	code, stdout, _ := runCLI(t, "-url", srv.URL, "-smoke", "-endpoint", "review", "-lang", "go", "-out", t.TempDir())
	if code != exitFailed || calls.Load() != 1 {
		t.Fatalf("exit %d after %d calls\n%s", code, calls.Load(), stdout)
	}
	if !strings.Contains(stdout, "insufficient_quota") {
		t.Errorf("stdout should name the quota error:\n%s", stdout)
	}
}

func TestFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-endpoint", "refactor"},
		{"-lang", "rust"},
		{"-size", "large"},
		{"-c", "0"},
		{"-burst", "-n", "5"},
		{"-smoke", "-endpoint", "chat"},
		{"-smoke", "-burst"},
		{"-url", "ftp://host"},
		{"-url", "http://user:pw@host:8080"},
		{"-url", "http://host:8080/?key=x"},
		{"stray-argument"},
	} {
		if code, _, stderr := runCLI(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d (stderr %q)", args, code, exitUsage, stderr)
		}
	}
	if code, _, _ := runCLI(t, "-h"); code != exitOK {
		t.Errorf("-h: exit %d", code)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:8080":        "http://127.0.0.1:8080",
		"http://127.0.0.1:8080/":       "http://127.0.0.1:8080",
		"http://127.0.0.1:8080/v1":     "http://127.0.0.1:8080",
		"https://lgai.example.org/v1/": "https://lgai.example.org",
		"https://h.example/prefix/v1":  "https://h.example/prefix",
	} {
		got, err := normalizeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
