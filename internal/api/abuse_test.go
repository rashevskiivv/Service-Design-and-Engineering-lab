package api

import (
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRefusedRequestsAreThrottled is security-code V2: a key that sends
// invalid requests gets a bounded number of 400s, then 429s that read no body,
// and none of them writes a usage row. Other keys are unaffected.
func TestRefusedRequestsAreThrottled(t *testing.T) {
	e := newEnv(t)
	key, _ := e.mint(`{"name":"lab-01"}`)
	codes := map[int]int{}
	for range 200 {
		codes[e.user(key, "POST", "/v1/chat/completions", `{}`).Code]++
	}
	// The bucket refills 0.5 tokens/s, so a slow test run may earn one more.
	if codes[400] < rejectBurst || codes[400] > rejectBurst+2 || codes[400]+codes[429] != 200 {
		t.Errorf("status counts %v, want %d-%d × 400 and the rest 429", codes, rejectBurst, rejectBurst+2)
	}
	if rows := e.usage(); len(rows) != 0 {
		t.Errorf("%d usage rows for refused requests", len(rows))
	}

	body := &countingReader{r: strings.NewReader(chatBody)}
	r := httptest.NewRequest("POST", "/v1/chat/completions", body)
	r.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	e.pub.ServeHTTP(rec, r)
	expect(t, rec, 429, "rate_limit_exceeded")
	if body.n != 0 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("throttled request read %d body bytes; Retry-After %q", body.n, rec.Header().Get("Retry-After"))
	}

	other, _ := e.mint(`{"name":"lab-02"}`)
	expect(t, e.user(other, "POST", "/v1/chat/completions", chatBody), 200, "")
}

// TestKeySlotTakenBeforeBody is security-code V1 at the handler level: while
// one slow body holds the key's only slot, its next request is refused
// without a single body byte read.
func TestKeySlotTakenBeforeBody(t *testing.T) {
	e := newEnv(t, "LGAI_KEY_MAX_INFLIGHT", "1")
	key, id := e.mint(`{"name":"lab-01"}`)
	srv := httptest.NewServer(e.pub)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Length: 1000\r\n\r\n{", key)

	// Wait until the slow request holds the key's only slot.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		release, free := e.s.perKey.TryAcquire(id, 1)
		if !free {
			break
		}
		release()
		if time.Now().After(deadline) {
			t.Fatal("the slow request never took the slot")
		}
	}
	body := &countingReader{r: strings.NewReader(chatBody)}
	r := httptest.NewRequest("POST", "/v1/chat/completions", body)
	r.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	e.pub.ServeHTTP(rec, r)
	expect(t, rec, 429, "concurrency_limit_exceeded")
	if body.n != 0 {
		t.Errorf("read %d body bytes before refusing", body.n)
	}
}
