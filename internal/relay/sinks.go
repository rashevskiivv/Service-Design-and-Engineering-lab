package relay

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"local-generative-ai/internal/oai"
)

// Event is one upstream SSE event.
type Event struct {
	Raw   []byte         // its lines as received, each ending in "\n"
	Chunk *oai.ChunkPeek // the decoded data payload; nil for [DONE] and events without data
	Done  bool           // data: [DONE]
}

// Text is the answer text the event carries: the first choice's content delta.
func (e Event) Text() string {
	if e.Chunk == nil || len(e.Chunk.Choices) == 0 || e.Chunk.Choices[0].Delta.Content == nil {
		return ""
	}
	return *e.Chunk.Choices[0].Delta.Content
}

// Sink writes a relayed stream to the client in one response format
// (DECISIONS D9). Stream calls Start, then Event for every event, then Finish
// after [DONE] or Fail after an upstream failure (never when the client left).
// An error from Start, Event or Finish means the client is gone, except
// errAnswerCapped. Fail reports whether the connection must be dropped
// because part of a response was already sent.
type Sink interface {
	Start(w *Writer) error
	Event(w *Writer, ev Event) error
	Finish(w *Writer, out Outcome) error
	Fail(w *Writer, status int, code string) (abort bool)
}

// errAnswerCapped from Event ends the stream as a truncated answer: the sink
// holds as much as it may.
var errAnswerCapped = errors.New("answer reached the buffer cap")

// failError is the JSON error a sink sends when an upstream failure happens
// before any of its response was written.
func failError(w *Writer, status int, code string) {
	oai.WriteError(w.w, oai.NewError(status, oai.TypeServer, code, "", "the model server failed; retry the request"))
}

// failMessage is the one generic mid-stream error text (DECISIONS D4).
const failMessage = "the model server failed during the stream; retry the request"

// errorEvent is the mid-stream error of the SSE format. openai-python raises
// APIError on a data event whose JSON has "error". No [DONE] follows.
var errorEvent = []byte(`data: {"error":{"message":"` + failMessage + `",` +
	`"type":"server_error","code":"upstream_error","param":null}}` + "\n\n")

// SSE is the full format when the client streams: upstream events as they
// are, except the usage-only chunk unless the client asked for usage
// (ADR 0004).
type SSE struct{ WantsUsage bool }

// Start sends the event-stream headers.
func (s SSE) Start(w *Writer) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	return w.Begin(http.StatusOK)
}

// Event forwards the event.
func (s SSE) Event(w *Writer, ev Event) error {
	if ev.Chunk != nil && ev.Chunk.UsageOnly() && !s.WantsUsage {
		return nil
	}
	return w.Write(ev.Raw, []byte("\n"))
}

// Finish has nothing to add: [DONE] was forwarded.
func (SSE) Finish(*Writer, Outcome) error { return nil }

// Fail sends the generic error event. The full format keeps today's
// behaviour: the stream then ends normally.
func (SSE) Fail(w *Writer, _ int, _ string) bool {
	_ = w.Write(errorEvent)
	return false
}

// Live writes the answer text as it arrives, flushed per delta: the text and
// report formats when the client streams. Head comes first, Foot last. The
// response starts with the first answer text, so a failure before it is still
// a normal JSON error; a failure after it appends one "[error: …]" line and
// drops the connection, so curl exits non-zero (review D9-m2, D9-n5).
type Live struct {
	ContentType string
	Disposition string
	Head        string
	Foot        func(Outcome) string // nil: none
	started     bool
	newline     bool // the last byte written was "\n"
}

// Start sends nothing yet.
func (l *Live) Start(*Writer) error { return nil }

// Event writes the event's answer text, starting the response first.
func (l *Live) Event(w *Writer, ev Event) error {
	text := ev.Text()
	if text == "" {
		return nil
	}
	if err := l.begin(w); err != nil {
		return err
	}
	return l.write(w, text)
}

// Finish ends the answer with a newline, then writes Foot.
func (l *Live) Finish(w *Writer, out Outcome) error {
	if err := l.begin(w); err != nil {
		return err
	}
	tail := ""
	if !l.newline {
		tail = "\n"
	}
	if l.Foot != nil {
		tail += l.Foot(out)
	}
	return l.write(w, tail)
}

// Fail appends the error line and asks for the connection to be dropped, or
// sends a JSON error if nothing was written yet.
func (l *Live) Fail(w *Writer, status int, code string) bool {
	if !l.started {
		failError(w, status, code)
		return false
	}
	_ = l.write(w, "\n\n[error: "+failMessage+"]\n")
	return true
}

// begin sends the headers and Head once.
func (l *Live) begin(w *Writer) error {
	if l.started {
		return nil
	}
	l.started = true
	h := w.Header()
	h.Set("Content-Type", l.ContentType)
	h.Set("Content-Disposition", l.Disposition)
	h.Set("X-Accel-Buffering", "no")
	if err := w.Begin(http.StatusOK); err != nil {
		return err
	}
	return l.write(w, l.Head)
}

func (l *Live) write(w *Writer, s string) error {
	if s == "" {
		return nil
	}
	l.newline = strings.HasSuffix(s, "\n")
	return w.Write([]byte(s))
}

// Response is the one response a Buffered sink writes.
type Response struct {
	ContentType string
	Header      http.Header // extra headers, e.g. Content-Disposition
	Body        string
}

// Buffered collects the answer text and writes one response at the end: the
// code format, and text or report when the client did not stream. Nothing has
// been sent before then, so a failure is a normal JSON error. Limit caps the
// answer in bytes; past it the upstream is cancelled and the answer ends as
// truncated (review D9-m1).
type Buffered struct {
	Render func(answer string, out Outcome) Response
	Limit  int // bytes; 0 = no cap
	answer strings.Builder
}

// Start sends nothing yet.
func (b *Buffered) Start(*Writer) error { return nil }

// Event collects the answer text, up to Limit.
func (b *Buffered) Event(_ *Writer, ev Event) error {
	text := ev.Text()
	if b.Limit > 0 && b.answer.Len()+len(text) > b.Limit {
		return errAnswerCapped
	}
	b.answer.WriteString(text)
	return nil
}

// Finish renders and writes the whole response.
func (b *Buffered) Finish(w *Writer, out Outcome) error {
	r := b.Render(b.answer.String(), out)
	h := w.Header()
	for k, v := range r.Header {
		h[k] = v
	}
	h.Set("Content-Type", r.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(r.Body)))
	w.w.WriteHeader(http.StatusOK)
	return w.Write([]byte(r.Body))
}

// Fail sends a generic JSON error with the failure's status (502 or 504).
func (b *Buffered) Fail(w *Writer, status int, code string) bool {
	failError(w, status, code)
	return false
}
