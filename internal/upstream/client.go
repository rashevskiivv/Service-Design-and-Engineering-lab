// Package upstream is the HTTP client for the one OpenAI-compatible model
// server (Ollama, vLLM or llama.cpp). It never follows redirects, sends only an
// allowlisted set of headers, and maps failures to generic API errors.
package upstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"local-generative-ai/internal/config"
	"local-generative-ai/internal/oai"
)

const (
	maxErrorBody   = 64 << 10 // bytes of an upstream error body we look at
	maxErrorMsg    = 512      // bytes of an upstream 400 message passed to the client
	readyTimeout   = 2 * time.Second
	dialTimeout    = 5 * time.Second
	defaultRetryIn = 10 * time.Second
)

// Client talks to the upstream. Streaming requests use a transport with a
// response-header (first byte) timeout; non-streaming requests rely on the
// caller's overall timeout, because a non-streaming server sends headers only
// after the whole completion is generated (critic M4).
type Client struct {
	chatURL, modelsURL, healthURL string
	apiKey                        string
	headers                       http.Header
	stream, plain                 *http.Client
	retryAfter                    time.Duration
}

// New builds a client. maxConns is the gateway's global in-flight limit.
func New(cfg config.Upstream, maxConns int, overloadRetryAfter time.Duration) *Client {
	plain := &http.Transport{
		Proxy:               nil, // never route model traffic through HTTP_PROXY
		DialContext:         (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: dialTimeout,
		DisableCompression:  true, // never a gzip-buffered SSE
		MaxIdleConnsPerHost: max(maxConns, 1),
		IdleConnTimeout:     90 * time.Second,
	}
	stream := plain.Clone()
	stream.ResponseHeaderTimeout = cfg.HeaderTimeout
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	health := join(cfg.BaseURL, "/models")
	if cfg.HealthURL != nil {
		health = cfg.HealthURL.String()
	}
	return &Client{
		chatURL:    join(cfg.BaseURL, "/chat/completions"),
		modelsURL:  join(cfg.BaseURL, "/models"),
		healthURL:  health,
		apiKey:     cfg.APIKey,
		headers:    cfg.Headers.Clone(),
		stream:     &http.Client{Transport: stream, CheckRedirect: noRedirect},
		plain:      &http.Client{Transport: plain, CheckRedirect: noRedirect},
		retryAfter: orDefault(overloadRetryAfter, defaultRetryIn),
	}
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func join(base *url.URL, path string) string {
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	return u.String()
}

// newRequest builds an upstream request with exactly these headers:
// Content-Type, Accept, X-Request-ID, the upstream Authorization and
// LGAI_UPSTREAM_HEADERS. Nothing from the client request is copied (#9).
func (c *Client) newRequest(ctx context.Context, method, target string, body []byte, accept, requestID string) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header[k] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", accept)
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header["User-Agent"] = []string{""} // present but empty: net/http then sends none
	return req, nil
}

// ChatCompletions posts body to {base}/chat/completions. On a 2xx the caller
// owns resp.Body. Otherwise the body is drained and closed and a mapped error
// is returned; its Status is oai.StatusClientClosed if ctx was cancelled by the
// client going away (nothing should be sent then).
func (c *Client) ChatCompletions(ctx context.Context, body []byte, requestID string, stream bool) (*http.Response, *oai.Error) {
	accept, hc := "application/json", c.plain
	if stream {
		accept, hc = "text/event-stream", c.stream
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.chatURL, body, accept, requestID)
	if err != nil {
		return nil, upstreamError()
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, TransportError(ctx, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	e := c.mapStatus(resp.StatusCode, resp.Header.Get("Retry-After"), b)
	e.UpstreamStatus = resp.StatusCode
	return nil, e
}

// TransportError maps an error from sending the request or reading its body.
func TransportError(ctx context.Context, err error) *oai.Error {
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return timeoutError()
		}
		return oai.NewError(oai.StatusClientClosed, oai.TypeServer, oai.CodeUpstreamError, "", "client closed request")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return timeoutError()
	}
	return oai.NewError(http.StatusBadGateway, oai.TypeServer, oai.CodeUpstreamUnavailable, "", "upstream model server is unavailable")
}

func timeoutError() *oai.Error {
	return oai.NewError(http.StatusGatewayTimeout, oai.TypeServer, oai.CodeUpstreamTimeout, "", "upstream model server timed out")
}

func upstreamError() *oai.Error {
	return oai.NewError(http.StatusBadGateway, oai.TypeServer, oai.CodeUpstreamError, "", "upstream model server error")
}

// mapStatus maps a non-2xx upstream answer (security #24): only 400/422, which
// concern the client's own input, pass the upstream message through (≤ 512 B).
// Every other status gets a fixed message. 3xx (redirects are never followed)
// is a 502.
func (c *Client) mapStatus(status int, retryAfter string, body []byte) *oai.Error {
	switch {
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		msg := errorMessage(body)
		code := oai.CodeInvalidValue
		if l := strings.ToLower(msg); strings.Contains(l, "context length") || strings.Contains(l, "context_length") ||
			strings.Contains(l, "maximum context") || strings.Contains(l, "too many tokens") {
			code = oai.CodeContextLength
		}
		if msg == "" {
			msg = "the model server rejected the request"
		}
		return oai.BadRequest(code, "", "upstream rejected the request: %s", msg)
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
		e := oai.NewError(http.StatusServiceUnavailable, oai.TypeServer, oai.CodeOverloaded, "", "server busy; retry later")
		e.RetryAfter = c.retryAfter
		if s, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && s > 0 && s <= 600 {
			e.RetryAfter = time.Duration(s) * time.Second
		}
		return e
	}
	return upstreamError()
}

// errorMessage extracts a short message from an OpenAI/vLLM/Ollama error body.
func errorMessage(body []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  any             `json:"detail"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	msg := v.Message
	var inner struct {
		Message string `json:"message"`
	}
	var s string
	switch {
	case json.Unmarshal(v.Error, &inner) == nil && inner.Message != "":
		msg = inner.Message
	case json.Unmarshal(v.Error, &s) == nil && s != "":
		msg = s
	case msg == "" && v.Detail != nil:
		msg = fmt.Sprint(v.Detail)
	}
	return truncate(msg, maxErrorMsg)
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Ready probes the health URL (default {base}/models) with a 2 s timeout.
func (c *Client) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, c.healthURL, nil, "application/json", "")
	if err != nil {
		return err
	}
	resp, err := c.plain.Do(req)
	if err != nil {
		return stripURL(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

// ModelIDs lists the upstream's model ids ({base}/models); used for a startup
// warning when an alias points at a model that is not loaded.
func (c *Client) ModelIDs(ctx context.Context) ([]string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.modelsURL, nil, "application/json", "")
	if err != nil {
		return nil, err
	}
	resp, err := c.plain.Do(req)
	if err != nil {
		return nil, stripURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models status %d", resp.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list); err != nil {
		return nil, err
	}
	ids := make([]string, len(list.Data))
	for i, m := range list.Data {
		ids[i] = m.ID
	}
	return ids, nil
}

// stripURL drops the request URL from a transport error so it can be logged:
// a configured URL may carry credentials in its query string.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
