package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-generative-ai/internal/testupstream"
)

func TestLiveSink(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{Chunks: []string{"Hello", " world"}})
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	sink := &Live{ContentType: "text/plain; charset=utf-8", Disposition: `attachment; filename="x.md"`, Head: "# H\n\n",
		Foot: func(o Outcome) string { return "tokens " + string(rune('0'+o.Usage.CompletionTokens)) + "\n" }}
	out := Stream(ctx, cancel, sink, rec, body, opts())
	if out.Status != 200 || rec.Body.String() != "# H\n\nHello world\ntokens 2\n" {
		t.Fatalf("outcome %+v body %q", out, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "text/plain; charset=utf-8" || h.Get("Content-Disposition") != `attachment; filename="x.md"` {
		t.Errorf("headers %v", h)
	}
	// headers, head, one flush per delta (the role-only chunk carries no text), tail
	if want := []string{"", "# H\n\n", "# H\n\nHello", "# H\n\nHello world", "# H\n\nHello world\ntokens 2\n"}; strings.Join(rec.flushes, "|") != strings.Join(want, "|") {
		t.Errorf("flushes %q", rec.flushes)
	}
}

func TestLiveSinkFail(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{MidError: true})
	rec := httptest.NewRecorder()
	out := Stream(ctx, cancel, &Live{ContentType: "text/plain"}, rec, body, opts())
	if out.Status != http.StatusBadGateway || rec.Body.String() != "Hello\n\n[error: "+failMessage+"]\n" || strings.Contains(rec.Body.String(), "CANARY") {
		t.Errorf("outcome %+v body %q", out, rec.Body)
	}
}

func TestBufferedSink(t *testing.T) {
	_, ctx, cancel, body := open(t, testupstream.Behaviour{Chunks: []string{"a", "b"}})
	var got string
	sink := &Buffered{Render: func(answer string, o Outcome) Response {
		got = answer
		return Response{ContentType: "text/x-go; charset=utf-8", Header: http.Header{"X-Lgai-Code-Blocks": {"1"}}, Body: "code\n"}
	}}
	rec := httptest.NewRecorder()
	out := Stream(ctx, cancel, sink, rec, body, opts())
	if out.Status != 200 || got != "ab" || rec.Body.String() != "code\n" || rec.Header().Get("Content-Length") != "5" ||
		rec.Header().Get("X-LGAI-Code-Blocks") != "1" || rec.Header().Get("Content-Type") != "text/x-go; charset=utf-8" {
		t.Errorf("outcome %+v answer %q body %q headers %v", out, got, rec.Body, rec.Header())
	}

	_, ctx, cancel, body = open(t, testupstream.Behaviour{NoDone: true})
	rec = httptest.NewRecorder()
	out = Stream(ctx, cancel, &Buffered{Render: func(string, Outcome) Response { t.Error("rendered a failed stream"); return Response{} }}, rec, body, opts())
	if out.Status != http.StatusBadGateway || rec.Code != http.StatusBadGateway || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		!strings.Contains(rec.Body.String(), `"code":"upstream_error"`) {
		t.Errorf("failure: %+v %d %s", out, rec.Code, rec.Body)
	}
}
