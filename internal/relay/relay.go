// Package relay copies an upstream chat completion to the client: SSE event by
// event with a flush after each, or a bounded JSON body. It captures usage and
// time-to-first-token without keeping any content.
package relay

import (
	"context"
	"errors"
	"net/http"
	"time"

	"local-generative-ai/internal/oai"
)

// Cancellation causes set on the upstream request context.
var (
	// ErrIdle: the upstream sent nothing for longer than the idle timeout.
	ErrIdle = errors.New("upstream idle timeout")
	// ErrClientGone: a write to the client failed.
	ErrClientGone = errors.New("client gone")
	// ErrUpstreamTimeout: the whole upstream request exceeded its time limit.
	ErrUpstreamTimeout = errors.New("upstream request timeout")
)

const (
	defaultMaxEventBytes = 1 << 20 // one SSE line/event above this is an upstream error
	maxJSONBytes         = 8 << 20 // non-streaming body cap
)

// Options configures one relay.
type Options struct {
	Start            time.Time // request arrival; TTFT reference
	ClientWantsUsage bool      // forward the usage-only chunk
	PromptBytes      int       // for the fallback estimate
	IdleTimeout      time.Duration
	WriteTimeout     time.Duration // per client write
	MaxEventBytes    int           // 0 → 1 MiB
	Now              func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Outcome is what happened, for the usage row and the access log.
type Outcome struct {
	Status         int // effective: 200, 499, 502, 504
	ErrCode        string
	Usage          oai.Usage
	UsageEstimated bool
	TTFT           time.Duration // 0 = no token seen (always 0 for JSON)
	Reason         string        // why an upstream response failed; for the log only, never content
}

// failure classifies why the upstream read stopped, from the context cause.
func failure(ctx context.Context) (int, string) {
	switch {
	case ctx.Err() == nil:
		return http.StatusBadGateway, oai.CodeUpstreamError
	case errors.Is(context.Cause(ctx), ErrIdle), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return http.StatusGatewayTimeout, oai.CodeUpstreamTimeout
	default:
		return oai.StatusClientClosed, ""
	}
}

// writer wraps the client connection: a fresh write deadline before every
// write, a flush after, and the deadline cleared at the end. The server has
// no global WriteTimeout (ARCHITECTURE §5.4), and net/http does not reset a
// connection's write deadline between keep-alive requests, so a stale deadline
// must never be left behind.
type writer struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
}

func newWriter(w http.ResponseWriter, o Options) *writer {
	return &writer{w: w, rc: http.NewResponseController(w), timeout: o.WriteTimeout}
}

func (cw *writer) write(chunks ...[]byte) error {
	if cw.timeout > 0 {
		// Deadlines are wall-clock times on the socket, so they use time.Now,
		// never the injectable clock.
		if err := cw.rc.SetWriteDeadline(time.Now().Add(cw.timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
	}
	for _, c := range chunks {
		if _, err := cw.w.Write(c); err != nil {
			return err
		}
	}
	if err := cw.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func (cw *writer) clearDeadline() {
	_ = cw.rc.SetWriteDeadline(time.Time{})
}

func estimate(bytes int) int { return (bytes + 3) / 4 }
