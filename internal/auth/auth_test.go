package auth

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"local-generative-ai/internal/config"
	"local-generative-ai/internal/store"
)

func TestKeyGenerateFormat(t *testing.T) {
	re := regexp.MustCompile(`^lgai_[A-Za-z0-9_-]{43}$`)
	seen := map[string]bool{}
	for range 10000 {
		k, h, prefix, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(k) || seen[k] || !WellFormed(k) || prefix != k[:12] || h != Hash(k) {
			t.Fatalf("bad key %q", k)
		}
		if b, err := base64.RawURLEncoding.DecodeString(k[5:]); err != nil || len(b) != 32 {
			t.Fatalf("%q does not decode to 32 bytes", k)
		}
		seen[k] = true
	}
}

// spyLookup records whether the store was queried.
type spyLookup struct {
	calls int
	key   store.Key
	err   error
}

func (s *spyLookup) KeyByHash(context.Context, [32]byte, time.Time) (store.Key, error) {
	s.calls++
	return s.key, s.err
}

func TestWellFormedRejects(t *testing.T) {
	k, _, _, _ := Generate()
	for _, bad := range []string{
		"sk-_" + k[5:], k[:47], k + "A", k[:47] + "=", k[:40] + "+" + k[41:], k[:40] + "/" + k[41:],
		k[:20] + " " + k[21:], k[:20] + "а" + k[22:], "", "lgai_",
	} {
		if WellFormed(bad) {
			t.Errorf("WellFormed(%q) = true", bad)
		}
		spy := &spyLookup{}
		rec := serve(RequireKey(spy, config.Limits{}, time.Now, slog.New(slog.DiscardHandler))(ok()), "Bearer "+bad)
		if rec.Code != 401 || spy.calls != 0 {
			t.Errorf("%q: status %d, lookups %d", bad, rec.Code, spy.calls)
		}
	}
}

func TestBearerParsing(t *testing.T) {
	k, _, _, _ := Generate()
	active := &spyLookup{key: store.Key{ID: 7}}
	h := RequireKey(active, config.Limits{RateRPM: 6}, time.Now, slog.New(slog.DiscardHandler))(ok())
	for _, v := range []string{"Bearer " + k, "bearer " + k, "BEARER  " + k} {
		if rec := serve(h, v); rec.Code != 200 {
			t.Errorf("%q → %d", v, rec.Code)
		}
	}
	for _, v := range []string{"Basic " + k, "Bearer", "Bearer ", k, "Token " + k} {
		rec := serve(h, v)
		if rec.Code != 401 || strings.Contains(rec.Body.String(), k) {
			t.Errorf("%q → %d %s", v, rec.Code, rec.Body)
		}
	}
	// Two Authorization headers, and keys in other places, are refused.
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer "+k)
			r.Header.Add("Authorization", "Bearer "+k)
		},
		func(r *http.Request) { r.URL.RawQuery = "api_key=" + k },
		func(r *http.Request) { r.Header.Set("X-API-Key", k) },
		func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "api_key", Value: k}) },
	} {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		mutate(r)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 401 || strings.Contains(rec.Body.String(), k) {
			t.Errorf("status %d body %s", rec.Code, rec.Body)
		}
	}
}

func TestPrincipalLimits(t *testing.T) {
	k, _, _, _ := Generate()
	zero, four := int64(0), int64(4)
	lookup := &spyLookup{key: store.Key{ID: 3, RPMLimit: &zero, MaxInflight: &four}}
	var got Principal
	h := RequireKey(lookup, config.Limits{RateRPM: 6, RateBurst: 3, KeyMaxInflight: 1, TokenQuotaDaily: 100}, time.Now,
		slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = PrincipalFrom(r.Context())
	}))
	serve(h, "Bearer "+k)
	if got != (Principal{KeyID: 3, RPM: 0, Burst: 3, MaxInflight: 4, DailyQuota: 100}) {
		t.Errorf("principal %+v", got)
	}
}

func TestLookupErrorIs500(t *testing.T) {
	k, _, _, _ := Generate()
	h := RequireKey(&spyLookup{err: io.ErrUnexpectedEOF}, config.Limits{}, time.Now, slog.New(slog.DiscardHandler))(ok())
	if rec := serve(h, "Bearer "+k); rec.Code != 500 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestAdminAuth(t *testing.T) {
	token := "3q2+7wAAAAB5dGVzdCB0b2tlbiBmb3IgbGdhaSBnYXRld2F5ISE="
	h := RequireAdmin(token)(ok())
	userKey, _, _, _ := Generate()
	cases := map[string]int{
		"":                               401,
		"Basic " + token:                 401,
		"Bearer " + token:                200,
		"Bearer " + token + "x":          403,
		"Bearer short":                   403,
		"Bearer " + userKey:              403,
		"bearer " + token:                200,
		"Bearer " + token[:len(token)-1]: 403,
	}
	for header, want := range cases {
		rec := serve(h, header)
		if rec.Code != want {
			t.Errorf("%q → %d, want %d", header, rec.Code, want)
		}
		if strings.Contains(rec.Body.String(), token) {
			t.Error("body echoes the token")
		}
	}
}

func ok() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
}

func serve(h http.Handler, authorization string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/v1/models", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
