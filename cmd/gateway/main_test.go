package main

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local-generative-ai/internal/auth"
	"local-generative-ai/internal/config"
	"local-generative-ai/internal/store"
)

func serve(t *testing.T, cfg config.Config, maxConns int, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(cfg, h, slog.New(slog.DiscardHandler))
	go func() { _ = srv.Serve(limitListen(ln, maxConns)) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

func testConfig() config.Config {
	var c config.Config
	c.HTTP.ReadHeaderTimeout, c.HTTP.IdleTimeout = 200*time.Millisecond, time.Minute
	return c
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
}

// TestConnCap is security #17: the (N+1)th connection is not served until
// one of the N closes.
func TestConnCap(t *testing.T) {
	addr := serve(t, testConfig(), 1, okHandler())
	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	get := func(c net.Conn) {
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	}
	get(first)
	if line, _ := bufio.NewReader(first).ReadString('\n'); !strings.Contains(line, "200") {
		t.Fatalf("first: %q", line)
	}
	second, err := net.Dial("tcp", addr) // the kernel accepts; the server must not
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	get(second)
	_ = second.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("second connection served while the cap was reached")
	}
	_ = first.Close()
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	if line, _ := bufio.NewReader(second).ReadString('\n'); !strings.Contains(line, "200") {
		t.Fatalf("second after close: %q", line)
	}
}

func TestSlowHeaders(t *testing.T) {
	addr := serve(t, testConfig(), 8, okHandler())
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost:")
	start := time.Now()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.ReadAll(c) // returns when the server closes the connection
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("connection held for %s", d)
	}
}

func TestOversizedHeaders(t *testing.T) {
	var called atomic.Bool
	addr := serve(t, testConfig(), 8, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
	req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	req.Header.Set("X-Big", strings.Repeat("a", 20<<10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge || called.Load() {
		t.Errorf("status %d, handler called %v", resp.StatusCode, called.Load())
	}
}

func TestHealthcheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	if code := healthcheck(":" + port); code != 0 {
		t.Errorf("healthy → %d", code)
	}
	srv.Close()
	if code := healthcheck("0.0.0.0:" + port); code != 1 {
		t.Errorf("down → %d", code)
	}
	if code := healthcheck("nonsense"); code != 1 {
		t.Errorf("bad addr → %d", code)
	}
}

func TestGenSecretPassesValidation(t *testing.T) {
	var b strings.Builder
	if genSecret(&b) != 0 || config.ValidateSecret(strings.TrimSpace(b.String())) != nil {
		t.Errorf("secret %q", b.String())
	}
}

func TestGenKeysRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out, hashes := filepath.Join(dir, "keys.csv"), filepath.Join(dir, "keys.hashes")
	args := []string{"-n", "3", "-prefix", "lab-", "-expires", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "-out", out, "-hashes", hashes}
	if code := genKeys(args, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{out, hashes} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, fi.Mode(), err)
		}
	}
	plain, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(plain)), "\n")
	if len(lines) != 4 || lines[0] != "name,key" {
		t.Fatalf("csv %q", plain)
	}
	hf, _ := os.Open(hashes)
	defer hf.Close()
	keys, expired, err := store.ParseKeysFile(hf, time.Now())
	if err != nil || len(keys) != 3 || expired != 0 {
		t.Fatalf("parse: %v %d %v", keys, expired, err)
	}
	hashed, _ := os.ReadFile(hashes)
	for i, line := range lines[1:] {
		name, key, _ := strings.Cut(line, ",")
		if keys[i].Name != name || keys[i].Hash != auth.Hash(key) || !auth.WellFormed(key) || strings.Contains(string(hashed), key) {
			t.Errorf("key %d mismatch", i)
		}
	}
	if code := genKeys(args, io.Discard); code != 1 {
		t.Error("existing files overwritten")
	}
	if code := genKeys([]string{"-n", "0", "-prefix", "x"}, io.Discard); code != 1 {
		t.Error("n=0 accepted")
	}
}
