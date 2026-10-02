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

// Stream reads an upstream SSE body one event at a time and hands each event
// to sink, which writes the client's format. ctx is the upstream request
// context; cancel cancels it with a cause (idle watchdog, client gone). Usage,
// TTFT and failures are tracked here for every format. Upstream error
// payloads never reach the sink; it gets Fail instead.
func Stream(ctx context.Context, cancel context.CancelCauseFunc, sink Sink, w http.ResponseWriter, body io.Reader, o Options) Outcome {
	s := &streamState{o: o, sink: sink, cw: newWriter(w, o), maxEvent: o.MaxEventBytes}
	if s.maxEvent <= 0 {
		s.maxEvent = defaultMaxEventBytes
	}
	defer s.cw.clearDeadline()

	if err := sink.Start(s.cw); err != nil {
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
	sink     Sink
	cw       *Writer
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
	ev := Event{Raw: s.event}
	data := s.data
	s.event, s.data = nil, nil
	if len(ev.Raw) == 0 {
		return false, true
	}
	if len(data) > 0 {
		payload := bytes.Join(data, []byte("\n"))
		if string(payload) == "[DONE]" {
			ev.Done = true
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
			if peek.HasToken() {
				s.tokens++
				if s.ttft == 0 {
					s.ttft = max(s.o.now().Sub(s.o.Start), time.Millisecond)
				}
			}
			ev.Chunk = &peek
		}
	}
	if err := s.sink.Event(s.cw, ev); err != nil {
		s.gone = true
		cancel(ErrClientGone)
		return false, false
	}
	return ev.Done, true
}

// end finishes the stream: after [DONE] the sink finishes the response (200);
// otherwise classify the failure and, unless the client is gone, let the sink
// report it.
func (s *streamState) end(ctx context.Context, done bool) Outcome {
	if done {
		out := s.outcome(http.StatusOK, "")
		if err := s.sink.Finish(s.cw, out); err != nil {
			out.Status = oai.StatusClientClosed
		}
		return out
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
		s.sink.Fail(s.cw, status, code)
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
