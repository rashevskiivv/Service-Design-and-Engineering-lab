package api

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"local-generative-ai/internal/testupstream"
)

// hashForms is every form of a key that must never leave the gateway.
func hashForms(key string) []string {
	h := sha256.Sum256([]byte(key))
	return []string{key, hex.EncodeToString(h[:]), base64.StdEncoding.EncodeToString(h[:]), base64.RawURLEncoding.EncodeToString(h[:])}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, "LGAI_RATE_RPM", "1", "LGAI_RATE_BURST", "1")
	key, _ := e.mint(`{"name":"lab-01"}`)
	maint := newEnv(t)
	mkey, _ := maint.mint(`{"name":"lab-01"}`)
	maint.s.maintenance.Store(true)
	cases := map[string]*httptest.ResponseRecorder{
		"json 200": e.user(key, "GET", "/v1/models", ""),
		"sse 200":  e.user(key, "POST", "/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":"x"}]}`),
		"400":      e.do(e.pub, "POST", "/v1/chat/completions", `{`, "Authorization", "Bearer "+key),
		"401":      e.do(e.pub, "GET", "/v1/models", ""),
		"404":      e.do(e.pub, "GET", "/nope", ""),
		"405":      e.do(e.pub, "GET", "/v1/chat/completions", ""),
		"429":      e.user(key, "POST", "/v1/chat/completions", chatBody),
		"503":      maint.user(mkey, "POST", "/v1/chat/completions", chatBody),
		"admin":    e.adm("GET", "/admin/keys", ""),
	}
	for name, rec := range cases {
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" ||
			h.Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" ||
			h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Request-ID") == "" {
			t.Errorf("%s (%d): headers %v", name, rec.Code, h)
		}
		for k := range h {
			if strings.HasPrefix(k, "Access-Control-") {
				t.Errorf("%s: %s present", name, k)
			}
		}
		if rec.Code >= 400 && h.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("%s: error content type %q", name, h.Get("Content-Type"))
		}
	}
	if cases["429"].Code != 429 || cases["503"].Code != 503 || cases["sse 200"].Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("setup: 429=%d 503=%d", cases["429"].Code, cases["503"].Code)
	}
}

func TestNoCORS(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.pub, "OPTIONS", "/v1/chat/completions", "", "Origin", "https://evil.example", "Access-Control-Request-Method", "POST")
	expect(t, rec, 405, "method_not_allowed")
	if rec.Header().Get("Allow") != "POST" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("headers %v", rec.Header())
	}
}

func TestReadyzCachedAndGeneric(t *testing.T) {
	e := newEnv(t)
	e.up.Set(testupstream.Behaviour{Status: 503})
	for range 100 {
		rec := e.do(e.pub, "GET", "/readyz", "")
		expect(t, rec, 503, "upstream_unavailable")
		if regexp.MustCompile(`\d+\.\d+\.\d+\.\d+|:\d{2,5}`).MatchString(rec.Body.String()) || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("readyz body %s", rec.Body)
		}
	}
	if n := len(e.up.Requests()); n != 1 {
		t.Errorf("%d upstream probes for 100 calls", n)
	}
	ok := newEnv(t)
	rec := ok.do(ok.pub, "GET", "/readyz", "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"db":"ok","status":"ready","upstream":"ok"}` {
		t.Errorf("ready: %d %s", rec.Code, rec.Body)
	}
}

func TestRequestIDSanitised(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	generated := regexp.MustCompile(`^[0-9a-f]{16}$`)
	for _, bad := range []string{"a b", strings.Repeat("r", 65), "%00", "x\ty"} {
		rec := e.user(key, "POST", "/v1/chat/completions", chatBody, "X-Request-ID", bad)
		id := rec.Header().Get("X-Request-ID")
		if !generated.MatchString(id) || e.up.Last(t).Header.Get("X-Request-ID") != id {
			t.Errorf("%q → %q", bad, id)
		}
	}
	rec := e.user(key, "POST", "/v1/chat/completions", chatBody, "X-Request-ID", "lab.req-42")
	if rec.Header().Get("X-Request-ID") != "lab.req-42" || e.lastUsage().RequestID != "lab.req-42" {
		t.Errorf("valid id not reused: %q", rec.Header().Get("X-Request-ID"))
	}
}

// TestNoSecretsOrContentAnywhere is the canary test (security §6 F): no key,
// hash, token or content in the DEBUG log, the database or its WAL.
func TestNoSecretsOrContentAnywhere(t *testing.T) {
	e := newEnv(t, "LGAI_UPSTREAM_API_KEY", upstreamKey)
	key, id := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{Chunks: []string{"COMPLETION-CANARY"}})
	e.user(key, "POST", "/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":"PROMPT-CANARY"}]}`)
	e.user(key, "POST", "/v1/tasks/fix", `{"language":"go","code":"PROMPT-CANARY","error":"PROMPT-CANARY"}`)
	e.up.Set(testupstream.Behaviour{Status: 500, ErrorMessage: "UPSTREAM-ERROR-CANARY"})
	e.user(key, "POST", "/v1/chat/completions", chatBody)
	e.up.Set(testupstream.Behaviour{MidError: true})
	e.user(key, "POST", "/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":"x"}]}`)
	e.adm("DELETE", fmt.Sprintf("/admin/keys/%d", id), "")
	e.user(key, "GET", "/v1/models", "")

	secrets := append(hashForms(key), adminToken, upstreamKey, "PROMPT-CANARY", "COMPLETION-CANARY", "UPSTREAM-ERROR-CANARY", "CANARY-UPSTREAM-ERROR")
	sources := map[string]string{"log": e.logs.String()}
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(e.dbPath + suffix)
		if err != nil && suffix == "" {
			t.Fatal(err)
		}
		sources["db"+suffix] = string(b)
	}
	if !strings.Contains(sources["log"], `"msg":"request"`) {
		t.Fatal("no access log lines captured")
	}
	for name, content := range sources {
		for _, s := range secrets {
			if strings.Contains(content, s) {
				t.Errorf("%s contains %q", name, s)
			}
		}
	}
}

// TestSlowReaderReleasesSlot: a client that stops reading is cut off by the
// per-write deadline; its slot is released, the upstream request cancelled
// and the row recorded as 499.
func TestSlowReaderReleasesSlot(t *testing.T) {
	e := newEnv(t, "LGAI_WRITE_TIMEOUT", "200ms")
	key, _ := e.mint(`{"name":"lab-01"}`)
	// 10 000 events of 2 KB: far more than the socket buffers hold.
	e.up.Set(testupstream.Behaviour{Chunks: repeatChunks(strings.Repeat("y", 2000), 10000)})
	srv := httptest.NewServer(e.pub)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	body := `{"stream":true,"messages":[{"role":"user","content":"x"}]}`
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		key, len(body), body)
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("status line %q %v", status, err)
	}
	// Stop reading. The gateway must give up within about WRITE_TIMEOUT.
	deadline := time.Now().Add(15 * time.Second)
	for {
		rows := e.usage()
		in, _ := e.s.gate.Stats()
		if len(rows) == 1 && in == 0 {
			if rows[0].Status != 499 {
				t.Errorf("usage status %d", rows[0].Status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not released: inflight=%d rows=%v", in, rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for e.up.Cancelled() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if e.up.Cancelled() == 0 {
		t.Error("upstream request was not cancelled")
	}
}

func repeatChunks(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestMethodNotAllowedJSON(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.pub, "DELETE", "/v1/models", "")
	expect(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if a := rec.Header().Get("Allow"); !strings.Contains(a, "GET") {
		t.Errorf("Allow %q", a)
	}
	expect(t, e.adm("PATCH", "/admin/maintenance", `{}`), http.StatusMethodNotAllowed, "method_not_allowed")
}

// TestSlowBody: a body trickled in slower than LGAI_BODY_READ_TIMEOUT is cut
// off with 408; no gate slot is taken and nothing reaches the upstream.
func TestSlowBody(t *testing.T) {
	e := newEnv(t, "LGAI_BODY_READ_TIMEOUT", "300ms")
	key, _ := e.mint(`{"name":"lab-01"}`)
	srv := httptest.NewServer(e.pub)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Length: 100\r\n\r\n{", key)
	go func() {
		for range 20 {
			time.Sleep(100 * time.Millisecond)
			if _, err := conn.Write([]byte(" ")); err != nil {
				return
			}
		}
	}()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(status, "408") {
		t.Fatalf("status line %q %v", status, err)
	}
	if rows := e.usage(); len(rows) != 0 {
		t.Errorf("usage rows for a refused request: %+v", rows)
	}
	if len(e.up.Requests()) != 0 {
		t.Error("upstream called")
	}
}

// TestUnauthenticatedBodyNotAwaited: a 401 for a request whose body never
// finishes arrives at once, and the connection is closed.
func TestUnauthenticatedBodyNotAwaited(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.pub)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nContent-Length: 100000\r\n\r\n{")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || !resp.Close {
		t.Errorf("status %d, close %v", resp.StatusCode, resp.Close)
	}
	// The server must also drop the connection, not keep draining the body
	// the client never finishes (net/http reads up to 256 KiB otherwise).
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("connection still open after the 401: %v", err)
	}
}
