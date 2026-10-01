package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-generative-ai/internal/api"
	"local-generative-ai/internal/auth"
	"local-generative-ai/internal/config"
	"local-generative-ai/internal/store"
	"local-generative-ai/internal/testupstream"
	"local-generative-ai/internal/upstream"
)

// gateway serves the real public handler behind the connection cap and
// returns its address and two fresh keys.
func gateway(t *testing.T, kv ...string) (addr, keyA, keyB string) {
	t.Helper()
	up := testupstream.New(t)
	vars := map[string]string{"LGAI_MODELS": "coder=up-model", "LGAI_UPSTREAM_BASE_URL": up.BaseURL(),
		"LGAI_DB_PATH": filepath.Join(t.TempDir(), "gw.db")}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	cfg, _, err := config.Load(func(k string) string { return vars[k] }, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var keys [2]string
	var news []store.NewKey
	for i := range keys {
		plain, hash, prefix, _ := auth.Generate()
		keys[i] = plain
		news = append(news, store.NewKey{Name: fmt.Sprintf("lab-%02d", i+1), Prefix: prefix, Hash: hash})
	}
	if _, err := st.CreateKeys(context.Background(), news, time.Now()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	srv := api.New(api.Deps{Cfg: cfg, Store: st, Upstream: upstream.New(cfg.Upstream, 4, time.Second), Log: log})
	return serve(t, cfg, cfg.HTTP.MaxConns, srv.PublicHandler()), keys[0], keys[1]
}

// TestSlowBodiesCannotFillConnCap is security-code V1: one key opens
// 2 × LGAI_MAX_CONNS connections that send headers and then stall in the body.
// Its in-flight slot is taken before the body is read, so all but one get an
// immediate 429 and their connections close; another key is still served.
func TestSlowBodiesCannotFillConnCap(t *testing.T) {
	const maxConns = 4
	addr, attacker, victim := gateway(t, "LGAI_MAX_CONNS", fmt.Sprint(maxConns), "LGAI_KEY_MAX_INFLIGHT", "1")
	for range 2 * maxConns {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprintf(c, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\n"+
			"Content-Type: application/json\r\nContent-Length: 1000\r\n\r\n{", attacker)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("POST", "http://"+addr+"/v1/chat/completions",
		strings.NewReader(`{"model":"coder","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+victim)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("other key not served while one key stalls bodies: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("other key got %d", resp.StatusCode)
	}
}
