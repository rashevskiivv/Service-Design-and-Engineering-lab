// Package oai holds the OpenAI wire types the gateway speaks, the OpenAI-style
// error envelope, and the chat-request allowlist (NormalizeChat). Everything in
// it is a pure function of its inputs.
package oai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Error "type" values (OpenAI envelope).
const (
	TypeInvalidRequest = "invalid_request_error"
	TypeAuthentication = "authentication_error"
	TypePermission     = "permission_error"
	TypeNotFound       = "not_found_error"
	TypeRateLimit      = "rate_limit_error"
	TypeQuota          = "insufficient_quota"
	TypeServer         = "server_error"
)

// Error "code" values used across packages.
const (
	CodeInvalidJSON         = "invalid_json"
	CodeMissingField        = "missing_field"
	CodeInvalidValue        = "invalid_value"
	CodeUnknownField        = "unknown_field"
	CodeUnsupported         = "unsupported_parameter"
	CodeContextLength       = "context_length_exceeded"
	CodeTooLarge            = "request_too_large"
	CodeRequestTimeout      = "request_timeout"
	CodeInvalidAPIKey       = "invalid_api_key"
	CodeMissingAdminToken   = "missing_admin_token"
	CodeForbidden           = "forbidden"
	CodeNotFound            = "not_found"
	CodeMethodNotAllowed    = "method_not_allowed"
	CodeUnknownTask         = "unknown_task"
	CodeModelNotFound       = "model_not_found"
	CodeKeyNotFound         = "key_not_found"
	CodeRateLimit           = "rate_limit_exceeded"
	CodeConcurrency         = "concurrency_limit_exceeded"
	CodeQuota               = "insufficient_quota"
	CodeUpstreamError       = "upstream_error"
	CodeUpstreamUnavailable = "upstream_unavailable"
	CodeUpstreamTimeout     = "upstream_timeout"
	CodeOverloaded          = "server_overloaded"
	CodeMaintenance         = "maintenance"
	CodeInternal            = "internal_error"
)

// StatusClientClosed is the non-standard status recorded when the client went
// away before the response finished. It is never sent on the wire.
const StatusClientClosed = 499

// Error is an API error: an HTTP status plus the OpenAI error envelope, and the
// retry hints that go into response headers.
type Error struct {
	Status     int
	Type       string
	Code       string
	Param      string // "" → null
	Message    string
	RetryAfter time.Duration // > 0 → Retry-After header (whole seconds, ≥ 1)
	NoRetry    bool          // → X-Should-Retry: false
	// UpstreamStatus is the model server's real HTTP status behind a mapped
	// error (0 = none). It is logged, never sent to the client.
	UpstreamStatus int
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// NewError builds an Error.
func NewError(status int, typ, code, param, msg string) *Error {
	return &Error{Status: status, Type: typ, Code: code, Param: param, Message: msg}
}

// BadRequest is a 400 invalid_request_error with the given code.
func BadRequest(code, param, format string, args ...any) *Error {
	return NewError(http.StatusBadRequest, TypeInvalidRequest, code, param, fmt.Sprintf(format, args...))
}

// Invalid is a 400 invalid_value error.
func Invalid(param, format string, args ...any) *Error {
	return BadRequest(CodeInvalidValue, param, format, args...)
}

// Unsupported is a 400 unsupported_parameter error.
func Unsupported(param, format string, args ...any) *Error {
	return BadRequest(CodeUnsupported, param, format, args...)
}

// TooLarge is a 413 request_too_large error.
func TooLarge(param, format string, args ...any) *Error {
	return NewError(http.StatusRequestEntityTooLarge, TypeInvalidRequest, CodeTooLarge, param, fmt.Sprintf(format, args...))
}

// Internal is a generic 500 that reveals nothing.
func Internal() *Error {
	return NewError(http.StatusInternalServerError, TypeServer, CodeInternal, "", "internal error")
}

// ModelNotFound is the 404 for an unknown model alias. Long names are not echoed.
func ModelNotFound(name string) *Error {
	msg := "model not found; see GET /v1/models"
	if len(name) <= 64 {
		msg = fmt.Sprintf("model %q not found; see GET /v1/models", name)
	}
	return NewError(http.StatusNotFound, TypeNotFound, CodeModelNotFound, "model", msg)
}

type envelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    string  `json:"code"`
	Param   *string `json:"param"`
}

// Envelope returns the JSON-serialisable {"error":{...}} object.
func (e *Error) Envelope() any {
	b := errorBody{Message: e.Message, Type: e.Type, Code: e.Code}
	if e.Param != "" {
		p := e.Param
		b.Param = &p
	}
	return envelope{Error: b}
}

// RetryAfterSeconds rounds d up to whole seconds, minimum 1.
func RetryAfterSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	return max(s, 1)
}

// WriteError sends e as a JSON error response with its retry headers.
func WriteError(w http.ResponseWriter, e *Error) {
	h := w.Header()
	if e.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(RetryAfterSeconds(e.RetryAfter)))
	}
	if e.NoRetry {
		h.Set("X-Should-Retry", "false")
	}
	WriteJSON(w, e.Status, e.Envelope())
}

// WriteJSON sends v as an application/json response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		b, _ = json.Marshal(Internal().Envelope())
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
