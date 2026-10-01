// Package testupstream is a scriptable fake OpenAI-compatible model server
// for tests. It records every request (method, path, headers, decoded body)
// and answers /chat/completions with JSON or SSE, including the failure modes
// the gateway must handle. Only tests import it.
package testupstream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Request is one recorded upstream request.
type Request struct {
	Method     string
	Path       string
	Header     http.Header
	Raw        []byte
	Body       map[string]any
	RemoteAddr string // the client side of the TCP connection
}

// Behaviour scripts the answers. The zero value is a healthy server.
type Behaviour struct {
	Status       int           // non-2xx: answer {"error":{"message":ErrorMessage}}
	ErrorMessage string        // for Status
	RetryAfter   string        // Retry-After header with Status
	Header       http.Header   // extra response headers on every answer
	HeaderDelay  time.Duration // wait before sending headers
	Redirect     string        // answer 303 to this URL
	Chunks       []string      // content pieces (default "Hello", " world")
	NoUsage      bool          // never send usage
	UsageAlways  bool          // usage on every chunk (vLLM --enable-force-include-usage)
	MidError     bool          // after the first chunk: data: {"error":…}
	RawError     bool          // after the first chunk: a raw JSON line (not SSE)
	NoDone       bool          // end without data: [DONE]
	StallAfter   int           // >0: after this many chunks, go silent until cancelled
	ChunkDelay   time.Duration // pause between chunks
	HugeLine     int           // >0: one data line of this many bytes after the first chunk
	DoneDelay    time.Duration // pause after data: [DONE] before the response ends
	Body         string        // non-streaming 200 body, sent verbatim (Content-Type from Header, else JSON)
}

// Server is a running fake upstream.
type Server struct {
	*httptest.Server
	mu        sync.Mutex
	b         Behaviour
	reqs      []Request
	cancelled atomic.Int64
}

// New starts a fake upstream that is closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{}
	s.Server = httptest.NewServer(s)
	t.Cleanup(s.Close)
	return s
}

// BaseURL is the upstream base URL including /v1.
func (s *Server) BaseURL() string { return s.URL + "/v1" }

// Set replaces the behaviour.
func (s *Server) Set(b Behaviour) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b = b
}

// Requests returns the recorded requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// Last returns the last recorded request, failing the test if there is none.
func (s *Server) Last(t testing.TB) Request {
	t.Helper()
	r := s.Requests()
	if len(r) == 0 {
		t.Fatal("upstream received no request")
	}
	return r[len(r)-1]
}

// Cancelled counts requests whose context ended before the answer was done.
func (s *Server) Cancelled() int64 { return s.cancelled.Load() }

// ServeHTTP implements the fake.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Raw: raw, Body: body,
		RemoteAddr: r.RemoteAddr})
	b := s.b
	s.mu.Unlock()

	if b.Redirect != "" {
		http.Redirect(w, r, b.Redirect, http.StatusSeeOther)
		return
	}
	if b.HeaderDelay > 0 && !sleep(r, b.HeaderDelay) {
		s.cancelled.Add(1)
		return
	}
	for k, v := range b.Header {
		w.Header()[k] = v
	}
	if b.Status != 0 && b.Status != http.StatusOK {
		if b.RetryAfter != "" {
			w.Header().Set("Retry-After", b.RetryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.Status)
		msg, _ := json.Marshal(b.ErrorMessage)
		fmt.Fprintf(w, `{"error":{"message":%s,"type":"BadRequestError"}}`, msg)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/models") {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"up-model","object":"model"}]}`)
		return
	}
	chunks := b.Chunks
	if chunks == nil {
		chunks = []string{"Hello", " world"}
	}
	if stream, _ := body["stream"].(bool); stream {
		s.stream(w, r, b, body, chunks)
		return
	}
	if b.Body != "" {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		fmt.Fprint(w, b.Body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	content, _ := json.Marshal(strings.Join(chunks, ""))
	usage := ""
	if !b.NoUsage {
		usage = fmt.Sprintf(`,"usage":{"prompt_tokens":11,"completion_tokens":%d,"total_tokens":%d}`, len(chunks), 11+len(chunks))
	}
	fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"up-model",`+
		`"choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]%s}`, content, usage)
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, b Behaviour, body map[string]any, chunks []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	ok := true
	send := func(payload string) {
		if !ok {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil || rc.Flush() != nil {
			ok = false
		}
	}
	defer func() {
		if !ok || r.Context().Err() != nil {
			s.cancelled.Add(1)
		}
	}()
	usage := func(n int) string {
		return fmt.Sprintf(`{"prompt_tokens":11,"completion_tokens":%d,"total_tokens":%d}`, n, 11+n)
	}
	head := `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"up-model","choices":`
	send(head + `[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	for i, c := range chunks {
		if b.StallAfter > 0 && i == b.StallAfter {
			<-r.Context().Done()
			return
		}
		if b.ChunkDelay > 0 && !sleep(r, b.ChunkDelay) {
			return
		}
		content, _ := json.Marshal(c)
		extra := ""
		if b.UsageAlways {
			extra = `,"usage":` + usage(i+1)
		}
		send(head + `[{"index":0,"delta":{"content":` + string(content) + `},"finish_reason":null}]` + extra + `}`)
		if i == 0 {
			switch {
			case b.MidError:
				send(`{"error":{"message":"CANARY-UPSTREAM-ERROR /root/.cache/huggingface","type":"internal"}}`)
				return
			case b.RawError:
				fmt.Fprint(w, `{"error":"CANARY-UPSTREAM-ERROR raw"}`+"\n")
				return
			case b.HugeLine > 0:
				send(`{"x":"` + strings.Repeat("a", b.HugeLine) + `"}`)
			}
		}
		if !ok {
			return
		}
	}
	send(head + `[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	so, _ := body["stream_options"].(map[string]any)
	if iu, _ := so["include_usage"].(bool); iu && !b.NoUsage {
		send(head + `[],"usage":` + usage(len(chunks)) + `}`)
	}
	if !b.NoDone {
		send("[DONE]")
		if b.DoneDelay > 0 {
			sleep(r, b.DoneDelay) // the chunked terminator follows in a later packet
		}
	}
}

// sleep waits d unless the request is cancelled first.
func sleep(r *http.Request, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-r.Context().Done():
		return false
	}
}
