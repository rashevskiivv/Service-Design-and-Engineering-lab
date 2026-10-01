package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"
)

// Result classes. 429 and 503 are expected under load and are reported
// apart from errors (docs/research/performance.md §6.5.7).
const (
	classOK          = "ok"
	classHTTP401     = "http_401"
	classHTTP429     = "http_429"
	classHTTP503     = "http_503"
	classHTTP4xx     = "http_4xx"
	classHTTP5xx     = "http_5xx"
	classHTTPOther   = "http_other"
	classStreamError = "stream_error"     // a data: {"error":...} event after 200
	classTruncated   = "stream_truncated" // EOF without data: [DONE]
	classBadResponse = "bad_response"     // 200 with an unparseable body
	classTimeout     = "timeout"          // -timeout exceeded
	classConnError   = "conn_error"       // dial, reset, TLS, ...
	classCancelled   = "cancelled"        // the run was interrupted (Ctrl-C)
)

const (
	tokensFromUsage  = "usage"
	tokensFromChunks = "chunks"
)

type runner struct {
	cfg    *config
	corpus corpus
	client *http.Client
	now    func() time.Time
	sleep  func(ctx context.Context, d time.Duration) error
	stderr io.Writer
}

func newRunner(cfg *config, stderr io.Writer) (*runner, error) {
	c, err := loadCorpus()
	if err != nil {
		return nil, err
	}
	return &runner{
		cfg:    cfg,
		corpus: c,
		client: newHTTPClient(cfg.conc),
		now:    time.Now,
		sleep:  sleepCtx,
		stderr: stderr,
	}, nil
}

// newHTTPClient returns a client that does not become the bottleneck: one
// HTTP/1.1 connection per concurrent stream (like real students), no gzip
// (it would buffer the stream), no redirects (a redirect would be a
// misconfigured URL, and Go drops Authorization on cross-host redirects).
func newHTTPClient(conc int) *http.Client {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: max(conc, 2),
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type job struct {
	index    int
	endpoint string // chat or a task name
	lang     string
	size     string
	sn       snippet
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
}

// taskRequest follows TaskRequest in api/openapi.yaml.
type taskRequest struct {
	Language      string         `json:"language"`
	Code          string         `json:"code"`
	Error         string         `json:"error,omitempty"`
	Model         string         `json:"model,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
}

func (r *runner) buildBody(j job) ([]byte, error) {
	var so *streamOptions
	if r.cfg.stream {
		so = &streamOptions{IncludeUsage: true}
	}
	if j.endpoint == "chat" {
		return json.Marshal(chatRequest{
			Model: r.cfg.model,
			Messages: []chatMessage{
				{Role: "system", Content: chatSystemPrompt},
				{Role: "user", Content: chatPrompt(j.sn)},
			},
			Stream:        r.cfg.stream,
			StreamOptions: so,
			MaxTokens:     r.cfg.maxTokens,
		})
	}
	tr := taskRequest{
		Language:      j.lang,
		Code:          j.sn.code,
		Model:         r.cfg.model,
		Stream:        r.cfg.stream,
		StreamOptions: so,
		MaxTokens:     r.cfg.maxTokens,
	}
	if j.endpoint == "fix" {
		tr.Error = j.sn.errText
	}
	return json.Marshal(tr)
}

func (r *runner) endpointURL(endpoint string) string {
	if endpoint == "chat" {
		return r.cfg.baseURL + "/v1/chat/completions"
	}
	return r.cfg.baseURL + "/v1/tasks/" + endpoint
}

// result is one request's outcome. The exported fields go to -json; the
// answer text never does.
type result struct {
	Index            int      `json:"index"`
	Endpoint         string   `json:"endpoint"`
	Lang             string   `json:"lang"`
	Size             string   `json:"size"`
	Stream           bool     `json:"stream"`
	StartMs          float64  `json:"start_ms"`
	Status           int      `json:"status"`
	Class            string   `json:"class"`
	ErrorCode        string   `json:"error_code,omitempty"`
	Error            string   `json:"error,omitempty"`
	RetryAfter       string   `json:"retry_after,omitempty"`
	TTFBMs           *float64 `json:"ttfb_ms,omitempty"`
	TTFTMs           *float64 `json:"ttft_ms,omitempty"`
	TTFCMs           *float64 `json:"ttfc_ms,omitempty"`
	E2EMs            float64  `json:"e2e_ms"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TokensSource     string   `json:"tokens_source,omitempty"`
	ContentChunks    int      `json:"content_chunks"`
	ReasoningChunks  int      `json:"reasoning_chunks"`
	DecodeTokPerSec  *float64 `json:"decode_tok_s,omitempty"`
	FinishReason     string   `json:"finish_reason,omitempty"`
	ContentBytes     int      `json:"content_bytes"`
	ReasoningBytes   int      `json:"reasoning_bytes"`
	Attempts         int      `json:"attempts,omitempty"`

	// Internal measurements, relative to t0 (just before client.Do).
	ttfb, ttft, ttfc, lastTok, e2e time.Duration
	hasTTFB, hasTTFT, hasTTFC      bool
	shouldRetry                    string // X-Should-Retry on 429/503
	content                        string // kept only in smoke mode
}

func (res *result) finalize() {
	if res.hasTTFB {
		res.TTFBMs = msPtr(res.ttfb)
	}
	if res.hasTTFT {
		res.TTFTMs = msPtr(res.ttft)
	}
	if res.hasTTFC {
		res.TTFCMs = msPtr(res.ttfc)
	}
	res.E2EMs = ms(res.e2e)
	res.DecodeTokPerSec = nil
	// Per-stream decode speed excludes the wait for the first token:
	// (tokens - 1) / (t_last_token - t_first_token), performance.md §6.5.6.
	if res.Stream && res.hasTTFT && res.CompletionTokens > 1 && res.lastTok > res.ttft {
		v := float64(res.CompletionTokens-1) / (res.lastTok - res.ttft).Seconds()
		res.DecodeTokPerSec = &v
	}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func msPtr(d time.Duration) *float64 {
	v := ms(d)
	return &v
}

// do sends one request and measures it. keepContent keeps the answer text
// (smoke mode only).
func (r *runner) do(ctx context.Context, j job, runStart time.Time, keepContent bool) result {
	res := result{Index: j.index, Endpoint: j.endpoint, Lang: j.lang, Size: j.size, Stream: r.cfg.stream, Attempts: 1}
	body, err := r.buildBody(j)
	if err != nil {
		res.Class, res.Error = classBadResponse, "encode request: "+err.Error()
		res.finalize()
		return res
	}
	reqCtx, cancel := context.WithTimeout(ctx, r.cfg.timeout)
	defer cancel()

	var t0 time.Time
	var ttfbNs atomic.Int64
	trace := &httptrace.ClientTrace{
		GotFirstResponseByte: func() { ttfbNs.Store(int64(r.now().Sub(t0))) },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(reqCtx, trace), http.MethodPost, r.endpointURL(j.endpoint), bytes.NewReader(body))
	if err != nil {
		res.Class, res.Error = classConnError, err.Error()
		res.finalize()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	if r.cfg.stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if r.cfg.key != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.key)
	}

	t0 = r.now()
	res.StartMs = ms(t0.Sub(runStart))
	resp, err := r.client.Do(req)
	if v := ttfbNs.Load(); v > 0 {
		res.ttfb, res.hasTTFB = time.Duration(v), true
	}
	if err != nil {
		res.Class, res.Error = classifyTransportErr(err, ctx, reqCtx)
		res.e2e = r.now().Sub(t0)
		res.finalize()
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode

	if resp.StatusCode != http.StatusOK {
		res.RetryAfter = resp.Header.Get("Retry-After")
		res.shouldRetry = resp.Header.Get("X-Should-Retry")
		res.ErrorCode, res.Error = readErrorBody(resp.Body)
		res.Class = classForStatus(resp.StatusCode)
		res.e2e = r.now().Sub(t0)
		res.finalize()
		return res
	}

	var readErr error
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		readErr = consumeSSE(resp.Body, t0, r.now, &res, keepContent)
	} else {
		readErr = consumeJSON(resp.Body, t0, r.now, &res, keepContent)
	}
	res.e2e = r.now().Sub(t0)
	if readErr != nil && res.Class == "" {
		res.Class, res.Error = classifyTransportErr(readErr, ctx, reqCtx)
	}
	if res.Class == "" {
		res.Class = classOK
	}
	res.finalize()
	return res
}

func classForStatus(code int) string {
	switch {
	case code == http.StatusUnauthorized:
		return classHTTP401
	case code == http.StatusTooManyRequests:
		return classHTTP429
	case code == http.StatusServiceUnavailable:
		return classHTTP503
	case code >= 400 && code < 500:
		return classHTTP4xx
	case code >= 500:
		return classHTTP5xx
	}
	return classHTTPOther
}

func classifyTransportErr(err error, parent, reqCtx context.Context) (class, msg string) {
	switch {
	case parent.Err() != nil:
		return classCancelled, "run interrupted"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded):
		return classTimeout, "request exceeded -timeout"
	case errors.Is(err, errSSELineTooLong):
		return classBadResponse, err.Error()
	}
	return classConnError, err.Error()
}

// apiError is the OpenAI error object; vLLM also puts message/code at the
// top level with "object":"error" and a numeric code.
type apiError struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
}

func rawCode(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// decodeErrorField understands {"error":{...}} and {"error":"text"}.
func decodeErrorField(raw json.RawMessage) (code, msg string, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", false
	}
	var e apiError
	if json.Unmarshal(raw, &e) == nil {
		return rawCode(e.Code), e.Message, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return "", s, true
	}
	return "", string(raw), true
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func readErrorBody(body io.Reader) (code, msg string) {
	b, _ := io.ReadAll(io.LimitReader(body, 64<<10))
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
	}
	if json.Unmarshal(b, &env) == nil {
		if c, m, ok := decodeErrorField(env.Error); ok {
			return c, truncate(m, 200)
		}
		if env.Message != "" {
			return rawCode(env.Code), truncate(env.Message, 200)
		}
	}
	return "", truncate(string(b), 200)
}

type usageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type streamChunk struct {
	Object  string          `json:"object"`
	Message string          `json:"message"`
	Code    json.RawMessage `json:"code"`
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Delta struct {
			Content          string            `json:"content"`
			Reasoning        string            `json:"reasoning"`
			ReasoningContent string            `json:"reasoning_content"`
			ToolCalls        []json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageJSON `json:"usage"`
}

// consumeSSE reads a chat.completion.chunk stream and fills the timing and
// token fields of res. t0 is the instant just before the request was sent.
//
//   - TTFT: first event with non-empty content, reasoning or tool_calls
//     (a role-only first chunk with content "" does not count).
//   - TTFC: first event with non-empty delta.content, what a student sees.
//   - tokens: usage.completion_tokens when a usage chunk arrives, else the
//     number of token-bearing events.
//
// It returns a non-nil error only for read failures; protocol outcomes are
// recorded in res.Class.
func consumeSSE(body io.Reader, t0 time.Time, now func() time.Time, res *result, keepContent bool) error {
	sr := newSSEReader(body)
	var (
		content     strings.Builder
		usage       *usageJSON
		tokenEvents int
		readErr     error
	)
loop:
	for {
		ev, err := sr.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if res.Class == "" {
					res.Class, res.Error = classTruncated, "stream ended without data: [DONE]"
				}
			} else {
				readErr = err // the caller classifies it (timeout, reset, ...)
			}
			break
		}
		t := now().Sub(t0)
		if ev.data == "[DONE]" {
			if res.Class == "" {
				res.Class = classOK
			}
			break
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(ev.data), &ch); err != nil {
			res.Class, res.Error = classBadResponse, "SSE event is not JSON: "+truncate(ev.data, 120)
			break
		}
		if code, msg, ok := decodeErrorField(ch.Error); ok || ev.event == "error" || ch.Object == "error" {
			if !ok {
				code, msg = rawCode(ch.Code), ch.Message
			}
			res.Class, res.ErrorCode, res.Error = classStreamError, code, truncate(msg, 200)
			break loop
		}
		if ch.Usage != nil {
			usage = ch.Usage
		}
		if len(ch.Choices) == 0 {
			continue
		}
		c := ch.Choices[0]
		reasoning := len(c.Delta.Reasoning) + len(c.Delta.ReasoningContent)
		if len(c.Delta.Content) > 0 || reasoning > 0 || len(c.Delta.ToolCalls) > 0 {
			tokenEvents++
			if !res.hasTTFT {
				res.ttft, res.hasTTFT = t, true
			}
			res.lastTok = t
		}
		if len(c.Delta.Content) > 0 {
			res.ContentChunks++
			res.ContentBytes += len(c.Delta.Content)
			if !res.hasTTFC {
				res.ttfc, res.hasTTFC = t, true
			}
			if keepContent {
				content.WriteString(c.Delta.Content)
			}
		}
		if reasoning > 0 {
			res.ReasoningChunks++
			res.ReasoningBytes += reasoning
		}
		if c.FinishReason != "" {
			res.FinishReason = c.FinishReason
		}
	}
	if usage != nil {
		res.PromptTokens, res.CompletionTokens, res.TokensSource = usage.PromptTokens, usage.CompletionTokens, tokensFromUsage
	} else {
		res.CompletionTokens, res.TokensSource = tokenEvents, tokensFromChunks
	}
	if keepContent {
		res.content = content.String()
	}
	return readErr
}

type completionJSON struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageJSON      `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// consumeJSON reads a non-streaming chat.completion. Content only becomes
// visible when the whole body has arrived, so TTFT = TTFC = that instant.
func consumeJSON(body io.Reader, t0 time.Time, now func() time.Time, res *result, keepContent bool) error {
	b, err := io.ReadAll(io.LimitReader(body, 16<<20))
	if err != nil {
		return err
	}
	t := now().Sub(t0)
	var cj completionJSON
	if err := json.Unmarshal(b, &cj); err != nil {
		res.Class, res.Error = classBadResponse, "response is not JSON: "+truncate(string(b), 120)
		return nil
	}
	if code, msg, ok := decodeErrorField(cj.Error); ok {
		res.Class, res.ErrorCode, res.Error = classBadResponse, code, truncate(msg, 200)
		return nil
	}
	if len(cj.Choices) > 0 {
		m := cj.Choices[0].Message
		res.FinishReason = cj.Choices[0].FinishReason
		res.ContentBytes = len(m.Content)
		res.ReasoningBytes = len(m.Reasoning) + len(m.ReasoningContent)
		if res.ContentBytes > 0 {
			res.ttfc, res.hasTTFC = t, true
			res.ContentChunks = 1
		}
		if res.ContentBytes+res.ReasoningBytes > 0 {
			res.ttft, res.hasTTFT, res.lastTok = t, true, t
		}
		if keepContent {
			res.content = m.Content
		}
	}
	if cj.Usage != nil {
		res.PromptTokens, res.CompletionTokens, res.TokensSource = cj.Usage.PromptTokens, cj.Usage.CompletionTokens, tokensFromUsage
	}
	return nil
}

// describe renders one result as a single log line (no content, no key).
func (res *result) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s/%s/%s status=%d %s", res.Index, res.Endpoint, res.Lang, res.Size, res.Status, res.Class)
	if res.ErrorCode != "" {
		fmt.Fprintf(&b, " code=%s", res.ErrorCode)
	}
	if res.RetryAfter != "" {
		fmt.Fprintf(&b, " retry-after=%s", res.RetryAfter)
	}
	if res.hasTTFC {
		fmt.Fprintf(&b, " ttfc=%.2fs", res.ttfc.Seconds())
	}
	fmt.Fprintf(&b, " e2e=%.2fs", res.e2e.Seconds())
	if res.TokensSource != "" {
		fmt.Fprintf(&b, " tokens=%d(%s)", res.CompletionTokens, res.TokensSource)
	}
	if res.DecodeTokPerSec != nil {
		fmt.Fprintf(&b, " %.1ftok/s", *res.DecodeTokPerSec)
	}
	if res.Error != "" && res.Class != classOK {
		fmt.Fprintf(&b, " error=%q", truncate(res.Error, 100))
	}
	return b.String()
}
