package oai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// DecodeStrict decodes a JSON object body into v, refusing unknown fields,
// invalid UTF-8, nesting deeper than MaxDepth and trailing data. It is used for
// the task and admin bodies (the chat body goes through NormalizeChat).
func DecodeStrict(body []byte, v any) *Error {
	if !utf8.Valid(body) {
		return BadRequest(CodeInvalidJSON, "", "request body must be valid UTF-8")
	}
	if !DepthOK(body, MaxDepth) {
		return BadRequest(CodeInvalidJSON, "", "JSON nesting is deeper than %d levels", MaxDepth)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return BadRequest(CodeInvalidJSON, "", "unexpected data after the JSON object")
	}
	return nil
}

func decodeError(err error) *Error {
	const unknown = "json: unknown field "
	var typeErr *json.UnmarshalTypeError
	switch {
	case strings.HasPrefix(err.Error(), unknown):
		field := strings.Trim(strings.TrimPrefix(err.Error(), unknown), `"`)
		if len(field) > 64 {
			field = field[:64]
		}
		return BadRequest(CodeUnknownField, field, "unknown field %q", field)
	case errors.As(err, &typeErr) && typeErr.Field != "":
		return Invalid(typeErr.Field, "%s has the wrong type", typeErr.Field)
	case errors.As(err, &typeErr):
		return BadRequest(CodeInvalidJSON, "", "request body must be a JSON object")
	}
	return BadRequest(CodeInvalidJSON, "", "invalid JSON body")
}
