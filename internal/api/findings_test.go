package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"local-generative-ai/internal/testupstream"
)

const streamBody = `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`

// TestInvalidUpstreamBody is review m1 end to end: a non-streaming 200 that
// is not a chat completion is a generic 502, recorded and logged with a reason.
func TestInvalidUpstreamBody(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	h := http.Header{}
	h.Set("Content-Type", "text/html")
	e.up.Set(testupstream.Behaviour{Header: h, Body: "<html>CANARY proxy error</html>"})
	rec := e.user(key, "POST", "/v1/chat/completions", chatBody)
	expect(t, rec, 502, "upstream_error")
	if strings.Contains(rec.Body.String(), "CANARY") {
		t.Errorf("upstream body leaked: %s", rec.Body)
	}
	if u := e.lastUsage(); u.Status != 502 || u.ErrorCode != "upstream_error" {
		t.Errorf("usage %+v", u)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, `"reason":"body is not a JSON chat completion (content-type text/html)"`) ||
		!strings.Contains(logs, `"upstream_status":200`) || strings.Contains(logs, "CANARY") {
		t.Errorf("log lacks the reason or leaks content: %s", logs)
	}
}

// TestUpstreamStatusLogged is review m3: the client sees a generic 502, the
// log shows the real upstream status and how long the upstream took.
func TestUpstreamStatusLogged(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	for _, status := range []int{401, 404, 500} {
		e.up.Set(testupstream.Behaviour{Status: status})
		expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 502, "upstream_error")
		want := `"msg":"upstream request failed","request_id":`
		if !strings.Contains(e.logs.String(), want) || !strings.Contains(e.logs.String(), `"upstream_status":`+strconv.Itoa(status)+`,"upstream_ms":`) {
			t.Errorf("upstream %d not logged: %s", status, e.logs.String())
		}
	}
}

// TestPanicAfterAdmissionRecords500 is review m6, before the response starts:
// a JSON 500 for the client, a 500 usage row, and every slot released.
func TestPanicAfterAdmissionRecords500(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.s.up = nil // the upstream call now panics
	expect(t, e.user(key, "POST", "/v1/chat/completions", chatBody), 500, "internal_error")
	if u := e.lastUsage(); u.Status != 500 || u.ErrorCode != "internal_error" {
		t.Errorf("usage %+v", u)
	}
	if in, q := e.s.gate.Stats(); in != 0 || q != 0 {
		t.Errorf("gate %d/%d", in, q)
	}
	if !strings.Contains(e.logs.String(), `"msg":"panic"`) {
		t.Error("panic not logged")
	}
}

// panicWriter panics on the first body write, i.e. after the stream's headers.
type panicWriter struct{ *httptest.ResponseRecorder }

func (panicWriter) Write([]byte) (int, error) { panic("write exploded") }

// TestPanicMidStreamAbortsConnection is review m6 after the response started:
// no JSON is appended to the event stream; the connection is aborted, and the
// usage row and the access-log line still say 500.
func TestPanicMidStreamAbortsConnection(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(streamBody))
	r.Header.Set("Authorization", "Bearer "+key)
	w := panicWriter{httptest.NewRecorder()}
	func() {
		defer func() {
			if v := recover(); v != http.ErrAbortHandler {
				t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
			}
		}()
		e.pub.ServeHTTP(w, r)
	}()
	if strings.Contains(w.Body.String(), "internal_error") {
		t.Errorf("JSON error appended to the stream: %q", w.Body)
	}
	if u := e.lastUsage(); u.Status != 500 {
		t.Errorf("usage %+v", u)
	}
	if !strings.Contains(e.logs.String(), `"msg":"request"`) || !strings.Contains(e.logs.String(), `"status":500`) {
		t.Errorf("access log line missing: %s", e.logs.String())
	}
	if in, _ := e.s.gate.Stats(); in != 0 {
		t.Errorf("gate slot leaked")
	}
}

// TestWaitCallsAfterForcedClose is review m2: after a forced close the stream
// handler still writes its usage row, and WaitCalls reports when it has.
func TestWaitCallsAfterForcedClose(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{Chunks: repeatChunks("x", 200), ChunkDelay: 20 * time.Millisecond})
	srv := httptest.NewServer(e.pub)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/v1/chat/completions", strings.NewReader(streamBody))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := e.s.WaitCalls(short); err == nil {
		t.Fatal("WaitCalls returned while a stream was running")
	}

	_ = srv.Config.Close() // what main does when Shutdown times out
	ctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := e.s.WaitCalls(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := e.usage(); len(rows) != 1 || rows[0].Status != 499 {
		t.Errorf("usage rows %+v", rows)
	}
}

// TestUpstreamConnectionReused is review n5: the relay reads past [DONE] to
// EOF, so sequential streams share one upstream connection even when the
// response end arrives in a later packet.
func TestUpstreamConnectionReused(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	e.up.Set(testupstream.Behaviour{DoneDelay: 20 * time.Millisecond})
	for range 5 {
		expect(t, e.user(key, "POST", "/v1/chat/completions", streamBody), 200, "")
	}
	addrs := map[string]bool{}
	for _, r := range e.up.Requests() {
		addrs[r.RemoteAddr] = true
	}
	if len(addrs) != 1 {
		t.Errorf("5 streams used %d upstream connections", len(addrs))
	}
}
