package oai

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// Model is a public alias and what it maps to upstream.
type Model struct {
	Alias    string
	Upstream string
	Defaults ModelDefaults
}

// TokenLimits are the server-side max_tokens rules shared by chat and tasks.
type TokenLimits struct {
	Cap          int // LGAI_MAX_TOKENS_CAP, every request
	NonStreamCap int // LGAI_NONSTREAM_MAX_TOKENS, non-streaming requests only (critic M4)
}

// Clamp returns the effective max_tokens: requested (0 = unset → def), then
// clamped to Cap, and for non-streaming requests also to NonStreamCap.
func (l TokenLimits) Clamp(requested int64, def int, stream bool) int {
	n := int64(def)
	if requested > 0 {
		n = requested
	}
	limit := l.Cap
	if !stream && l.NonStreamCap > 0 && l.NonStreamCap < limit {
		limit = l.NonStreamCap
	}
	return int(min(n, int64(limit)))
}

// ModelDefaults are per-alias values (LGAI_MODEL_DEFAULTS) merged into every
// upstream request whose client did not set them (critic M3).
type ModelDefaults struct {
	Temperature     *float64
	TopP            *float64
	TopK            *int
	PresencePenalty *float64
	ReasoningEffort string
	EnableThinking  *bool
}

// Apply fills the fields of r that are still unset.
func (d ModelDefaults) Apply(r *ChatRequest) {
	if r.Temperature == nil {
		r.Temperature = clone(d.Temperature)
	}
	if r.TopP == nil {
		r.TopP = clone(d.TopP)
	}
	if r.TopK == nil {
		r.TopK = clone(d.TopK)
	}
	if r.PresencePenalty == nil {
		r.PresencePenalty = clone(d.PresencePenalty)
	}
	if r.ReasoningEffort == "" {
		r.ReasoningEffort = d.ReasoningEffort
	}
	if r.ChatTemplateKwargs == nil && d.EnableThinking != nil {
		r.ChatTemplateKwargs = &ChatTemplateKwargs{EnableThinking: *d.EnableThinking}
	}
}

// String renders the defaults as the JSON they were parsed from (for the
// startup config log; nothing in it is secret).
func (d ModelDefaults) String() string {
	var r ChatRequest
	d.Apply(&r)
	b, _ := json.Marshal(struct {
		Temperature        *float64            `json:"temperature,omitempty"`
		TopP               *float64            `json:"top_p,omitempty"`
		TopK               *int                `json:"top_k,omitempty"`
		PresencePenalty    *float64            `json:"presence_penalty,omitempty"`
		ReasoningEffort    string              `json:"reasoning_effort,omitempty"`
		ChatTemplateKwargs *ChatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
	}{r.Temperature, r.TopP, r.TopK, r.PresencePenalty, r.ReasoningEffort, r.ChatTemplateKwargs})
	return string(b)
}

func clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

var defaultsAllowed = map[string]bool{
	"temperature": true, "top_p": true, "top_k": true, "presence_penalty": true,
	"reasoning_effort": true, "chat_template_kwargs": true,
}

// ParseModelDefaults parses one alias's LGAI_MODEL_DEFAULTS object. Only the
// allowlisted fields are accepted, with the same rules as client requests.
func ParseModelDefaults(raw []byte) (ModelDefaults, error) {
	f, ok := decodeObject(raw)
	if !ok {
		return ModelDefaults{}, errors.New("must be a JSON object")
	}
	for k := range f {
		if !defaultsAllowed[k] {
			names := make([]string, 0, len(defaultsAllowed))
			for n := range defaultsAllowed {
				names = append(names, n)
			}
			sort.Strings(names)
			return ModelDefaults{}, errors.New("field " + k + " is not allowed; allowed: " + strings.Join(names, ", "))
		}
	}
	var d ModelDefaults
	var e *Error
	if d.Temperature, e = temperatureField(f); e != nil {
		return d, e
	}
	if d.TopP, e = topPField(f); e != nil {
		return d, e
	}
	if d.TopK, e = topKField(f); e != nil {
		return d, e
	}
	if d.PresencePenalty, e = penaltyField(f, "presence_penalty"); e != nil {
		return d, e
	}
	if d.ReasoningEffort, e = reasoningField(f); e != nil {
		return d, e
	}
	kw, e := templateKwargsField(f)
	if e != nil {
		return d, e
	}
	if kw != nil {
		d.EnableThinking = &kw.EnableThinking
	}
	return d, nil
}

// ---- field validators shared by NormalizeChat and ParseModelDefaults ----

func floatField(f fields, key, rule string, ok func(float64) bool) (*float64, *Error) {
	v, present := f.get(key)
	if !present {
		return nil, nil
	}
	x, isNum := decodeNumber(v)
	if !isNum || !ok(x) {
		return nil, Invalid(key, "%s must be a number %s", key, rule)
	}
	return &x, nil
}

func temperatureField(f fields) (*float64, *Error) {
	return floatField(f, "temperature", "in [0, 2]", func(x float64) bool { return x >= 0 && x <= 2 })
}

func topPField(f fields) (*float64, *Error) {
	return floatField(f, "top_p", "in (0, 1]", func(x float64) bool { return x > 0 && x <= 1 })
}

func penaltyField(f fields, key string) (*float64, *Error) {
	return floatField(f, key, "in [-2, 2]", func(x float64) bool { return x >= -2 && x <= 2 })
}

func topKField(f fields) (*int, *Error) {
	v, present := f.get("top_k")
	if !present {
		return nil, nil
	}
	i, ok := ParseInt(v)
	if !ok || !(i == -1 || i == 0 || (i >= 1 && i <= 100)) {
		return nil, Invalid("top_k", "top_k must be -1, 0 or an integer in [1, 100]")
	}
	k := int(i)
	return &k, nil
}

func reasoningField(f fields) (string, *Error) {
	v, present := f.get("reasoning_effort")
	if !present {
		return "", nil
	}
	s, _ := decodeString(v)
	switch s {
	case "low", "medium", "high":
		return s, nil
	}
	return "", Invalid("reasoning_effort", "reasoning_effort must be low, medium or high")
}

// templateKwargsField accepts exactly {"enable_thinking": bool}; any other key
// or type is refused (security #4, CVE-2025-61620 and CVE-2025-62426).
func templateKwargsField(f fields) (*ChatTemplateKwargs, *Error) {
	v, present := f.get("chat_template_kwargs")
	if !present {
		return nil, nil
	}
	kw, ok := decodeObject(v)
	if !ok {
		return nil, Unsupported("chat_template_kwargs", "chat_template_kwargs must be {\"enable_thinking\": bool}")
	}
	for k := range kw {
		if k != "enable_thinking" {
			return nil, Unsupported("chat_template_kwargs", "chat_template_kwargs only accepts enable_thinking")
		}
	}
	raw, present := kw.get("enable_thinking")
	if !present {
		return nil, nil
	}
	b, ok := decodeBool(raw)
	if !ok {
		return nil, Unsupported("chat_template_kwargs", "chat_template_kwargs.enable_thinking must be a boolean")
	}
	return &ChatTemplateKwargs{EnableThinking: b}, nil
}
