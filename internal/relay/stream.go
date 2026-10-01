package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"local-generative-ai/internal/oai"
)

var (
	errLineTooLong = errors.New("sse line too long")
	errDrained     = errors.New("drain after [DONE] gave up")
)

// After [DONE] the relay reads on to EOF (the chunked terminator usually
// follows at once) so the upstream connection is reused instead of closed;
// this bounds that wait (review n5).
const (
	drainBytes = 4 << 10
	drainWait  = 100 * time.Millisecond
)

// errorEvent is the one generic mid-stream error (DECISIONS D4). openai-python
// raises APIError on a data event whose JSON has "error". No [DONE] follows.
var errorEvent = []byte(`data: {"error":{"message":"the model server failed during the stream; retry the request",` +
	`"type":"server_error","code":"upstream_error","param":null}}` + "\n\n")

// Stream relays an upstream SSE body to w, one event at a time with a flush
// after each. ctx is the upstream request context; cancel cancels it with a
// cause (idle watchdog, client gone). The usage-only chunk is forwarded only
// if the client asked for it (ADR 0004). Upstream error payloads are never
// forwarded; the client gets the generic error event instead.
func Stream(ctx context.Context, cancel context.CancelCauseFunc, w http.ResponseWriter, body io.Reader, o Options) Outcome {
	s := &streamState{o: o, cw: newWriter(w, o), maxEvent: o.MaxEventBytes}
	if s.maxEvent <= 0 {
		s.maxEvent = defaultMaxEventBytes
	}
	defer s.cw.clearDeadline()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := s.cw.write(); err != nil {
		cancel(ErrClientGone)
		return s.outcome(oai.StatusClientClosed, "")
	}

	idle := time.AfterFunc(o.IdleTimeout, func() { cancel(ErrIdle) })
	defer idle.Stop()

	br := bufio.NewReaderSize(body, 64<<10)
	for {
		line, err := readLine(br, s.maxEvent)
		if err != nil {
			if errors.Is(err, io.EOF) && len(s.event) > 0 {
				if done, ok := s.flushEvent(cancel); !ok || done {
					return s.end(ctx, done)
				}
			}
			switch {
			case errors.Is(err, errLineTooLong):
				s.reason = "sse line too long"
			case errors.Is(err, io.EOF):
				s.reason = "stream ended without [DONE]"
			default:
				s.reason = "read error"
			}
			return s.end(ctx, false)
		}
		idle.Reset(o.IdleTimeout)
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			done, ok := s.flushEvent(cancel)
			if done {
				drain(br, cancel)
			}
			if !ok || done {
				return s.end(ctx, done)
			}
			continue
		}
		if !s.addLine(line) {
			return s.end(ctx, false)
		}
	}
}

type streamState struct {
	o        Options
	cw       *writer
	maxEvent int
	event    []byte   // raw lines of the pending event, each ending in \n
	data     [][]byte // its data payloads
	usage    *oai.Usage
	tokens   int // token-bearing chunks, for the fallback estimate
	ttft     time.Duration
	gone     bool   // a client write failed
	reason   string // why the upstream stream failed (log only)
}

// addLine appends one SSE line to the pending event. Lines that are not SSE
// fields (e.g. a raw JSON error body) end the stream as an upstream error.
func (s *streamState) addLine(line []byte) bool {
	name, value := line, []byte(nil)
	if i := bytes.IndexByte(line, ':'); i >= 0 {
		name, value = line[:i], bytes.TrimPrefix(line[i+1:], []byte(" "))
	}
	switch string(name) {
	case "data":
		s.data = append(s.data, bytes.Clone(value))
	case "", "event", "id", "retry": // "" is a ':' comment
	default:
		s.reason = "line that is not an SSE field"
		return false
	}
	s.event = append(append(s.event, line...), '\n')
	if len(s.event) > s.maxEvent {
		s.reason = "sse event too long"
		return false
	}
	return true
}

// flushEvent handles one complete event. done reports [DONE]; ok is false if
// the stream must end with an error (upstream error event or client gone).
func (s *streamState) flushEvent(cancel context.CancelCauseFunc) (done, ok bool) {
	event, data := s.event, s.data
	s.event, s.data = s.event[:0], nil
	if len(event) == 0 {
		return false, true
	}
	forward := true
	token := false
	if len(data) > 0 {
		payload := bytes.Join(data, []byte("\n"))
		if string(payload) == "[DONE]" {
			done = true
		} else {
			var peek oai.ChunkPeek
			if json.Unmarshal(payload, &peek) != nil {
				s.reason = "data event is not JSON"
				return false, false
			}
			if peek.IsError() {
				s.reason = "error event"
				return false, false
			}
			if peek.Usage != nil {
				u := *peek.Usage
				s.usage = &u
			}
			if token = peek.HasToken(); token {
				s.tokens++
			}
			forward = !peek.UsageOnly() || s.o.ClientWantsUsage
		}
	}
	if forward {
		if err := s.cw.write(event, []byte("\n")); err != nil {
			s.gone = true
			cancel(ErrClientGone)
			return false, false
		}
		if token && s.ttft == 0 {
			s.ttft = max(s.o.now().Sub(s.o.Start), time.Millisecond)
		}
	}
	return done, true
}

// end finishes the stream: 200 after [DONE]; otherwise classify the failure
// and, unless the client is gone, send the generic error event.
func (s *streamState) end(ctx context.Context, done bool) Outcome {
	if done {
		return s.outcome(http.StatusOK, "")
	}
	if s.gone {
		return s.outcome(oai.StatusClientClosed, "")
	}
	status, code := failure(ctx)
	switch {
	case errors.Is(context.Cause(ctx), ErrIdle):
		s.reason = "no data within the idle timeout"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.reason = "upstream request timeout"
	}
	if status != oai.StatusClientClosed {
		_ = s.cw.write(errorEvent)
	}
	return s.outcome(status, code)
}

func (s *streamState) outcome(status int, code string) Outcome {
	out := Outcome{Status: status, ErrCode: code, TTFT: s.ttft}
	if status != http.StatusOK {
		out.Reason = s.reason
	}
	if s.usage != nil {
		out.Usage = *s.usage
	} else {
		out.Usage = oai.Usage{PromptTokens: estimate(s.o.PromptBytes), CompletionTokens: s.tokens}
		out.UsageEstimated = true
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens
	return out
}

// readLine reads one line of at most limit bytes. A final line without a
// newline is returned as is; the next call reports io.EOF.
func readLine(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(buf)+len(frag) > limit {
			return nil, errLineTooLong
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			buf = append(buf, frag...)
			continue
		case errors.Is(err, io.EOF) && len(buf)+len(frag) > 0:
			return append(buf, frag...), nil
		case err != nil:
			return nil, err
		}
		if buf == nil {
			return frag, nil
		}
		return append(buf, frag...), nil
	}
}

// drain reads what follows [DONE], bounded in bytes and time, so the
// upstream connection reaches EOF and can be reused. If the upstream is slow
// to end the response, the request is cancelled and the connection dropped.
func drain(br *bufio.Reader, cancel context.CancelCauseFunc) {
	t := time.AfterFunc(drainWait, func() { cancel(errDrained) })
	defer t.Stop()
	_, _ = io.CopyN(io.Discard, br, drainBytes)
}
