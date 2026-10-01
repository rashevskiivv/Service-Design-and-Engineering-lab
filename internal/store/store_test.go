package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "gateway.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func newKey(name string, n byte) NewKey {
	return NewKey{Name: name, Prefix: "lgai_" + strings.Repeat(string('a'+n%26), 7), Hash: sha256.Sum256([]byte{n})}
}

func create(t *testing.T, s *Store, keys ...NewKey) []Key {
	t.Helper()
	out, err := s.CreateKeys(context.Background(), keys, t0)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDBFileModes(t *testing.T) {
	s, path := open(t)
	k := create(t, s, newKey("lab-01", 1))[0]
	if err := s.InsertUsage(context.Background(), UsageRow{TS: t0, RequestID: "r", KeyID: k.ID, Endpoint: "chat", Status: 200}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", filepath.Base(f), fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
}

func TestKeyLifecycle(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	rpm := int64(0)
	nk := newKey("team-demo", 1)
	nk.RPMLimit = &rpm
	k := create(t, s, nk)[0]
	if k.ID == 0 || k.Name != "team-demo" || k.RPMLimit == nil || *k.RPMLimit != 0 || k.DailyTokenQuota != nil || !k.CreatedAt.Equal(t0) {
		t.Fatalf("created %+v", k)
	}
	got, err := s.KeyByHash(ctx, nk.Hash, t0)
	if err != nil || got.ID != k.ID {
		t.Fatalf("KeyByHash = %+v, %v", got, err)
	}
	if _, err := s.KeyByHash(ctx, sha256.Sum256([]byte("unknown")), t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown key: %v", err)
	}
	r1, err := s.RevokeKey(ctx, k.ID, t0.Add(time.Minute))
	if err != nil || r1.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", r1, err)
	}
	r2, _ := s.RevokeKey(ctx, k.ID, t0.Add(time.Hour))
	if !r2.RevokedAt.Equal(*r1.RevokedAt) {
		t.Error("revoke is not idempotent")
	}
	if _, err := s.KeyByHash(ctx, nk.Hash, t0.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked key still active: %v", err)
	}
	if _, err := s.RevokeKey(ctx, 999, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke unknown: %v", err)
	}
	if _, err := s.GetKey(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("get unknown: %v", err)
	}
}

func TestExpiredKeyIsNotFound(t *testing.T) {
	s, _ := open(t)
	exp := t0.Add(time.Hour)
	nk := newKey("lab-01", 1)
	nk.ExpiresAt = &exp
	create(t, s, nk)
	if _, err := s.KeyByHash(context.Background(), nk.Hash, exp.Add(-time.Millisecond)); err != nil {
		t.Errorf("before expiry: %v", err)
	}
	if _, err := s.KeyByHash(context.Background(), nk.Hash, exp); !errors.Is(err, ErrNotFound) {
		t.Errorf("at expiry: %v", err)
	}
}

func TestRevokeByPrefixAndAll(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	create(t, s, newKey("lab-01", 1), newKey("lab-02", 2), newKey("lab_03", 3), newKey("team-demo", 4))
	n, err := s.RevokeKeys(ctx, "lab-", false, t0)
	if err != nil || n != 2 {
		t.Fatalf("prefix revoke = %d, %v", n, err)
	}
	if n, _ := s.RevokeKeys(ctx, "lab-", false, t0); n != 0 {
		t.Errorf("second prefix revoke = %d", n)
	}
	if _, err := s.RevokeKeys(ctx, "", false, t0); err == nil {
		t.Error("empty prefix accepted")
	}
	if n, _ := s.RevokeKeys(ctx, "", true, t0); n != 2 {
		t.Errorf("revoke all = %d", n)
	}
	keys, _ := s.ListKeys(ctx)
	for _, k := range keys {
		if k.RevokedAt == nil {
			t.Errorf("%s still active", k.Name)
		}
	}
}

func TestUsageQuotaAndSummary(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	k := create(t, s, newKey("lab-01", 1))[0]
	ttft := 900 * time.Millisecond
	rows := []UsageRow{
		{TS: t0, Status: 200, PromptTokens: 10, CompletionTokens: 20, TTFT: &ttft, Latency: 2 * time.Second, Stream: true, Model: "coder"},
		{TS: t0.Add(time.Minute), Status: 429, ErrorCode: "rate_limit_exceeded", Latency: time.Millisecond},
		{TS: t0.Add(2 * time.Minute), Status: 499, PromptTokens: 5, CompletionTokens: 1, Estimated: true, Latency: time.Second, Endpoint: "review", Language: "go"},
		{TS: t0.Add(-24 * time.Hour), Status: 200, PromptTokens: 1000, CompletionTokens: 1000, Latency: time.Second},
	}
	for _, r := range rows {
		r.KeyID, r.RequestID = k.ID, "req"
		if r.Endpoint == "" {
			r.Endpoint = "chat"
		}
		if err := s.InsertUsage(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	if n, err := s.TokensSince(ctx, k.ID, day); err != nil || n != 36 {
		t.Errorf("TokensSince = %d, %v", n, err)
	}
	u, err := s.KeyUsage(ctx, k.ID, day, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if u.Requests != 3 || u.OK != 1 || u.RateLimited != 1 || u.ClientCancelled != 1 || u.TotalTokens != 36 ||
		u.EstimatedRows != 1 || *u.AvgTTFTMS != 900 || u.Name != "lab-01" || !u.LastRequestAt.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("summary %+v", u)
	}
	empty, _ := s.KeyUsage(ctx, k.ID, t0.Add(time.Hour), t0.Add(2*time.Hour))
	if empty.Requests != 0 || empty.AvgLatencyMS != nil || empty.FirstRequestAt != nil {
		t.Errorf("empty summary %+v", empty)
	}
	keys, _ := s.ListKeys(ctx)
	if keys[0].LastUsedAt == nil || !keys[0].LastUsedAt.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("last_used_at = %v", keys[0].LastUsedAt)
	}
	if _, err := s.KeyUsage(ctx, 999, day, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown key usage: %v", err)
	}
}

// TestUsageSchemaHasNoContentColumns pins the documented, content-free columns.
func TestUsageSchemaHasNoContentColumns(t *testing.T) {
	s, _ := open(t)
	rows, err := s.db.Query("SELECT name FROM pragma_table_info('usage') ORDER BY cid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		cols = append(cols, c)
	}
	want := []string{"id", "ts", "request_id", "key_id", "endpoint", "model", "language", "stream", "status", "error_code",
		"prompt_tokens", "completion_tokens", "usage_estimated", "queue_ms", "ttft_ms", "latency_ms"}
	if !slices.Equal(cols, want) {
		t.Errorf("usage columns = %v", cols)
	}
}

func TestSQLInjectionLiteral(t *testing.T) {
	s, _ := open(t)
	name := "x'); DROP TABLE api_keys;--"
	k := create(t, s, newKey(name, 1))[0]
	got, err := s.GetKey(context.Background(), k.ID)
	if err != nil || got.Name != name {
		t.Fatalf("got %q, %v", got.Name, err)
	}
	if n, _ := s.RevokeKeys(context.Background(), "x'); DROP", false, t0); n != 1 {
		t.Errorf("prefix revoke with quotes = %d", n)
	}
	if _, err := s.ListKeys(context.Background()); err != nil {
		t.Errorf("api_keys gone: %v", err)
	}
}

func TestKeysFileImport(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	exp := t0.Add(24 * time.Hour)
	a, b := newKey("lab-01", 1), newKey("lab-02", 2)
	a.ExpiresAt = &exp
	past := t0.Add(-time.Hour)
	old := newKey("lab-00", 9)
	old.ExpiresAt = &past
	file := KeysFileHeader + "\n\n" + KeysFileLine(a) + "\n" + KeysFileLine(b) + "\n" + KeysFileLine(old) + "\n"
	keys, expired, err := ParseKeysFile(strings.NewReader(file), t0)
	if err != nil || len(keys) != 2 || expired != 1 || !keys[0].ExpiresAt.Equal(exp) || keys[1].Hash != b.Hash {
		t.Fatalf("parse: %+v expired=%d err=%v", keys, expired, err)
	}
	if n, err := s.ImportKeys(ctx, keys, t0); err != nil || n != 2 {
		t.Fatalf("import = %d, %v", n, err)
	}
	if _, err := s.RevokeKey(ctx, 1, t0); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ImportKeys(ctx, keys, t0); n != 0 {
		t.Errorf("re-import added %d", n)
	}
	if _, err := s.KeyByHash(ctx, a.Hash, t0); !errors.Is(err, ErrNotFound) {
		t.Error("re-import revived a revoked key")
	}
	for _, bad := range []string{"lab-01,lgai_short,00", "lab-01,lgai_aaaaaaa,zz", "a,b", ",lgai_aaaaaaa," + strings.Repeat("0", 64),
		"lab,lgai_aaaaaaa," + strings.Repeat("0", 64) + ",tomorrow"} {
		if _, _, err := ParseKeysFile(strings.NewReader(bad), t0); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(context.Background(), path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer schema accepted: %v", err)
	}
}
