// Package config turns LGAI_* environment variables into a validated Config.
//
// Defaults are the laptop column of docs/DECISIONS.md D6 (Ollama on an M4 Pro).
// The server (L40S, vLLM) values are noted next to each default. Any invalid
// value fails startup with a message naming the variable.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"local-generative-ai/internal/oai"
)

// Config is the effective gateway configuration.
type Config struct {
	Addr          string // LGAI_ADDR: public listener
	AdminAddr     string // LGAI_ADMIN_ADDR: admin listener (loopback by default)
	AdminOnPublic bool   // LGAI_ADMIN_ON_PUBLIC: also mount /admin on the public listener
	AdminToken    string // LGAI_ADMIN_TOKEN[_FILE]: "" disables the admin API
	DBPath        string // LGAI_DB_PATH
	LogLevel      slog.Level
	Models        []oai.Model   // LGAI_MODELS (+ LGAI_MODEL_DEFAULTS); Models[0] is the default
	KeyTTL        time.Duration // LGAI_KEY_TTL: default key lifetime, 0 = no expiry
	KeysFile      string        // LGAI_KEYS_FILE: key hashes imported at startup
	Upstream      Upstream
	Limits        Limits
	Gen           Gen
	HTTP          HTTP
}

// Upstream configures the OpenAI-compatible model server.
type Upstream struct {
	BaseURL              *url.URL    // LGAI_UPSTREAM_BASE_URL, including /v1
	HealthURL            *url.URL    // LGAI_UPSTREAM_HEALTH_URL; nil → BaseURL + /models
	APIKey               string      // LGAI_UPSTREAM_API_KEY[_FILE]
	Headers              http.Header // LGAI_UPSTREAM_HEADERS[_FILE]
	AllowUnauthenticated bool        // LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED
	HeaderTimeout        time.Duration
	IdleTimeout          time.Duration
	TotalTimeout         time.Duration
}

// Limits are the per-key and global admission limits. 0 disables a per-key
// limit; MaxInflight is always ≥ 1.
type Limits struct {
	MaxInflight        int
	QueueSize          int
	QueueTimeout       time.Duration
	OverloadRetryAfter time.Duration
	RateRPM            int
	RateBurst          int
	KeyMaxInflight     int
	TokenQuotaDaily    int64
}

// Gen holds generation defaults and input caps.
type Gen struct {
	ChatDefaultMaxTokens int
	MaxTokensCap         int
	NonStreamMaxTokens   int
	TaskMaxInputBytes    int
	ChatMaxInputBytes    int
}

// HTTP holds server-side HTTP settings.
type HTTP struct {
	MaxBodyBytes      int64
	ReadHeaderTimeout time.Duration
	BodyReadTimeout   time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxConns          int
}

// ResolveModel maps a public alias to its model; "" selects the default.
func (c Config) ResolveModel(alias string) (oai.Model, bool) {
	if alias == "" {
		return c.Models[0], true
	}
	for _, m := range c.Models {
		if m.Alias == alias {
			return m, true
		}
	}
	return oai.Model{}, false
}

// AdminEnabled reports whether the admin API is served at all.
func (c Config) AdminEnabled() bool { return c.AdminToken != "" }

// Load reads the configuration from getenv. environ is used only to warn about
// unknown LGAI_* names (the warnings contain names, never values).
func Load(getenv func(string) string, environ []string) (Config, []string, error) {
	p := &parser{getenv: getenv, known: map[string]bool{}}
	var c Config

	c.Addr = p.str("LGAI_ADDR", "127.0.0.1:8080") // loopback on the laptop (security #23); containers set :8080
	c.AdminAddr = p.str("LGAI_ADMIN_ADDR", "127.0.0.1:8081")
	c.AdminOnPublic = p.bool("LGAI_ADMIN_ON_PUBLIC", false)
	c.AdminToken = p.secret("LGAI_ADMIN_TOKEN")
	c.DBPath = p.str("LGAI_DB_PATH", "data/gateway.db")
	c.LogLevel = p.level("LGAI_LOG_LEVEL", slog.LevelInfo)
	c.KeyTTL = p.dur("LGAI_KEY_TTL", 0, 0)
	c.KeysFile = p.str("LGAI_KEYS_FILE", "")
	c.Models = p.models("LGAI_MODELS", "LGAI_MODEL_DEFAULTS")

	u := &c.Upstream
	u.BaseURL = p.url("LGAI_UPSTREAM_BASE_URL", "http://127.0.0.1:11434/v1")
	u.HealthURL = p.url("LGAI_UPSTREAM_HEALTH_URL", "")
	u.APIKey = p.secret("LGAI_UPSTREAM_API_KEY")
	u.Headers = p.headers("LGAI_UPSTREAM_HEADERS", u.APIKey != "")
	u.AllowUnauthenticated = p.bool("LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED", false)
	u.HeaderTimeout = p.dur("LGAI_UPSTREAM_HEADER_TIMEOUT", 120*time.Second, time.Second) // server: 60s
	u.IdleTimeout = p.dur("LGAI_UPSTREAM_IDLE_TIMEOUT", 60*time.Second, time.Second)      // server: 30s
	u.TotalTimeout = p.dur("LGAI_UPSTREAM_TIMEOUT", 10*time.Minute, time.Second)          // server: 5m

	l := &c.Limits
	l.MaxInflight = p.int("LGAI_MAX_INFLIGHT", 4, 1)                               // server: 30
	l.QueueSize = p.int("LGAI_QUEUE_SIZE", 16, 0)                                  // server: 24
	l.QueueTimeout = p.dur("LGAI_QUEUE_TIMEOUT", 60*time.Second, time.Millisecond) // server: 30s
	l.OverloadRetryAfter = p.dur("LGAI_OVERLOAD_RETRY_AFTER", 10*time.Second, time.Second)
	l.RateRPM = p.int("LGAI_RATE_RPM", 6, 0)
	l.RateBurst = p.int("LGAI_RATE_BURST", 3, 1)
	l.KeyMaxInflight = p.int("LGAI_KEY_MAX_INFLIGHT", 1, 0) // server: 2
	l.TokenQuotaDaily = int64(p.int("LGAI_TOKEN_QUOTA_DAILY", 300000, 0))

	g := &c.Gen
	g.ChatDefaultMaxTokens = p.int("LGAI_CHAT_DEFAULT_MAX_TOKENS", 512, 1)
	g.MaxTokensCap = p.int("LGAI_MAX_TOKENS_CAP", 1024, 1)            // server: 1536
	g.NonStreamMaxTokens = p.int("LGAI_NONSTREAM_MAX_TOKENS", 768, 1) // server: 1024
	g.TaskMaxInputBytes = p.int("LGAI_TASK_MAX_INPUT_BYTES", 16384, 1)
	// 16 KiB of code is about 4.7k tokens; with LGAI_MAX_TOKENS_CAP (≤ 2048)
	// it fits an 8192-token context (review m5).
	g.ChatMaxInputBytes = p.int("LGAI_CHAT_MAX_INPUT_BYTES", 16384, 1)

	h := &c.HTTP
	h.MaxBodyBytes = int64(p.int("LGAI_MAX_BODY_BYTES", 262144, 1024))
	h.ReadHeaderTimeout = p.dur("LGAI_READ_HEADER_TIMEOUT", 10*time.Second, time.Millisecond)
	// A 256 KiB body arrives in well under 1 s on any real uplink; a short
	// timeout bounds how long a slow body can hold a connection (security-code V1).
	h.BodyReadTimeout = p.dur("LGAI_BODY_READ_TIMEOUT", 10*time.Second, time.Millisecond)
	h.WriteTimeout = p.dur("LGAI_WRITE_TIMEOUT", 10*time.Second, time.Millisecond)
	h.IdleTimeout = p.dur("LGAI_IDLE_TIMEOUT", 120*time.Second, time.Second)
	h.ShutdownTimeout = p.dur("LGAI_SHUTDOWN_TIMEOUT", 30*time.Second, time.Second)
	h.MaxConns = p.int("LGAI_MAX_CONNS", 256, 1)

	p.validate(&c)
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, nil, err
	}
	warnings := p.unknown(environ)
	if !strings.HasSuffix(c.Upstream.BaseURL.Path, "/v1") {
		warnings = append(warnings, "LGAI_UPSTREAM_BASE_URL does not end in /v1; "+
			"OpenAI-compatible servers (Ollama, vLLM, llama.cpp) serve under /v1")
	}
	return c, warnings, nil
}

// validate checks the rules that span several variables.
func (p *parser) validate(c *Config) {
	for _, a := range [][2]string{{"LGAI_ADDR", c.Addr}, {"LGAI_ADMIN_ADDR", c.AdminAddr}} {
		if _, _, err := net.SplitHostPort(a[1]); err != nil {
			p.fail(a[0], "must be host:port")
		}
	}
	if c.AdminOnPublic && c.AdminToken == "" {
		p.fail("LGAI_ADMIN_ON_PUBLIC", "requires LGAI_ADMIN_TOKEN")
	}
	if c.DBPath == "" || strings.ContainsAny(c.DBPath, "?#") {
		p.fail("LGAI_DB_PATH", "must be a file path without '?' or '#'")
	}
	if c.Gen.ChatDefaultMaxTokens > c.Gen.MaxTokensCap {
		p.fail("LGAI_CHAT_DEFAULT_MAX_TOKENS", "must not exceed LGAI_MAX_TOKENS_CAP")
	}
	if u := c.Upstream; u.BaseURL != nil && !u.AllowUnauthenticated &&
		u.APIKey == "" && len(u.Headers) == 0 && u.BaseURL.User == nil && !privateHost(u.BaseURL.Hostname()) {
		p.fail("LGAI_UPSTREAM_BASE_URL", "is a public host without upstream auth; set LGAI_UPSTREAM_API_KEY or "+
			"LGAI_UPSTREAM_HEADERS, or LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED=true (security #8)")
	}
}

// privateHost reports whether host is loopback or on a private network. IP
// literals are checked exactly. Names are judged without DNS: localhost,
// single-label names (Docker service names such as "vllm") and the .internal,
// .local and .localhost suffixes (e.g. host.docker.internal) count as private.
func privateHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	if host == "localhost" || (host != "" && !strings.Contains(host, ".")) {
		return true
	}
	for _, suffix := range []string{".localhost", ".internal", ".local"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// placeholderWords must never appear in a secret (security #2).
var placeholderWords = []string{"replace", "example", "changeme", "secret", "password", "admin"}

// ValidateSecret enforces the admin-token and upstream-key rules: at least 43
// characters (32 random bytes in base64), at least 16 distinct characters, and
// none of the placeholder words.
func ValidateSecret(v string) error {
	if len(v) < 43 {
		return errors.New("must be at least 43 characters (use: openssl rand -base64 32)")
	}
	distinct := map[rune]bool{}
	for _, r := range v {
		distinct[r] = true
	}
	if len(distinct) < 16 {
		return errors.New("must contain at least 16 distinct characters (use: openssl rand -base64 32)")
	}
	lower := strings.ToLower(v)
	for _, w := range placeholderWords {
		if strings.Contains(lower, w) {
			return errors.New("looks like a placeholder; generate one with: openssl rand -base64 32")
		}
	}
	return nil
}

// LogValue renders the config for the startup log. Secrets appear only as
// "set"/"unset"; URLs lose their userinfo and query.
func (c Config) LogValue() slog.Value {
	set := func(s string) string {
		if s == "" {
			return "unset"
		}
		return "set"
	}
	models := make([]string, len(c.Models))
	for i, m := range c.Models {
		models[i] = m.Alias + "=" + m.Upstream
		if d := m.Defaults.String(); d != "{}" {
			models[i] += " " + d
		}
	}
	admin := c.AdminAddr
	if !c.AdminEnabled() {
		admin = "disabled"
	}
	return slog.GroupValue(
		slog.String("addr", c.Addr),
		slog.String("admin_addr", admin),
		slog.Bool("admin_on_public", c.AdminOnPublic),
		slog.String("admin_token", set(c.AdminToken)),
		slog.String("db_path", c.DBPath),
		slog.String("log_level", c.LogLevel.String()),
		slog.Any("models", models),
		durAttr("key_ttl", c.KeyTTL),
		slog.String("keys_file", c.KeysFile),
		slog.Group("upstream",
			slog.String("base_url", redactURL(c.Upstream.BaseURL)),
			slog.String("health_url", redactURL(c.Upstream.HealthURL)),
			slog.String("api_key", set(c.Upstream.APIKey)),
			slog.Int("extra_headers", len(c.Upstream.Headers)),
			slog.Bool("allow_unauthenticated", c.Upstream.AllowUnauthenticated),
			durAttr("header_timeout", c.Upstream.HeaderTimeout),
			durAttr("idle_timeout", c.Upstream.IdleTimeout),
			durAttr("timeout", c.Upstream.TotalTimeout)),
		slog.Group("limits",
			slog.Int("max_inflight", c.Limits.MaxInflight),
			slog.Int("queue_size", c.Limits.QueueSize),
			durAttr("queue_timeout", c.Limits.QueueTimeout),
			durAttr("overload_retry_after", c.Limits.OverloadRetryAfter),
			slog.Int("rate_rpm", c.Limits.RateRPM),
			slog.Int("rate_burst", c.Limits.RateBurst),
			slog.Int("key_max_inflight", c.Limits.KeyMaxInflight),
			slog.Int64("token_quota_daily", c.Limits.TokenQuotaDaily)),
		slog.Group("gen",
			slog.Int("chat_default_max_tokens", c.Gen.ChatDefaultMaxTokens),
			slog.Int("max_tokens_cap", c.Gen.MaxTokensCap),
			slog.Int("nonstream_max_tokens", c.Gen.NonStreamMaxTokens),
			slog.Int("task_max_input_bytes", c.Gen.TaskMaxInputBytes),
			slog.Int("chat_max_input_bytes", c.Gen.ChatMaxInputBytes)),
		slog.Group("http",
			slog.Int64("max_body_bytes", c.HTTP.MaxBodyBytes),
			durAttr("read_header_timeout", c.HTTP.ReadHeaderTimeout),
			durAttr("body_read_timeout", c.HTTP.BodyReadTimeout),
			durAttr("write_timeout", c.HTTP.WriteTimeout),
			durAttr("idle_timeout", c.HTTP.IdleTimeout),
			durAttr("shutdown_timeout", c.HTTP.ShutdownTimeout),
			slog.Int("max_conns", c.HTTP.MaxConns)),
	)
}

func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	r := *u
	r.User, r.RawQuery, r.ForceQuery, r.Fragment, r.RawFragment = nil, "", false, "", ""
	return r.String()
}

// readSecretFile reads a *_FILE secret, trimming one trailing newline.
func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// otherTools are LGAI_* names used by other tools of this repo, not the
// gateway (LGAI_KEY: the load tester's API key, DECISIONS D8).
var otherTools = map[string]bool{"LGAI_KEY": true}

// unknown lists LGAI_* variables that Load never read (names only, sorted).
func (p *parser) unknown(environ []string) []string {
	var names []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "LGAI_") && !p.known[name] && !otherTools[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	warnings := make([]string, len(names))
	for i, n := range names {
		warnings[i] = fmt.Sprintf("unknown variable %s is ignored (typo?)", n)
	}
	return warnings
}

// durAttr logs a duration as "30s" rather than nanoseconds.
func durAttr(key string, d time.Duration) slog.Attr { return slog.String(key, d.String()) }
