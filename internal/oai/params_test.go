package oai

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseInt(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1", 1, true}, {"-3", -3, true}, {"1.0", 1, true}, {"1e9", 1e9, true}, {"1e400", math.MaxInt64, true},
		{"-1e400", math.MinInt64, true}, {"99999999999999999999", math.MaxInt64, true},
		{"1.5", 0, false}, {`"1"`, 0, false}, {"true", 0, false}, {"null", 0, false}, {"", 0, false}, {"1x", 0, false},
	}
	for _, tc := range tests {
		got, ok := ParseInt([]byte(tc.in))
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseInt(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTokenLimitsClamp(t *testing.T) {
	l := TokenLimits{Cap: 1024, NonStreamCap: 768}
	for _, tc := range []struct {
		req    int64
		def    int
		stream bool
		want   int
	}{
		{0, 512, true, 512}, {0, 1024, false, 768}, {2000, 512, true, 1024}, {2000, 512, false, 768}, {5, 512, false, 5},
	} {
		if got := l.Clamp(tc.req, tc.def, tc.stream); got != tc.want {
			t.Errorf("Clamp(%d, %d, %v) = %d, want %d", tc.req, tc.def, tc.stream, got, tc.want)
		}
	}
}

func TestParseModelDefaults(t *testing.T) {
	d, err := ParseModelDefaults([]byte(`{"reasoning_effort":"low","chat_template_kwargs":{"enable_thinking":false},"top_k":20,"top_p":0.8,"temperature":0.7,"presence_penalty":1.5}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.ReasoningEffort != "low" || *d.EnableThinking || *d.TopK != 20 || *d.TopP != 0.8 || *d.Temperature != 0.7 || *d.PresencePenalty != 1.5 {
		t.Errorf("got %+v", d)
	}
	if got := d.String(); got != `{"temperature":0.7,"top_p":0.8,"top_k":20,"presence_penalty":1.5,"reasoning_effort":"low","chat_template_kwargs":{"enable_thinking":false}}` {
		t.Errorf("String() = %s", got)
	}
	for _, bad := range []string{`{"keep_alive":-1}`, `{"max_tokens":9}`, `{"reasoning_effort":"max"}`, `{"chat_template_kwargs":{"tokenize":true}}`, `[]`, `{"temperature":9}`} {
		if _, err := ParseModelDefaults([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestWriteErrorHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	e := NewError(http.StatusTooManyRequests, TypeQuota, CodeQuota, "", "quota")
	e.RetryAfter, e.NoRetry = 1500*time.Millisecond, true
	WriteError(rec, e)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "2" || rec.Header().Get("X-Should-Retry") != "false" ||
		rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("headers %v", rec.Header())
	}
	if want := `{"error":{"message":"quota","type":"insufficient_quota","code":"insufficient_quota","param":null}}` + "\n"; rec.Body.String() != want {
		t.Errorf("body %s", rec.Body)
	}
}

func TestDecodeStrict(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}
	for body, code := range map[string]string{
		`{"a":1,"b":2}`: CodeUnknownField, `{"a":"x"}`: CodeInvalidValue, `[1]`: CodeInvalidJSON,
		`{"a":1} {}`: CodeInvalidJSON, `{"a":`: CodeInvalidJSON, "{\"a\":1,\"\xff\":1}": CodeInvalidJSON,
	} {
		if e := DecodeStrict([]byte(body), &v); e == nil || e.Code != code {
			t.Errorf("%s: got %v, want %s", body, e, code)
		}
	}
	if e := DecodeStrict([]byte(`{"a":1}`), &v); e != nil || v.A != 1 {
		t.Errorf("valid body: %v", e)
	}
}
