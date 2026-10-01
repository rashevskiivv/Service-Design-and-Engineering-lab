package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"local-generative-ai/internal/oai"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	infoKey
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// middleware wraps a router: request id → security headers → access log →
// panic recovery.
func (s *Server) middleware(h http.Handler) http.Handler {
	return requestID(securityHeaders(s.accessLog(s.recoverer(h))))
}

// requestID reuses a well-formed incoming X-Request-ID or generates one, and
// echoes it on the response.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// securityHeaders sets the headers every response carries, SSE and errors
// included (security #16). There are never any Access-Control-* headers.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// reqInfo collects per-request facts for the access log line. Handlers fill it;
// it never holds content, keys or client addresses.
type reqInfo struct {
	keyID                 int64
	endpoint, model, lang string
	stream, estimated     bool
	status                int // effective status (e.g. 499); 0 → the HTTP status
	errCode               string
	queueMS, ttftMS       int64 // -1 = not applicable
	promptTok, complTok   int
	inflight, queued      int
	hasGeneration         bool
}

// statusWriter records the status and byte count. Unwrap lets
// http.ResponseController reach Flush and SetWriteDeadline underneath.
type statusWriter struct {
	http.ResponseWriter
	status  int
	bytes   int64
	hasBody bool // the request carried a body
}

// WriteHeader also drops the connection after an error answer to a request
// with a body. Otherwise net/http would drain up to 256 KiB of unread body
// with no deadline, after the response: a client could trickle a body after a
// 401 or 429 and pin a connection (security-code V1). "Connection: close"
// skips the drain before the response; the expired read deadline ends the one
// in Body.Close after it.
func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		if code >= http.StatusBadRequest && w.hasBody {
			w.Header().Set("Connection", "close")
			_ = http.NewResponseController(w.ResponseWriter).SetReadDeadline(time.Now())
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// accessLog writes one JSON line per request (ARCHITECTURE §11), also when
// the handler aborts the connection with http.ErrAbortHandler.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		info := &reqInfo{queueMS: -1, ttftMS: -1}
		sw := &statusWriter{ResponseWriter: w, hasBody: r.ContentLength != 0}
		r = r.WithContext(context.WithValue(r.Context(), infoKey, info))
		defer s.logRequest(r, sw, info, start)
		next.ServeHTTP(sw, r)
	})
}

func (s *Server) logRequest(r *http.Request, sw *statusWriter, info *reqInfo, start time.Time) {
	status := info.status
	if status == 0 {
		status = sw.status
	}
	attrs := []slog.Attr{
		slog.String("request_id", requestIDFrom(r.Context())),
		slog.String("method", r.Method),
		slog.String("route", r.Pattern),
		slog.Int("status", status),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		slog.Int64("bytes_out", sw.bytes),
	}
	if info.hasGeneration {
		attrs = append(attrs,
			slog.Int64("key_id", info.keyID), slog.String("endpoint", info.endpoint),
			slog.String("model", info.model), slog.String("language", info.lang),
			slog.Bool("stream", info.stream), slog.Int64("queue_ms", info.queueMS),
			slog.Int64("ttft_ms", info.ttftMS), slog.Int("prompt_tokens", info.promptTok),
			slog.Int("completion_tokens", info.complTok), slog.Bool("usage_estimated", info.estimated),
			slog.String("error_code", info.errCode), slog.Int("inflight", info.inflight),
			slog.Int("queued", info.queued))
	}
	s.log.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
}

func infoFrom(r *http.Request) *reqInfo {
	if i, ok := r.Context().Value(infoKey).(*reqInfo); ok {
		return i
	}
	return &reqInfo{}
}

// recoverer turns a panic into a logged 500. If the response has already
// started (e.g. a stream), it aborts the connection instead: a JSON error
// appended to an event stream would only confuse the client (review m6).
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			s.log.Error("panic", "request_id", requestIDFrom(r.Context()), "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
			if sw, ok := w.(*statusWriter); ok && sw.status != 0 {
				panic(http.ErrAbortHandler)
			}
			oai.WriteError(w, oai.Internal())
		}()
		next.ServeHTTP(w, r)
	})
}

// readBody reads a request body after authentication: capped at
// LGAI_MAX_BODY_BYTES and bounded by LGAI_BODY_READ_TIMEOUT. The read deadline
// is cleared afterwards; a stale one would later cancel a running stream
// (ARCHITECTURE §5.4).
func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, *oai.Error) {
	limit := s.cfg.HTTP.MaxBodyBytes
	if r.ContentLength > limit {
		return nil, oai.TooLarge("", "request body is larger than %d bytes", limit)
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(s.cfg.HTTP.BodyReadTimeout))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	_ = rc.SetReadDeadline(time.Time{})
	if err == nil {
		return body, nil
	}
	var tooLarge *http.MaxBytesError
	var netErr net.Error
	switch {
	case errors.As(err, &tooLarge):
		return nil, oai.TooLarge("", "request body is larger than %d bytes", limit)
	case errors.As(err, &netErr) && netErr.Timeout():
		return nil, oai.NewError(http.StatusRequestTimeout, oai.TypeInvalidRequest, oai.CodeRequestTimeout, "",
			"the request body was not received in time")
	}
	return nil, oai.BadRequest(oai.CodeInvalidJSON, "", "could not read the request body")
}
