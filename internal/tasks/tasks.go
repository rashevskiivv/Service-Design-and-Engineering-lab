// Package tasks implements the coding-task endpoints (explain, review, tests,
// fix): request decoding and validation, and the language-aware prompts.
package tasks

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"local-generative-ai/internal/oai"
)

// Kind is a task name.
type Kind string

// The four tasks.
const (
	Explain Kind = "explain"
	Review  Kind = "review"
	Tests   Kind = "tests"
	Fix     Kind = "fix"
)

// Language is a supported programming language.
type Language string

// The five languages.
const (
	Python Language = "python"
	Java   Language = "java"
	Go     Language = "go"
	C      Language = "c"
	Cpp    Language = "cpp"
)

// ParseKind returns the task named s.
func ParseKind(s string) (Kind, bool) {
	k := Kind(s)
	_, ok := taskBlocks[k]
	return k, ok
}

// Defaults are a task's max_tokens and temperature when the client sets none.
type Defaults struct {
	MaxTokens   int
	Temperature float64
}

var defaults = map[Kind]Defaults{
	Explain: {512, 0.2},
	Review:  {1024, 0.2}, // bounded by the prompt to 5 findings (review m4)
	Tests:   {1024, 0.2},
	Fix:     {1024, 0.1},
}

// DefaultsFor returns the defaults of task k (always clamped to the cap later).
func DefaultsFor(k Kind) Defaults { return defaults[k] }

const maxInstructions = 1000

var frameworkPattern = regexp.MustCompile(`^[A-Za-z0-9 .+#()/_-]{1,64}$`)

// Request is a task request body (api/openapi.yaml TaskRequest). Unknown
// fields are refused.
type Request struct {
	Language      string             `json:"language"`
	Code          string             `json:"code"`
	Error         string             `json:"error"`
	Instructions  string             `json:"instructions"`
	Framework     string             `json:"framework"`
	Model         string             `json:"model"`
	Stream        bool               `json:"stream"`
	StreamOptions *oai.StreamOptions `json:"stream_options"`
	MaxTokens     json.RawMessage    `json:"max_tokens"`
	Temperature   *float64           `json:"temperature"`
}

// Decode parses a task body with DisallowUnknownFields, so typos such as
// "lang" fail loudly (ADR 0003).
func Decode(body []byte) (Request, *oai.Error) {
	var r Request
	e := oai.DecodeStrict(body, &r)
	return r, e
}

// Validate applies security §5.5 for task k.
func (r Request) Validate(k Kind, maxInputBytes int) *oai.Error {
	if r.Language == "" {
		return oai.BadRequest(oai.CodeMissingField, "language", "language is required: one of python, java, go, c, cpp")
	}
	if _, ok := languages[Language(r.Language)]; !ok {
		return oai.Invalid("language", "language must be one of python, java, go, c, cpp")
	}
	if strings.TrimSpace(r.Code) == "" {
		return oai.BadRequest(oai.CodeMissingField, "code", "code is required")
	}
	if k == Fix && strings.TrimSpace(r.Error) == "" {
		return oai.BadRequest(oai.CodeMissingField, "error", "error is required for fix: paste the compiler, runtime or test output")
	}
	if utf8.RuneCountInString(r.Instructions) > maxInstructions {
		return oai.Invalid("instructions", "instructions must be at most %d characters", maxInstructions)
	}
	if r.Framework != "" {
		if k != Tests {
			return oai.Invalid("framework", "framework is only accepted by the tests task")
		}
		if !frameworkPattern.MatchString(r.Framework) {
			return oai.Invalid("framework", "framework must match ^[A-Za-z0-9 .+#()/_-]{1,64}$")
		}
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 2) {
		return oai.Invalid("temperature", "temperature must be a number in [0, 2]")
	}
	if _, e := r.RequestedMaxTokens(); e != nil {
		return e
	}
	if n := len(r.Code) + len(r.Error) + len(r.Instructions) + len(r.Framework); n > maxInputBytes {
		return oai.TooLarge("code", "task input is %d bytes; the limit is %d", n, maxInputBytes)
	}
	return nil
}

// RequestedMaxTokens returns the client's max_tokens, or 0 if unset.
func (r Request) RequestedMaxTokens() (int64, *oai.Error) {
	if len(r.MaxTokens) == 0 || string(r.MaxTokens) == "null" {
		return 0, nil
	}
	n, ok := oai.ParseInt(r.MaxTokens)
	if !ok || n < 1 {
		return 0, oai.Invalid("max_tokens", "max_tokens must be an integer >= 1")
	}
	return n, nil
}

// Messages returns the system and user messages for a validated request.
func Messages(k Kind, r Request) []oai.Message {
	return []oai.Message{
		oai.TextMessage("system", systemPrompts[k][Language(r.Language)]),
		oai.TextMessage("user", userMessage(k, r)),
	}
}
