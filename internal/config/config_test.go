package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodToken = "3q2+7wAAAAB5dGVzdCB0b2tlbiBmb3IgbGdhaSBnYXRld2F5ISE="

func env(kv ...string) (func(string) string, []string) {
	m := map[string]string{"LGAI_MODELS": "coder=gpt-oss:20b"}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	var environ []string
	for k, v := range m {
		environ = append(environ, k+"="+v)
	}
	return func(k string) string { return m[k] }, environ
}

func load(t *testing.T, kv ...string) (Config, []string, error) {
	t.Helper()
	getenv, environ := env(kv...)
	return Load(getenv, environ)
}

func TestDefaultsAreLaptopColumn(t *testing.T) {
	c, warns, err := load(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings: %v", warns)
	}
	if _, warns, _ := load(t, "LGAI_UPSTREAM_BASE_URL", "http://127.0.0.1:11434"); len(warns) != 1 || !strings.Contains(warns[0], "/v1") {
		t.Errorf("missing /v1 not warned: %v", warns)
	}
	checks := map[string][2]any{
		"addr":           {c.Addr, "127.0.0.1:8080"},
		"admin_addr":     {c.AdminAddr, "127.0.0.1:8081"},
		"max_inflight":   {c.Limits.MaxInflight, 4},
		"queue":          {c.Limits.QueueSize, 16},
		"queue_timeout":  {c.Limits.QueueTimeout, 60 * time.Second},
		"retry_after":    {c.Limits.OverloadRetryAfter, 10 * time.Second},
		"rpm":            {c.Limits.RateRPM, 6},
		"burst":          {c.Limits.RateBurst, 3},
		"key_inflight":   {c.Limits.KeyMaxInflight, 1},
		"quota":          {c.Limits.TokenQuotaDaily, int64(300000)},
		"chat_default":   {c.Gen.ChatDefaultMaxTokens, 512},
		"cap":            {c.Gen.MaxTokensCap, 1024},
		"nonstream":      {c.Gen.NonStreamMaxTokens, 768},
		"task_input":     {c.Gen.TaskMaxInputBytes, 16384},
		"chat_input":     {c.Gen.ChatMaxInputBytes, 16384},
		"body":           {c.HTTP.MaxBodyBytes, int64(262144)},
		"header_timeout": {c.Upstream.HeaderTimeout, 120 * time.Second},
		"idle_timeout":   {c.Upstream.IdleTimeout, 60 * time.Second},
		"total_timeout":  {c.Upstream.TotalTimeout, 10 * time.Minute},
		"write_timeout":  {c.HTTP.WriteTimeout, 10 * time.Second},
		"body_timeout":   {c.HTTP.BodyReadTimeout, 10 * time.Second},
		"max_conns":      {c.HTTP.MaxConns, 256},
		"key_ttl":        {c.KeyTTL, time.Duration(0)},
		"upstream":       {c.Upstream.BaseURL.String(), "http://127.0.0.1:11434/v1"},
	}
	for name, v := range checks {
		if v[0] != v[1] {
			t.Errorf("%s = %v, want %v", name, v[0], v[1])
		}
	}
	if c.AdminEnabled() {
		t.Error("admin enabled without a token")
	}
}

// TestNoUnlimitedDefaults: no limit defaults to 0 (= unlimited) except the
// documented key TTL.
func TestNoUnlimitedDefaults(t *testing.T) {
	c, _, err := load(t)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]int64{
		"rpm": int64(c.Limits.RateRPM), "key_inflight": int64(c.Limits.KeyMaxInflight), "quota": c.Limits.TokenQuotaDaily,
		"queue": int64(c.Limits.QueueSize), "cap": int64(c.Gen.MaxTokensCap),
	} {
		if v == 0 {
			t.Errorf("%s defaults to 0 (unlimited)", name)
		}
	}
}

func TestAdminTokenValidation(t *testing.T) {
	for _, bad := range []string{
		goodToken[:42], // high entropy, one char short
		"replace-with-output-of-openssl-rand-base64-32",
		"replace-me",
		strings.Repeat("a", 43),
		"abcdefghijklmnopqrstuvwxyz0123456789changeme",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-admin-xyz",
	} {
		for _, name := range []string{"LGAI_ADMIN_TOKEN", "LGAI_UPSTREAM_API_KEY"} {
			if _, _, err := load(t, name, bad); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s=%q accepted (err %v)", name, bad, err)
			} else if strings.Contains(err.Error(), bad) {
				t.Errorf("error echoes the secret: %v", err)
			}
		}
	}
	for i := range 20 {
		var b [32]byte
		_, _ = rand.Read(b[:])
		s := base64.StdEncoding.EncodeToString(b[:])
		if ValidateSecret(s) != nil {
			if strings.Contains(strings.ToLower(s), "admin") || strings.Contains(strings.ToLower(s), "secret") {
				continue // astronomically rare, but a correct rejection
			}
			t.Errorf("random secret %d rejected", i)
		}
	}
	c, _, err := load(t, "LGAI_ADMIN_TOKEN", goodToken)
	if err != nil || !c.AdminEnabled() {
		t.Fatalf("good token refused: %v", err)
	}
}

func TestSecretFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(goodToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, err := load(t, "LGAI_ADMIN_TOKEN_FILE", path)
	if err != nil || c.AdminToken != goodToken {
		t.Fatalf("got %q, %v", c.AdminToken, err)
	}
	if _, _, err := load(t, "LGAI_ADMIN_TOKEN_FILE", path, "LGAI_ADMIN_TOKEN", goodToken); err == nil {
		t.Error("both NAME and NAME_FILE accepted")
	}
}

// TestUnauthenticatedPublicUpstreamRefused is security #8.
func TestUnauthenticatedPublicUpstreamRefused(t *testing.T) {
	if _, _, err := load(t, "LGAI_UPSTREAM_BASE_URL", "https://example.com/v1"); err == nil {
		t.Error("public upstream without auth accepted")
	}
	for _, kv := range [][]string{
		{"LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED", "true"},
		{"LGAI_UPSTREAM_API_KEY", goodToken},
		{"LGAI_UPSTREAM_HEADERS", "Modal-Key=wk-1;Modal-Secret=ws-2"},
	} {
		if _, _, err := load(t, append([]string{"LGAI_UPSTREAM_BASE_URL", "https://example.com/v1"}, kv...)...); err != nil {
			t.Errorf("%v: %v", kv, err)
		}
	}
	for _, u := range []string{"http://127.0.0.1:11434/v1", "http://host.docker.internal:11434/v1", "http://vllm:8000/v1",
		"http://10.1.2.3/v1", "http://[::1]:8000/v1", "http://localhost/v1"} {
		if _, _, err := load(t, "LGAI_UPSTREAM_BASE_URL", u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestUpstreamHeaders(t *testing.T) {
	c, _, err := load(t, "LGAI_UPSTREAM_HEADERS", " Modal-Key = wk-1 ; Modal-Secret=ws-2;")
	if err != nil {
		t.Fatal(err)
	}
	if c.Upstream.Headers.Get("Modal-Key") != "wk-1" || c.Upstream.Headers.Get("Modal-Secret") != "ws-2" {
		t.Errorf("headers = %v", c.Upstream.Headers)
	}
	for _, bad := range []string{"NoEquals", "Bad Name=x", "Host=evil", "Content-Type=x", "X-Request-ID=1",
		"Modal-Key=a\x01b", "Modal-Key=a\x7fb", "Modal-Key=a\x00b", "Modal-Key=a\x1bb", "Modal-Key=a\rb"} {
		if _, _, err := load(t, "LGAI_UPSTREAM_HEADERS", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if c, _, err := load(t, "LGAI_UPSTREAM_HEADERS", "Modal-Key=a\tb"); err != nil || c.Upstream.Headers.Get("Modal-Key") != "a\tb" {
		t.Errorf("tab inside a value refused: %v", err)
	}
	if _, _, err := load(t, "LGAI_UPSTREAM_HEADERS", "Modal-Key=a\x01b"); err == nil || !strings.Contains(err.Error(), "control character") {
		t.Errorf("control character error unclear: %v", err)
	}
	if _, _, err := load(t, "LGAI_UPSTREAM_HEADERS", "Authorization=Bearer x", "LGAI_UPSTREAM_API_KEY", goodToken); err == nil {
		t.Error("Authorization accepted twice")
	}
}

func TestModelsAndDefaults(t *testing.T) {
	c, _, err := load(t, "LGAI_MODELS", "coder=gpt-oss:20b, fast=qwen2.5-coder:7b,llama3",
		"LGAI_MODEL_DEFAULTS", `coder={"reasoning_effort":"low"}; fast={"chat_template_kwargs":{"enable_thinking":false},"top_k":20}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) != 3 || c.Models[2].Alias != "llama3" || c.Models[2].Upstream != "llama3" {
		t.Fatalf("models = %+v", c.Models)
	}
	if m, ok := c.ResolveModel(""); !ok || m.Alias != "coder" || m.Defaults.ReasoningEffort != "low" {
		t.Errorf("default model = %+v", m)
	}
	if m, _ := c.ResolveModel("fast"); m.Defaults.EnableThinking == nil || *m.Defaults.TopK != 20 {
		t.Errorf("fast defaults = %+v", m.Defaults)
	}
	if _, ok := c.ResolveModel("gpt-4o"); ok {
		t.Error("unknown alias resolved")
	}
	for _, kv := range [][]string{
		{"LGAI_MODELS", ""},
		{"LGAI_MODELS", "a=x,a=y"},
		{"LGAI_MODEL_DEFAULTS", `nope={"reasoning_effort":"low"}`},
		{"LGAI_MODEL_DEFAULTS", `coder={"keep_alive":-1}`},
		{"LGAI_MODEL_DEFAULTS", `coder={"reasoning_effort":"low"} junk`},
	} {
		if _, _, err := load(t, kv...); err == nil {
			t.Errorf("%v accepted", kv)
		}
	}
}

func TestInvalidValuesAllReported(t *testing.T) {
	_, _, err := load(t, "LGAI_MAX_INFLIGHT", "0", "LGAI_QUEUE_TIMEOUT", "soon", "LGAI_CHAT_DEFAULT_MAX_TOKENS", "4096",
		"LGAI_ADMIN_ON_PUBLIC", "true", "LGAI_DB_PATH", "x.db?mode=memory", "LGAI_LOG_LEVEL", "loud")
	if err == nil {
		t.Fatal("accepted")
	}
	for _, name := range []string{"LGAI_MAX_INFLIGHT", "LGAI_QUEUE_TIMEOUT", "LGAI_CHAT_DEFAULT_MAX_TOKENS",
		"LGAI_ADMIN_ON_PUBLIC", "LGAI_DB_PATH", "LGAI_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention %s: %v", name, err)
		}
	}
}

func TestConfigLogValue(t *testing.T) {
	c, warns, err := load(t, "LGAI_ADMIN_TOKEN", goodToken, "LGAI_UPSTREAM_BASE_URL", "https://u:p@h.example/v1?token=x",
		"LGAI_UPSTREAM_HEADERS", "Modal-Secret=hdr-secret-value", "LGAI_ADMIN_TOKN", "secretvalue",
		"LGAI_KEY", "lgai_loadtest") // the load tester's variable is not a typo
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("starting", "config", c)
	out := buf.String()
	for _, leak := range []string{goodToken, "u:p", "token=x", "hdr-secret-value"} {
		if strings.Contains(out, leak) {
			t.Errorf("config log contains %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"admin_token":"set"`) || !strings.Contains(out, `"api_key":"unset"`) {
		t.Errorf("secrets not shown as set/unset: %s", out)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "LGAI_ADMIN_TOKN") || strings.Contains(warns[0], "secretvalue") {
		t.Errorf("warnings = %v", warns)
	}
}
