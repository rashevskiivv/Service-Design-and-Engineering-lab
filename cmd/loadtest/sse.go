package main

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// maxSSELine bounds a single SSE line. The gateway uses the same limit.
const maxSSELine = 1 << 20

var errSSELineTooLong = errors.New("SSE line longer than 1 MiB")

type sseEvent struct {
	event string // value of the last "event:" field, "" for the default type
	data  string // "data:" lines joined with "\n"
}

// sseReader parses a text/event-stream body (WHATWG HTML "Server-sent
// events", section "Interpreting an event stream"), with two lenient
// deviations useful for load testing: an event still being assembled at EOF
// is returned instead of dropped, and events whose data is empty are skipped.
//
// Lines end in LF or CRLF. Lines starting with ':' are comments (heartbeats).
// "id:", "retry:" and unknown fields are ignored.
type sseReader struct {
	br         *bufio.Reader
	pendingErr error
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{br: bufio.NewReaderSize(r, 64<<10)}
}

// next returns the next event, or io.EOF at the end of the stream, or the
// underlying read error.
func (s *sseReader) next() (sseEvent, error) {
	if s.pendingErr != nil {
		return sseEvent{}, s.pendingErr
	}
	var (
		ev      sseEvent
		data    strings.Builder
		hasData bool
	)
	finish := func() (sseEvent, bool) {
		d := strings.TrimSuffix(data.String(), "\n")
		out := sseEvent{event: ev.event, data: d}
		data.Reset()
		hasData = false
		ev = sseEvent{}
		return out, d != ""
	}
	for {
		line, err := s.readLine()
		if err == nil || line != "" {
			if line == "" { // blank line: dispatch
				if hasData {
					if out, ok := finish(); ok {
						return out, nil
					}
				}
				ev = sseEvent{}
			} else if !strings.HasPrefix(line, ":") {
				field, value, found := strings.Cut(line, ":")
				if found {
					value = strings.TrimPrefix(value, " ")
				}
				switch field {
				case "data":
					data.WriteString(value)
					data.WriteByte('\n')
					hasData = true
				case "event":
					ev.event = value
				}
			}
		}
		if err != nil {
			if hasData {
				if out, ok := finish(); ok {
					s.pendingErr = err
					return out, nil
				}
			}
			return sseEvent{}, err
		}
	}
}

// readLine returns one line without its LF or CRLF terminator. At EOF it
// returns the unterminated remainder (possibly "") together with io.EOF.
func (s *sseReader) readLine() (string, error) {
	var buf []byte
	for {
		chunk, err := s.br.ReadSlice('\n')
		if len(buf)+len(chunk) > maxSSELine {
			return "", errSSELineTooLong
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		line := strings.TrimSuffix(string(buf), "\n")
		line = strings.TrimSuffix(line, "\r")
		return line, err
	}
}
