package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"local-generative-ai/internal/config"
	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/testupstream"
)

const apiKey = "3q2+7wAAAAB5dGVzdCB0b2tlbiBmb3IgbGdhaSBnYXRld2F5ISE="

func newClient(t *testing.T, base string, mutate func(*config.Upstream)) *Client {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Upstream{BaseURL: u, HeaderTimeout: 5 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg, 4, 7*time.Second)
}

func chat(t *testing.T, c *Client, stream bool) (*http.Response, *oai.Error) {
	t.Helper()
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":` + map[bool]string{true: "true", false: "false"}[stream] + `}`
	resp, e := c.ChatCompletions(context.Background(), []byte(body), "req-1", stream)
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	return resp, e
}

func TestNoRedirects(t *testing.T) {
	second := testupstream.New(t)
	up := testupstream.New(t)
	up.Set(testupstream.Behaviour{Redirect: second.URL + "/v1/chat/completions"})
	_, e := chat(t, newClient(t, up.BaseURL(), nil), false)
	if e == nil || e.Status != http.StatusBadGateway || e.Code != oai.CodeUpstreamError {
		t.Fatalf("got %+v, want 502 upstream_error", e)
	}
	if n := len(second.Requests()); n != 0 {
		t.Errorf("redirect target received %d requests", n)
	}
}

func TestUpstreamRequestHeaders(t *testing.T) {
	up := testupstream.New(t)
	h := http.Header{}
	h.Set("Modal-Key", "wk-1")
	c := newClient(t, up.BaseURL(), func(u *config.Upstream) { u.APIKey = apiKey; u.Headers = h })
	if _, e := chat(t, c, true); e != nil {
		t.Fatal(e)
	}
	r := up.Last(t)
	if r.Method != http.MethodPost || r.Path != "/v1/chat/completions" {
		t.Errorf("%s %s", r.Method, r.Path)
	}
	want := map[string]string{
		"Authorization": "Bearer " + apiKey, "Content-Type": "application/json", "Accept": "text/event-stream",
		"X-Request-Id": "req-1", "Modal-Key": "wk-1", "Content-Length": r.Header.Get("Content-Length"),
	}
	for k := range r.Header {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected upstream header %s: %q", k, r.Header.Get(k))
		}
	}
	for k, v := range want {
		if r.Header.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, r.Header.Get(k), v)
		}
	}
	// Without an upstream key there is no Authorization at all.
	if _, e := chat(t, newClient(t, up.BaseURL(), nil), false); e != nil {
		t.Fatal(e)
	}
	if got := up.Last(t).Header.Values("Authorization"); len(got) != 0 {
		t.Errorf("Authorization sent without a key: %v", got)
	}
}

func TestUpstreamErrorText(t *testing.T) {
	up := testupstream.New(t)
	c := newClient(t, up.BaseURL(), nil)

	up.Set(testupstream.Behaviour{Status: 500, ErrorMessage: "/root/.cache/huggingface/CANARY"})
	_, e := chat(t, c, false)
	if e == nil || e.Status != 502 || strings.Contains(e.Message, "CANARY") {
		t.Errorf("500: %+v", e)
	}
	up.Set(testupstream.Behaviour{Status: 400, ErrorMessage: "bad CANARY2 " + strings.Repeat("é", 400)})
	_, e = chat(t, c, false)
	if e == nil || e.Status != 400 || !strings.Contains(e.Message, "CANARY2") || len(e.Message) > 512+40 {
		t.Errorf("400: %d %q", len(e.Message), e.Message)
	}
	up.Set(testupstream.Behaviour{Status: 400, ErrorMessage: "This model's maximum context length is 8192 tokens"})
	if _, e = chat(t, c, false); e == nil || e.Code != oai.CodeContextLength {
		t.Errorf("context length: %+v", e)
	}
	for _, s := range []int{401, 403, 404} {
		up.Set(testupstream.Behaviour{Status: s, ErrorMessage: "model CANARY3 not found"})
		if _, e = chat(t, c, false); e == nil || e.Status != 502 || strings.Contains(e.Message, "CANARY3") {
			t.Errorf("%d: %+v", s, e)
		}
	}
}

func TestOverloadMapping(t *testing.T) {
	up := testupstream.New(t)
	c := newClient(t, up.BaseURL(), nil)
	up.Set(testupstream.Behaviour{Status: 429, RetryAfter: "3"})
	if _, e := chat(t, c, true); e == nil || e.Status != 503 || e.Code != oai.CodeOverloaded || e.RetryAfter != 3*time.Second {
		t.Errorf("429: %+v", e)
	}
	up.Set(testupstream.Behaviour{Status: 503})
	if _, e := chat(t, c, true); e == nil || e.Status != 503 || e.RetryAfter != 7*time.Second {
		t.Errorf("503: %+v", e)
	}
}

func TestUpstreamTLSVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	t.Cleanup(srv.Close)
	_, e := chat(t, newClient(t, srv.URL+"/v1", nil), false)
	if e == nil || e.Status != http.StatusBadGateway || e.Code != oai.CodeUpstreamUnavailable {
		t.Errorf("self-signed: %+v", e)
	}
}

// TestHeaderTimeoutStreamOnly is critic M4: the first-byte timeout applies to
// streams; a non-streaming request waits for the whole completion.
func TestHeaderTimeoutStreamOnly(t *testing.T) {
	up := testupstream.New(t)
	up.Set(testupstream.Behaviour{HeaderDelay: 300 * time.Millisecond})
	c := newClient(t, up.BaseURL(), func(u *config.Upstream) { u.HeaderTimeout = 100 * time.Millisecond })
	if _, e := chat(t, c, true); e == nil || e.Status != http.StatusGatewayTimeout || e.Code != oai.CodeUpstreamTimeout {
		t.Errorf("stream: %+v", e)
	}
	if _, e := chat(t, c, false); e != nil {
		t.Errorf("non-stream: %+v", e)
	}
}

func TestTransportErrors(t *testing.T) {
	up := testupstream.New(t)
	up.Set(testupstream.Behaviour{HeaderDelay: time.Second})
	c := newClient(t, up.BaseURL(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, e := c.ChatCompletions(ctx, []byte(`{}`), "", false); e == nil || e.Status != http.StatusGatewayTimeout {
		t.Errorf("deadline: %+v", e)
	}
	ctx, cancel2 := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel2)
	if _, e := c.ChatCompletions(ctx, []byte(`{}`), "", false); e == nil || e.Status != oai.StatusClientClosed {
		t.Errorf("client gone: %+v", e)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if _, e := chat(t, newClient(t, closed.URL+"/v1", nil), false); e == nil || e.Code != oai.CodeUpstreamUnavailable {
		t.Errorf("dial: %+v", e)
	}
}

func TestReadyAndModelIDs(t *testing.T) {
	up := testupstream.New(t)
	c := newClient(t, up.BaseURL(), nil)
	if err := c.Ready(context.Background()); err != nil {
		t.Errorf("ready: %v", err)
	}
	if r := up.Last(t); r.Path != "/v1/models" || r.Method != http.MethodGet {
		t.Errorf("probe %s %s", r.Method, r.Path)
	}
	ids, err := c.ModelIDs(context.Background())
	if err != nil || !slices.Equal(ids, []string{"up-model"}) {
		t.Errorf("ids %v %v", ids, err)
	}
	health, _ := url.Parse(up.URL + "/health?token=SECRET")
	c = newClient(t, up.BaseURL(), func(u *config.Upstream) { u.HealthURL = health })
	up.Set(testupstream.Behaviour{Status: 503})
	if err := c.Ready(context.Background()); err == nil {
		t.Error("503 health counted as ready")
	}
	if up.Last(t).Path != "/health" {
		t.Errorf("health URL not used: %s", up.Last(t).Path)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	c = newClient(t, closed.URL+"/v1?token=SECRET", nil)
	if err := c.Ready(context.Background()); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("probe error leaks the URL: %v", err)
	}
}
