package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"local-generative-ai/internal/config"
	"local-generative-ai/internal/store"
	"local-generative-ai/internal/testupstream"
	"local-generative-ai/internal/upstream"
)

const (
	adminToken  = "3q2+7wAAAAB5dGVzdCB0b2tlbiBmb3IgbGdhaSBnYXRld2F5ISE="
	upstreamKey = "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MEFCQ0RFRkdISUpLTE1O"
)

// env is one gateway wired to a temp SQLite file and a fake upstream.
type env struct {
	t      *testing.T
	s      *Server
	up     *testupstream.Server
	st     *store.Store
	pub    http.Handler
	admin  http.Handler
	logs   *syncBuffer
	dbPath string
	offset atomic.Int64 // added to time.Now (for expiry tests)
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newEnv builds a gateway; kv are LGAI_* overrides. Per-key limits are off
// unless a test turns them on.
func newEnv(t *testing.T, kv ...string) *env {
	t.Helper()
	e := &env{t: t, up: testupstream.New(t), logs: &syncBuffer{}}
	e.dbPath = filepath.Join(t.TempDir(), "gateway.db")
	vars := map[string]string{
		"LGAI_MODELS": "coder=up-model", "LGAI_UPSTREAM_BASE_URL": e.up.BaseURL(), "LGAI_ADMIN_TOKEN": adminToken,
		"LGAI_DB_PATH": e.dbPath, "LGAI_RATE_RPM": "0", "LGAI_KEY_MAX_INFLIGHT": "0", "LGAI_TOKEN_QUOTA_DAILY": "0",
	}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	cfg, _, err := config.Load(func(k string) string { return vars[k] }, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.st, err = store.Open(context.Background(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.st.Close() })
	log := slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	up := upstream.New(cfg.Upstream, cfg.Limits.MaxInflight, cfg.Limits.OverloadRetryAfter)
	e.s = New(Deps{Cfg: cfg, Store: e.st, Upstream: up, Log: log, Now: e.now})
	e.pub, e.admin = e.s.PublicHandler(), e.s.AdminHandler()
	return e
}

func (e *env) now() time.Time { return time.Now().Add(time.Duration(e.offset.Load())) }

func (e *env) advance(d time.Duration) { e.offset.Add(int64(d)) }

func (e *env) do(h http.Handler, method, path, body string, header ...string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Add(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func (e *env) user(key, method, path, body string, header ...string) *httptest.ResponseRecorder {
	return e.do(e.pub, method, path, body, append([]string{"Authorization", "Bearer " + key}, header...)...)
}

func (e *env) adm(method, path, body string) *httptest.ResponseRecorder {
	return e.do(e.admin, method, path, body, "Authorization", "Bearer "+adminToken)
}

// mint creates a key through the admin API and returns it with its id.
func (e *env) mint(body string) (string, int64) {
	e.t.Helper()
	rec := e.adm("POST", "/admin/keys", body)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create key: %d %s", rec.Code, rec.Body)
	}
	var k struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	decodeJSON(e.t, rec, &k)
	return k.Key, k.ID
}

type usageRow struct {
	Endpoint, Model, Language, ErrorCode, RequestID string
	Stream, Status, Prompt, Completion, Estimated   int
	TTFT, Queue                                     sql.NullInt64
}

// usage reads every usage row straight from the database file.
func (e *env) usage() []usageRow {
	e.t.Helper()
	db, err := sql.Open("sqlite", e.dbPath)
	if err != nil {
		e.t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT endpoint, COALESCE(model,''), COALESCE(language,''), COALESCE(error_code,''), request_id,
		stream, status, prompt_tokens, completion_tokens, usage_estimated, ttft_ms, queue_ms FROM usage ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []usageRow
	for rows.Next() {
		var u usageRow
		if err := rows.Scan(&u.Endpoint, &u.Model, &u.Language, &u.ErrorCode, &u.RequestID, &u.Stream, &u.Status,
			&u.Prompt, &u.Completion, &u.Estimated, &u.TTFT, &u.Queue); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, u)
	}
	return out
}

func (e *env) lastUsage() usageRow {
	e.t.Helper()
	rows := e.usage()
	if len(rows) == 0 {
		e.t.Fatal("no usage rows")
	}
	return rows[len(rows)-1]
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var v struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, rec, &v)
	return v.Error.Code
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	if code != "" {
		if got := errCode(t, rec); got != code {
			t.Fatalf("code %q, want %q", got, code)
		}
	}
}

const chatBody = `{"model":"coder","messages":[{"role":"user","content":"hi"}]}`

// ---- auth and routing ----

func TestAllPublicRoutesRequireKey(t *testing.T) {
	e := newEnv(t)
	if len(e.s.authRoutes) != 3 {
		t.Fatalf("auth routes %v", e.s.authRoutes)
	}
	for _, pattern := range e.s.authRoutes {
		method, path, _ := strings.Cut(pattern, " ")
		path = strings.ReplaceAll(path, "{task}", "explain")
		expect(t, e.do(e.pub, method, path, chatBody), http.StatusUnauthorized, "invalid_api_key")
	}
	for _, p := range []string{"/healthz", "/readyz"} {
		if rec := e.do(e.pub, "GET", p, ""); rec.Code != 200 {
			t.Errorf("%s → %d", p, rec.Code)
		}
	}
	if len(e.up.Requests()) != 1 { // the readyz probe only
		t.Errorf("upstream saw %d requests", len(e.up.Requests()))
	}
}

func TestAdminNotOnPublicHandler(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do(e.pub, "GET", "/admin/keys", "", "Authorization", "Bearer "+adminToken), 404, "not_found")
	expect(t, e.adm("GET", "/admin/keys", ""), 200, "")

	on := newEnv(t, "LGAI_ADMIN_ON_PUBLIC", "true")
	expect(t, on.do(on.pub, "GET", "/admin/keys", "", "Authorization", "Bearer "+adminToken), 200, "")
	expect(t, on.do(on.pub, "GET", "/admin/keys", ""), 401, "missing_admin_token")
}

func TestAdminAuth(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	expect(t, e.do(e.admin, "GET", "/admin/keys", ""), 401, "missing_admin_token")
	expect(t, e.do(e.admin, "GET", "/admin/keys", "", "Authorization", "Bearer wrong"), 403, "forbidden")
	expect(t, e.do(e.admin, "GET", "/admin/keys", "", "Authorization", "Bearer "+adminToken+"xyz"), 403, "forbidden")
	expect(t, e.do(e.admin, "GET", "/admin/keys", "", "Authorization", "Bearer "+key), 403, "forbidden")
	expect(t, e.do(e.admin, "GET", "/admin/nope", ""), 401, "missing_admin_token") // fail-closed
	expect(t, e.adm("GET", "/admin/nope", ""), 404, "not_found")
	expect(t, e.user(adminToken, "GET", "/v1/models", ""), 401, "invalid_api_key")
}

func TestAuthBeforeBody(t *testing.T) {
	e := newEnv(t)
	body := &countingReader{r: bytes.NewReader(make([]byte, 2<<20))}
	r := httptest.NewRequest("POST", "/v1/chat/completions", body)
	rec := httptest.NewRecorder()
	e.pub.ServeHTTP(rec, r)
	expect(t, rec, 401, "invalid_api_key")
	if body.n != 0 {
		t.Errorf("read %d body bytes before auth", body.n)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestUnknownRevokedExpiredIdentical(t *testing.T) {
	e := newEnv(t)
	revoked, id := e.mint(`{"name":"lab-01"}`)
	expect(t, e.adm("DELETE", "/admin/keys/"+strconv.FormatInt(id, 10), ""), 200, "")
	expired, _ := e.mint(`{"name":"lab-02","expires_at":"` + e.now().Add(time.Hour).Format(time.RFC3339) + `"}`)
	e.advance(2 * time.Hour)
	unknown := "lgai_" + strings.Repeat("A", 43)

	var first *httptest.ResponseRecorder
	for _, k := range []string{unknown, revoked, expired} {
		rec := e.user(k, "POST", "/v1/chat/completions", chatBody, "X-Request-ID", "same-id")
		expect(t, rec, 401, "invalid_api_key")
		if first == nil {
			first = rec
			continue
		}
		if rec.Body.String() != first.Body.String() || len(rec.Header()) != len(first.Header()) {
			t.Errorf("responses differ:\n%v %s\n%v %s", first.Header(), first.Body, rec.Header(), rec.Body)
		}
		for h := range first.Header() {
			if rec.Header().Get(h) != first.Header().Get(h) {
				t.Errorf("header %s differs", h)
			}
		}
	}
	if n := len(e.usage()); n != 0 {
		t.Errorf("%d usage rows for 401s", n)
	}
}

func TestRevokeIsImmediate(t *testing.T) {
	e := newEnv(t)
	key, id := e.mint(`{"name":"lab-01"}`)
	expect(t, e.user(key, "GET", "/v1/models", ""), 200, "")
	expect(t, e.adm("DELETE", "/admin/keys/"+strconv.FormatInt(id, 10), ""), 200, "")
	expect(t, e.user(key, "GET", "/v1/models", ""), 401, "invalid_api_key")
}

// ---- admin API ----

func TestKeyCreateMassAssignment(t *testing.T) {
	e := newEnv(t)
	for _, f := range []string{`"id":9`, `"key":"lgai_x"`, `"key_hash":"00"`, `"key_prefix":"lgai_x"`, `"created_at":"2026-01-01T00:00:00Z"`, `"revoked_at":null`} {
		expect(t, e.adm("POST", "/admin/keys", `{"name":"x",`+f+`}`), 400, "unknown_field")
	}
	for _, body := range []string{`{"name":""}`, `{"name":"` + strings.Repeat("n", 101) + `"}`, `{"name":"a\u0000b"}`,
		`{"name":"x","rpm_limit":-1}`, `{"name":"x","expires_at":"yesterday"}`, `{"name":"x","expires_at":"2020-01-01T00:00:00Z"}`} {
		expect(t, e.adm("POST", "/admin/keys", body), 400, "invalid_value")
	}
}

func TestPlaintextKeyShownOnce(t *testing.T) {
	e := newEnv(t)
	rec := e.adm("POST", "/admin/keys", `{"name":"lab-01","rpm_limit":0,"max_inflight":4}`)
	expect(t, rec, 201, "")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("201 without Cache-Control: no-store")
	}
	var k struct {
		ID        int64  `json:"id"`
		Key       string `json:"key"`
		Prefix    string `json:"key_prefix"`
		RPM       *int64 `json:"rpm_limit"`
		ExpiresAt any    `json:"expires_at"`
	}
	decodeJSON(t, rec, &k)
	if !strings.HasPrefix(k.Key, k.Prefix) || len(k.Prefix) != 12 || k.RPM == nil || *k.RPM != 0 || k.ExpiresAt != nil {
		t.Fatalf("created %+v", k)
	}
	secrets := hashForms(k.Key)
	id := strconv.FormatInt(k.ID, 10)
	e.user(k.Key, "POST", "/v1/chat/completions", chatBody)
	for _, r := range []*httptest.ResponseRecorder{
		e.adm("GET", "/admin/keys", ""), e.adm("GET", "/admin/keys/"+id, ""),
		e.adm("GET", "/admin/keys/"+id+"/usage", ""), e.adm("DELETE", "/admin/keys/"+id, ""),
	} {
		expect(t, r, 200, "")
		for _, s := range secrets {
			if strings.Contains(r.Body.String(), s) {
				t.Errorf("admin response contains a key form: %s", r.Body)
			}
		}
	}
}

func TestBatchAndRevokeByPrefix(t *testing.T) {
	e := newEnv(t)
	rec := e.adm("POST", "/admin/keys/batch", `{"count":3,"name_prefix":"lab-","expires_at":"`+e.now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}`)
	expect(t, rec, 201, "")
	var batch struct {
		Object string `json:"object"`
		Data   []struct {
			Name      string  `json:"name"`
			Key       string  `json:"key"`
			ExpiresAt *string `json:"expires_at"`
		} `json:"data"`
	}
	decodeJSON(t, rec, &batch)
	if batch.Object != "list" || len(batch.Data) != 3 || batch.Data[0].Name != "lab-01" || batch.Data[2].Name != "lab-03" || batch.Data[1].ExpiresAt == nil {
		t.Fatalf("batch %+v", batch)
	}
	for _, k := range batch.Data {
		expect(t, e.user(k.Key, "GET", "/v1/models", ""), 200, "")
	}
	other, _ := e.mint(`{"name":"team-demo"}`)
	for _, body := range []string{`{"count":0,"name_prefix":"x"}`, `{"count":201,"name_prefix":"x"}`, `{"count":1,"name_prefix":""}`} {
		expect(t, e.adm("POST", "/admin/keys/batch", body), 400, "invalid_value")
	}
	if rec := e.adm("POST", "/admin/keys/batch", `{"count":100,"name_prefix":"s-"}`); !strings.Contains(rec.Body.String(), `"name":"s-001"`) {
		t.Error("width not derived from count")
	}

	for _, body := range []string{`{}`, `{"name_prefix":"lab-","all":true}`, `{"all":false}`} {
		expect(t, e.adm("POST", "/admin/keys/revoke", body), 400, "invalid_value")
	}
	rec = e.adm("POST", "/admin/keys/revoke", `{"name_prefix":"lab-"}`)
	expect(t, rec, 200, "")
	if strings.TrimSpace(rec.Body.String()) != `{"revoked":3}` {
		t.Errorf("revoke body %s", rec.Body)
	}
	for _, k := range batch.Data {
		expect(t, e.user(k.Key, "GET", "/v1/models", ""), 401, "invalid_api_key")
	}
	expect(t, e.user(other, "GET", "/v1/models", ""), 200, "")
	rec = e.adm("POST", "/admin/keys/revoke", `{"all":true}`)
	if strings.TrimSpace(rec.Body.String()) != `{"revoked":101}` {
		t.Errorf("revoke all %s", rec.Body)
	}
	expect(t, e.user(other, "GET", "/v1/models", ""), 401, "invalid_api_key")
}

func TestKeyTTL(t *testing.T) {
	e := newEnv(t, "LGAI_KEY_TTL", "12h")
	rec := e.adm("POST", "/admin/keys", `{"name":"lab-01"}`)
	var k struct {
		ExpiresAt time.Time `json:"expires_at"`
		CreatedAt time.Time `json:"created_at"`
	}
	decodeJSON(t, rec, &k)
	if d := k.ExpiresAt.Sub(k.CreatedAt); d < 12*time.Hour-time.Second || d > 12*time.Hour+time.Second {
		t.Errorf("TTL %s", d)
	}
}

func TestKeyUsageEndpoint(t *testing.T) {
	e := newEnv(t)
	key, id := e.mint(`{"name":"lab-01"}`)
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 200, "")
	expect(t, e.user(key, "POST", "/v1/chat/completions", `{"n":2,"messages":[{"role":"user","content":"x"}]}`), 400, "unsupported_parameter")
	rec := e.adm("GET", "/admin/keys/"+strconv.FormatInt(id, 10)+"/usage", "")
	expect(t, rec, 200, "")
	var u store.UsageSummary
	decodeJSON(t, rec, &u)
	if u.Requests != 1 || u.OK != 1 || u.TotalTokens != 13 || u.Name != "lab-01" { // the 400 is not recorded
		t.Errorf("usage %+v", u)
	}
	expect(t, e.adm("GET", "/admin/keys/1/usage?since=yesterday", ""), 400, "invalid_value")
	expect(t, e.adm("GET", "/admin/keys/1/usage?since=2026-01-01T00:00:00+02:00", ""), 200, "") // unescaped +
	expect(t, e.adm("GET", "/admin/keys/1/usage?since=2026-10-02T00:00:00Z&until=2026-10-01T00:00:00Z", ""), 400, "invalid_value")
	expect(t, e.adm("GET", "/admin/keys/99/usage", ""), 404, "key_not_found")
	expect(t, e.adm("GET", "/admin/keys/abc", ""), 404, "key_not_found")
	expect(t, e.adm("DELETE", "/admin/keys/99", ""), 404, "key_not_found")
}

func TestMaintenanceMode(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	expect(t, e.adm("PUT", "/admin/maintenance", `{}`), 400, "missing_field")
	expect(t, e.adm("PUT", "/admin/maintenance", `{"enabled":true}`), 200, "")
	for _, rec := range []*httptest.ResponseRecorder{
		e.user(key, "POST", "/v1/chat/completions", chatBody),
		e.user(key, "POST", "/v1/tasks/explain", `{"language":"go","code":"x"}`),
	} {
		expect(t, rec, 503, "maintenance")
		if rec.Header().Get("Retry-After") != "10" {
			t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
		}
	}
	expect(t, e.user(key, "GET", "/v1/models", ""), 200, "")
	expect(t, e.do(e.pub, "GET", "/healthz", ""), 200, "")
	if rec := e.adm("GET", "/admin/maintenance", ""); strings.TrimSpace(rec.Body.String()) != `{"enabled":true}` {
		t.Errorf("status %s", rec.Body)
	}
	expect(t, e.adm("PUT", "/admin/maintenance", `{"enabled":false}`), 200, "")
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 200, "")
}
