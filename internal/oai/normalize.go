package oai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Limits from security review §5.1–§5.2.
const (
	maxModelLen       = 64
	maxMessages       = 128
	maxContentParts   = 64
	maxTools          = 16
	maxToolCalls      = 16
	maxStops          = 4
	maxStopBytes      = 64
	maxIDLen          = 64
	maxToolDescBytes  = 1024
	maxToolParamBytes = 8 << 10
	maxToolParamDepth = 8
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ChatPolicy is the server-side configuration NormalizeChat applies.
type ChatPolicy struct {
	// Resolve maps a public alias ("" = the default model) to a Model.
	Resolve          func(alias string) (Model, bool)
	DefaultMaxTokens int
	Tokens           TokenLimits
	MaxInputBytes    int // LGAI_CHAT_MAX_INPUT_BYTES
}

// Normalized is a validated chat request, ready to send upstream.
type Normalized struct {
	Request          ChatRequest
	PublicModel      string
	ClientWantsUsage bool // the client itself asked for the usage chunk
	PromptBytes      int  // message text + tools; for the usage estimate
}

// NormalizeChat validates a /v1/chat/completions body against the typed
// allowlist in security review §5 and builds the upstream request from the
// validated values only. Unknown top-level and message fields are dropped;
// dangerous or unsupported ones are refused.
func NormalizeChat(raw []byte, p ChatPolicy) (Normalized, *Error) {
	var n Normalized
	if !utf8.Valid(raw) {
		return n, BadRequest(CodeInvalidJSON, "", "request body must be valid UTF-8")
	}
	if !DepthOK(raw, MaxDepth) {
		return n, BadRequest(CodeInvalidJSON, "", "JSON nesting is deeper than %d levels", MaxDepth)
	}
	f, ok := decodeObject(raw)
	if !ok {
		return n, BadRequest(CodeInvalidJSON, "", "request body must be a JSON object")
	}

	model, e := resolveModel(f, p.Resolve)
	if e != nil {
		return n, e
	}
	req := &n.Request
	req.Model, n.PublicModel = model.Upstream, model.Alias

	if v, ok := f.get("n"); ok {
		if i, isInt := ParseInt(v); !isInt || i != 1 {
			return n, Unsupported("n", "n must be 1; multiple choices are not supported")
		}
	}
	if n.ClientWantsUsage, e = streamFields(f, req); e != nil {
		return n, e
	}

	msgRaw, present := f.get("messages")
	if !present {
		return n, BadRequest(CodeMissingField, "messages", "messages is required")
	}
	var textBytes, toolBytes int
	if req.Messages, textBytes, e = parseMessages(msgRaw); e != nil {
		return n, e
	}
	if req.Tools, toolBytes, e = parseTools(f); e != nil {
		return n, e
	}
	n.PromptBytes = textBytes + toolBytes
	if n.PromptBytes > p.MaxInputBytes {
		return n, TooLarge("messages", "chat input is %d bytes; the limit is %d", n.PromptBytes, p.MaxInputBytes)
	}

	requested, e := maxTokensFields(f)
	if e != nil {
		return n, e
	}
	req.MaxTokens = p.Tokens.Clamp(requested, p.DefaultMaxTokens, req.Stream)

	if e = samplingFields(f, req); e != nil {
		return n, e
	}
	if e = toolAndFormatFields(f, req); e != nil {
		return n, e
	}
	model.Defaults.Apply(req)
	return n, nil
}

func resolveModel(f fields, resolve func(string) (Model, bool)) (Model, *Error) {
	alias := ""
	if v, ok := f.get("model"); ok {
		s, isStr := decodeString(v)
		if !isStr {
			return Model{}, Invalid("model", "model must be a string")
		}
		alias = s
	}
	if len(alias) > maxModelLen {
		return Model{}, ModelNotFound(alias)
	}
	m, ok := resolve(alias)
	if !ok {
		return Model{}, ModelNotFound(alias)
	}
	return m, nil
}

// streamFields sets stream and the forced stream_options (ADR 0004) and
// reports whether the client asked for the usage chunk itself.
func streamFields(f fields, req *ChatRequest) (bool, *Error) {
	if v, ok := f.get("stream"); ok {
		b, isBool := decodeBool(v)
		if !isBool {
			return false, Invalid("stream", "stream must be a boolean")
		}
		req.Stream = b
	}
	if !req.Stream {
		return false, nil // stream_options without stream is dropped
	}
	req.StreamOptions = &StreamOptions{IncludeUsage: true}
	v, ok := f.get("stream_options")
	if !ok {
		return false, nil
	}
	so, isObj := decodeObject(v)
	if !isObj {
		return false, Invalid("stream_options", "stream_options must be an object")
	}
	iu, ok := so.get("include_usage")
	if !ok {
		return false, nil
	}
	b, isBool := decodeBool(iu)
	if !isBool {
		return false, Invalid("stream_options.include_usage", "include_usage must be a boolean")
	}
	return b, nil
}

// maxTokensFields merges max_tokens and max_completion_tokens (the smaller
// wins). The result is 0 when neither is set. max_completion_tokens is never
// forwarded.
func maxTokensFields(f fields) (int64, *Error) {
	var requested int64
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		v, ok := f.get(key)
		if !ok {
			continue
		}
		i, isInt := ParseInt(v)
		if !isInt || i < 1 {
			return 0, Invalid(key, "%s must be an integer >= 1", key)
		}
		if requested == 0 || i < requested {
			requested = i
		}
	}
	return requested, nil
}

func samplingFields(f fields, req *ChatRequest) *Error {
	var e *Error
	if req.Temperature, e = temperatureField(f); e != nil {
		return e
	}
	if req.TopP, e = topPField(f); e != nil {
		return e
	}
	if req.TopK, e = topKField(f); e != nil {
		return e
	}
	if req.FrequencyPenalty, e = penaltyField(f, "frequency_penalty"); e != nil {
		return e
	}
	if req.PresencePenalty, e = penaltyField(f, "presence_penalty"); e != nil {
		return e
	}
	if v, ok := f.get("seed"); ok {
		s, isInt := ParseInt(v)
		if !isInt {
			return Invalid("seed", "seed must be an integer")
		}
		req.Seed = &s
	}
	if v, ok := f.get("stop"); ok {
		if req.Stop, e = parseStop(v); e != nil {
			return e
		}
	}
	if req.ReasoningEffort, e = reasoningField(f); e != nil {
		return e
	}
	req.ChatTemplateKwargs, e = templateKwargsField(f)
	return e
}

func parseStop(raw json.RawMessage) ([]string, *Error) {
	var list []string
	if s, ok := decodeString(raw); ok {
		list = []string{s}
	} else {
		arr, isArr := decodeArray(raw)
		if !isArr {
			return nil, Invalid("stop", "stop must be a string or an array of strings")
		}
		for _, it := range arr {
			s, isStr := decodeString(it)
			if !isStr {
				return nil, Invalid("stop", "stop must be a string or an array of strings")
			}
			list = append(list, s)
		}
	}
	if len(list) > maxStops {
		return nil, Invalid("stop", "at most %d stop sequences are allowed", maxStops)
	}
	for _, s := range list {
		if len(s) == 0 || len(s) > maxStopBytes {
			return nil, Invalid("stop", "each stop sequence must be 1-%d bytes", maxStopBytes)
		}
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

// toolAndFormatFields handles response_format, tool_choice and
// parallel_tool_calls; they force engine-side grammar work, so only the cheap
// variants pass (security #5).
func toolAndFormatFields(f fields, req *ChatRequest) *Error {
	if v, ok := f.get("response_format"); ok {
		rf, isObj := decodeObject(v)
		if !isObj {
			return Invalid("response_format", "response_format must be an object")
		}
		tv, present := rf.get("type")
		if !present {
			return BadRequest(CodeMissingField, "response_format.type", "response_format.type is required")
		}
		switch t, _ := decodeString(tv); t {
		case "text", "json_object":
			req.ResponseFormat = &ResponseFormat{Type: t}
		default:
			return Unsupported("response_format.type", "response_format.type must be text or json_object")
		}
	}
	if v, ok := f.get("tool_choice"); ok {
		s, isStr := decodeString(v)
		switch {
		case isStr && (s == "none" || s == "auto"):
			req.ToolChoice = s
		case isStr && s != "required":
			return Invalid("tool_choice", "tool_choice must be \"none\" or \"auto\"")
		default:
			return Unsupported("tool_choice", "tool_choice must be \"none\" or \"auto\"; forced tool calls are not supported")
		}
	}
	if v, ok := f.get("parallel_tool_calls"); ok {
		b, isBool := decodeBool(v)
		if !isBool {
			return Invalid("parallel_tool_calls", "parallel_tool_calls must be a boolean")
		}
		if len(req.Tools) > 0 {
			req.ParallelToolCalls = &b
		}
	}
	return nil
}

// parseMessages applies security §5.2 and returns the messages plus the number
// of text bytes they carry (content and tool-call arguments).
func parseMessages(raw json.RawMessage) ([]Message, int, *Error) {
	arr, ok := decodeArray(raw)
	if !ok {
		return nil, 0, Invalid("messages", "messages must be an array")
	}
	if len(arr) == 0 {
		return nil, 0, Invalid("messages", "messages must not be empty")
	}
	if len(arr) > maxMessages {
		return nil, 0, Invalid("messages", "at most %d messages are allowed", maxMessages)
	}
	out := make([]Message, 0, len(arr))
	total := 0
	for i, m := range arr {
		msg, size, e := parseMessage(fmt.Sprintf("messages[%d]", i), m)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, msg)
		total += size
	}
	return out, total, nil
}

func parseMessage(at string, raw json.RawMessage) (Message, int, *Error) {
	var m Message
	f, ok := decodeObject(raw)
	if !ok {
		return m, 0, Invalid(at, "%s must be an object", at)
	}
	rv, _ := f.get("role")
	switch m.Role, _ = decodeString(rv); m.Role {
	case "system", "developer", "user", "assistant", "tool":
	default:
		return m, 0, Invalid(at+".role", "role must be one of system, developer, user, assistant, tool")
	}
	if v, ok := f.get("name"); ok {
		s, isStr := decodeString(v)
		if !isStr || !namePattern.MatchString(s) {
			return m, 0, Invalid(at+".name", "name must match ^[A-Za-z0-9_-]{1,64}$")
		}
		m.Name = s
	}
	size := 0
	if v, ok := f.get("tool_calls"); ok {
		if m.Role != "assistant" {
			return m, 0, Invalid(at+".tool_calls", "tool_calls is only allowed on assistant messages")
		}
		var e *Error
		if m.ToolCalls, size, e = parseToolCalls(at+".tool_calls", v); e != nil {
			return m, 0, e
		}
	}
	if v, ok := f.get("tool_call_id"); ok {
		s, isStr := decodeString(v)
		if m.Role != "tool" || !isStr || s == "" || len(s) > maxIDLen {
			return m, 0, Invalid(at+".tool_call_id", "tool_call_id is a 1-%d byte string on tool messages only", maxIDLen)
		}
		m.ToolCallID = s
	}
	v, ok := f.get("content")
	switch {
	case !ok && m.Role == "assistant" && len(m.ToolCalls) > 0:
		// content: null is allowed here and forwarded as null.
	case !ok:
		return m, 0, BadRequest(CodeMissingField, at+".content", "%s.content is required", at)
	default:
		text, e := parseContent(at+".content", v)
		if e != nil {
			return m, 0, e
		}
		m.Content = &text
		size += len(text)
	}
	return m, size, nil
}

// parseContent accepts a string or an array of text parts, flattened to one
// string. Any other part type (image_url, input_audio, file, …) is refused:
// the engine must never fetch media (security #3, #6).
func parseContent(at string, raw json.RawMessage) (string, *Error) {
	if s, ok := decodeString(raw); ok {
		return s, nil
	}
	parts, ok := decodeArray(raw)
	if !ok {
		return "", Invalid(at, "content must be a string or an array of text parts")
	}
	if len(parts) > maxContentParts {
		return "", Invalid(at, "at most %d content parts are allowed", maxContentParts)
	}
	texts := make([]string, 0, len(parts))
	for j, part := range parts {
		pat := fmt.Sprintf("%s[%d]", at, j)
		pf, isObj := decodeObject(part)
		if !isObj {
			return "", Invalid(pat, "content part must be an object")
		}
		tv, _ := pf.get("type")
		if typ, _ := decodeString(tv); typ != "text" {
			return "", Unsupported(pat+".type", "only text content parts are supported")
		}
		xv, _ := pf.get("text")
		s, isStr := decodeString(xv)
		if !isStr {
			return "", Invalid(pat+".text", "a text content part needs a string text")
		}
		texts = append(texts, s)
	}
	return strings.Join(texts, "\n"), nil
}

func parseToolCalls(at string, raw json.RawMessage) ([]ToolCall, int, *Error) {
	arr, ok := decodeArray(raw)
	if !ok {
		return nil, 0, Invalid(at, "tool_calls must be an array")
	}
	if len(arr) > maxToolCalls {
		return nil, 0, Invalid(at, "at most %d tool calls are allowed", maxToolCalls)
	}
	calls := make([]ToolCall, 0, len(arr))
	size := 0
	for j, c := range arr {
		cat := fmt.Sprintf("%s[%d]", at, j)
		cf, isObj := decodeObject(c)
		if !isObj {
			return nil, 0, Invalid(cat, "tool call must be an object")
		}
		iv, _ := cf.get("id")
		id, _ := decodeString(iv)
		if id == "" || len(id) > maxIDLen {
			return nil, 0, Invalid(cat+".id", "tool call id must be 1-%d bytes", maxIDLen)
		}
		tv, _ := cf.get("type")
		if t, _ := decodeString(tv); t != "function" {
			return nil, 0, Invalid(cat+".type", "tool call type must be function")
		}
		fv, _ := cf.get("function")
		ff, isObj := decodeObject(fv)
		if !isObj {
			return nil, 0, Invalid(cat+".function", "tool call function must be an object")
		}
		nv, _ := ff.get("name")
		name, _ := decodeString(nv)
		if !namePattern.MatchString(name) {
			return nil, 0, Invalid(cat+".function.name", "function name must match ^[A-Za-z0-9_-]{1,64}$")
		}
		av, _ := ff.get("arguments")
		args, isStr := decodeString(av)
		if !isStr {
			return nil, 0, Invalid(cat+".function.arguments", "function arguments must be a string")
		}
		size += len(args)
		calls = append(calls, ToolCall{ID: id, Type: "function", Function: FunctionCall{Name: name, Arguments: args}})
	}
	return calls, size, nil
}

// parseTools validates tools and returns them with their serialized size,
// which counts towards the chat input cap.
func parseTools(f fields) ([]Tool, int, *Error) {
	raw, present := f.get("tools")
	if !present {
		return nil, 0, nil
	}
	arr, ok := decodeArray(raw)
	if !ok {
		return nil, 0, Invalid("tools", "tools must be an array")
	}
	if len(arr) > maxTools {
		return nil, 0, Invalid("tools", "at most %d tools are allowed", maxTools)
	}
	if len(arr) == 0 {
		return nil, 0, nil
	}
	tools := make([]Tool, 0, len(arr))
	for i, t := range arr {
		tool, e := parseTool(fmt.Sprintf("tools[%d]", i), t)
		if e != nil {
			return nil, 0, e
		}
		tools = append(tools, tool)
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return nil, 0, Invalid("tools", "tools could not be encoded")
	}
	return tools, len(b), nil
}

func parseTool(at string, raw json.RawMessage) (Tool, *Error) {
	tf, ok := decodeObject(raw)
	if !ok {
		return Tool{}, Invalid(at, "tool must be an object")
	}
	tv, _ := tf.get("type")
	if t, _ := decodeString(tv); t != "function" {
		return Tool{}, Unsupported(at+".type", "only function tools are supported")
	}
	fv, _ := tf.get("function")
	ff, ok := decodeObject(fv)
	if !ok {
		return Tool{}, Invalid(at+".function", "tool function must be an object")
	}
	nv, _ := ff.get("name")
	name, _ := decodeString(nv)
	if !namePattern.MatchString(name) {
		return Tool{}, Invalid(at+".function.name", "function name must match ^[A-Za-z0-9_-]{1,64}$")
	}
	def := FunctionDef{Name: name}
	if dv, ok := ff.get("description"); ok {
		s, isStr := decodeString(dv)
		if !isStr || len(s) > maxToolDescBytes {
			return Tool{}, Invalid(at+".function.description", "description must be a string of at most %d bytes", maxToolDescBytes)
		}
		def.Description = s
	}
	if pv, ok := ff.get("parameters"); ok {
		var buf bytes.Buffer
		if _, isObj := decodeObject(pv); !isObj || json.Compact(&buf, pv) != nil {
			return Tool{}, Invalid(at+".function.parameters", "parameters must be a JSON object")
		}
		if buf.Len() > maxToolParamBytes || !DepthOK(buf.Bytes(), maxToolParamDepth) {
			return Tool{}, Invalid(at+".function.parameters", "parameters must be at most %d bytes and %d levels deep", maxToolParamBytes, maxToolParamDepth)
		}
		def.Parameters = buf.Bytes()
	}
	return Tool{Type: "function", Function: def}, nil
}
