package oai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func testPolicy() ChatPolicy {
	return ChatPolicy{
		Resolve: func(alias string) (Model, bool) {
			if alias == "" || alias == "coder" {
				return Model{Alias: "coder", Upstream: "up/coder"}, true
			}
			return Model{}, false
		},
		DefaultMaxTokens: 512,
		Tokens:           TokenLimits{Cap: 1024, NonStreamCap: 768},
		MaxInputBytes:    24576,
	}
}

// upstreamKeys is every top-level key NormalizeChat may forward (security §5.1).
var upstreamKeys = map[string]bool{
	"model": true, "messages": true, "stream": true, "stream_options": true, "max_tokens": true,
	"temperature": true, "top_p": true, "top_k": true, "stop": true, "seed": true, "frequency_penalty": true,
	"presence_penalty": true, "response_format": true, "tools": true, "tool_choice": true,
	"parallel_tool_calls": true, "reasoning_effort": true, "chat_template_kwargs": true,
}

const hello = `"messages":[{"role":"user","content":"hi"}]`

// run normalizes body and returns the upstream JSON decoded into a map.
func run(t *testing.T, body string, p ChatPolicy) (map[string]any, Normalized, *Error) {
	t.Helper()
	n, e := NormalizeChat([]byte(body), p)
	if e != nil {
		return nil, n, e
	}
	b, err := json.Marshal(n.Request)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if !upstreamKeys[k] {
			t.Errorf("upstream key %q is not on the allowlist", k)
		}
	}
	return m, n, nil
}

func TestNormalizeChatTable(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int    // 0 = success
		code   string // expected error code
		param  string
		check  func(t *testing.T, m map[string]any, n Normalized)
	}{
		{name: "minimal", body: `{` + hello + `}`, check: func(t *testing.T, m map[string]any, n Normalized) {
			if m["model"] != "up/coder" || n.PublicModel != "coder" || m["max_tokens"] != 512.0 || m["stream"] != false {
				t.Errorf("got %v", m)
			}
			if _, ok := m["stream_options"]; ok {
				t.Error("stream_options sent without stream")
			}
		}},
		{name: "max_tokens null is default", body: `{"max_tokens":null,` + hello + `}`, check: maxTokens(512)},
		{name: "max_tokens zero", body: `{"max_tokens":0,` + hello + `}`, status: 400, code: CodeInvalidValue, param: "max_tokens"},
		{name: "max_tokens negative", body: `{"max_tokens":-1,` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "max_tokens fraction", body: `{"max_tokens":1.5,` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "max_tokens string", body: `{"max_tokens":"9",` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "max_tokens 1e9 clamps to cap", body: `{"stream":true,"max_tokens":1e9,` + hello + `}`, check: maxTokens(1024)},
		{name: "non-stream clamps to nonstream cap", body: `{"max_tokens":5000,` + hello + `}`, check: maxTokens(768)},
		{name: "max_tokens huge integer", body: `{"stream":true,"max_tokens":99999999999999999999999,` + hello + `}`, check: maxTokens(1024)},
		{name: "only max_completion_tokens", body: `{"max_completion_tokens":50,` + hello + `}`, check: maxTokens(50)},
		{name: "both, smaller wins", body: `{"max_tokens":100,"max_completion_tokens":50,` + hello + `}`, check: maxTokens(50)},
		{name: "case variant ignored", body: `{"max_tokens":10,"MAX_TOKENS":999999,"Max_Tokens":5,` + hello + `}`, check: maxTokens(10)},
		{name: "duplicate key last wins but clamped", body: `{"max_tokens":10,"max_tokens":999999,"stream":true,` + hello + `}`, check: maxTokens(1024)},
		{name: "n 2", body: `{"n":2,` + hello + `}`, status: 400, code: CodeUnsupported, param: "n"},
		{name: "n 1", body: `{"n":1,` + hello + `}`, check: func(t *testing.T, m map[string]any, _ Normalized) {
			if _, ok := m["n"]; ok {
				t.Error("n forwarded")
			}
		}},
		{name: "stream_options without stream dropped", body: `{"stream_options":{"include_usage":true},` + hello + `}`,
			check: func(t *testing.T, m map[string]any, n Normalized) {
				if _, ok := m["stream_options"]; ok || n.ClientWantsUsage {
					t.Error("stream_options kept without stream")
				}
			}},
		{name: "stream forces include_usage", body: `{"stream":true,"stream_options":{"continuous_usage_stats":true},` + hello + `}`,
			check: func(t *testing.T, m map[string]any, n Normalized) {
				so := m["stream_options"].(map[string]any)
				if len(so) != 1 || so["include_usage"] != true || n.ClientWantsUsage {
					t.Errorf("stream_options = %v, wants=%v", so, n.ClientWantsUsage)
				}
			}},
		{name: "client asks for usage", body: `{"stream":true,"stream_options":{"include_usage":true},` + hello + `}`,
			check: func(t *testing.T, _ map[string]any, n Normalized) {
				if !n.ClientWantsUsage {
					t.Error("ClientWantsUsage false")
				}
			}},
		{name: "enable_thinking forwarded", body: `{"chat_template_kwargs":{"enable_thinking":false},` + hello + `}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				kw := m["chat_template_kwargs"].(map[string]any)
				if len(kw) != 1 || kw["enable_thinking"] != false {
					t.Errorf("kwargs = %v", kw)
				}
			}},
		{name: "chat_template smuggle", body: `{"chat_template_kwargs":{"chat_template":"{% for x in y %}"},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "tokenize", body: `{"chat_template_kwargs":{"tokenize":true},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "enable_thinking string", body: `{"chat_template_kwargs":{"enable_thinking":"yes"},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "enable_thinking plus extra", body: `{"chat_template_kwargs":{"enable_thinking":false,"x":1},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "response_format text", body: `{"response_format":{"type":"text","schema":{}},` + hello + `}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				if rf := m["response_format"].(map[string]any); len(rf) != 1 || rf["type"] != "text" {
					t.Errorf("response_format = %v", rf)
				}
			}},
		{name: "response_format json_object", body: `{"response_format":{"type":"json_object"},` + hello + `}`},
		{name: "response_format json_schema", body: `{"response_format":{"type":"json_schema","json_schema":{}},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "response_format structural_tag", body: `{"response_format":{"type":"structural_tag"},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "response_format grammar", body: `{"response_format":{"type":"grammar"},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "tool_choice required", body: `{"tool_choice":"required",` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "tool_choice object", body: `{"tool_choice":{"type":"function","function":{"name":"f"}},` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "tool_choice auto", body: `{"tool_choice":"auto",` + hello + `}`, check: field("tool_choice", "auto")},
		{name: "tool_choice none", body: `{"tool_choice":"none",` + hello + `}`, check: field("tool_choice", "none")},
		{name: "17 tools", body: `{"tools":[` + repeat(toolJSON("f", `{}`), 17) + `],` + hello + `}`, status: 400, code: CodeInvalidValue, param: "tools"},
		{name: "tool name with space", body: `{"tools":[` + toolJSON("a b", `{}`) + `],` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "tool params depth 9", body: `{"tools":[` + toolJSON("f", nested(9)) + `],` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "tool params depth 8", body: `{"tools":[` + toolJSON("f", nested(8)) + `],` + hello + `}`},
		{name: "tool params > 8KiB", body: `{"tools":[` + toolJSON("f", `{"d":"`+strings.Repeat("x", 8200)+`"}`) + `],` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "tool not function", body: `{"tools":[{"type":"code_interpreter"}],` + hello + `}`, status: 400, code: CodeUnsupported},
		{name: "parallel_tool_calls without tools dropped", body: `{"parallel_tool_calls":true,` + hello + `}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				if _, ok := m["parallel_tool_calls"]; ok {
					t.Error("parallel_tool_calls forwarded without tools")
				}
			}},
		{name: "tools re-encoded", body: `{"tools":[{"type":"function","strict":true,"function":{"name":"f","description":"d","parameters":{"type":"object"},"x":1}}],"parallel_tool_calls":false,` + hello + `}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				fn := m["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
				if len(fn) != 3 || m["parallel_tool_calls"] != false {
					t.Errorf("tools = %v", m["tools"])
				}
			}},
		{name: "logprobs dropped", body: `{"logprobs":true,"top_logprobs":20,` + hello + `}`},
		{name: "stop 5 items", body: `{"stop":["a","b","c","d","e"],` + hello + `}`, status: 400, code: CodeInvalidValue, param: "stop"},
		{name: "stop 65 bytes", body: `{"stop":"` + strings.Repeat("s", 65) + `",` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "stop empty", body: `{"stop":"",` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "stop string becomes array", body: `{"stop":"END",` + hello + `}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				if s := m["stop"].([]any); len(s) != 1 || s[0] != "END" {
					t.Errorf("stop = %v", s)
				}
			}},
		{name: "temperature 2.5", body: `{"temperature":2.5,` + hello + `}`, status: 400, code: CodeInvalidValue, param: "temperature"},
		{name: "top_p 0", body: `{"top_p":0,` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "top_k 101", body: `{"top_k":101,` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "top_k -1", body: `{"top_k":-1,` + hello + `}`, check: field("top_k", -1.0)},
		{name: "presence_penalty 3", body: `{"presence_penalty":3,` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "seed", body: `{"seed":42,` + hello + `}`, check: field("seed", 42.0)},
		{name: "reasoning_effort bad", body: `{"reasoning_effort":"max",` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "reasoning_effort low", body: `{"reasoning_effort":"low",` + hello + `}`, check: field("reasoning_effort", "low")},
		{name: "image_url part (SSRF)", body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://169.254.169.254/latest/meta-data/"}}]}]}`,
			status: 400, code: CodeUnsupported, param: "messages[0].content[0].type"},
		{name: "input_audio part", body: partBody("input_audio"), status: 400, code: CodeUnsupported},
		{name: "video_url part", body: partBody("video_url"), status: 400, code: CodeUnsupported},
		{name: "file part", body: partBody("file"), status: 400, code: CodeUnsupported},
		{name: "image_embeds part", body: partBody("image_embeds"), status: 400, code: CodeUnsupported},
		{name: "text parts flattened", body: `{"messages":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				if c := m["messages"].([]any)[0].(map[string]any)["content"]; c != "a\nb" {
					t.Errorf("content = %v", c)
				}
			}},
		{name: "unknown role", body: `{"messages":[{"role":"root","content":"x"}]}`, status: 400, code: CodeInvalidValue, param: "messages[0].role"},
		{name: "unknown message fields dropped", body: `{"messages":[{"role":"assistant","content":"x","reasoning_content":"r","audio":{},"refusal":"no"}]}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				if msg := m["messages"].([]any)[0].(map[string]any); len(msg) != 2 {
					t.Errorf("message = %v", msg)
				}
			}},
		{name: "assistant tool_calls with null content", body: `{"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"1","content":"42"}]}`,
			check: func(t *testing.T, m map[string]any, _ Normalized) {
				msg := m["messages"].([]any)[0].(map[string]any)
				if c, ok := msg["content"]; !ok || c != nil {
					t.Errorf("content = %v (present %v)", c, ok)
				}
			}},
		{name: "tool_calls on user", body: `{"messages":[{"role":"user","content":"x","tool_calls":[]}]}`, status: 400, code: CodeInvalidValue},
		{name: "missing content", body: `{"messages":[{"role":"user"}]}`, status: 400, code: CodeMissingField},
		{name: "bad name", body: `{"messages":[{"role":"user","name":"a b","content":"x"}]}`, status: 400, code: CodeInvalidValue},
		{name: "128 messages", body: `{"messages":[` + repeat(`{"role":"user","content":"x"}`, 128) + `]}`},
		{name: "129 messages", body: `{"messages":[` + repeat(`{"role":"user","content":"x"}`, 129) + `]}`, status: 400, code: CodeInvalidValue},
		{name: "no messages", body: `{}`, status: 400, code: CodeMissingField},
		{name: "empty messages", body: `{"messages":[]}`, status: 400, code: CodeInvalidValue},
		{name: "input at cap", body: `{"messages":[{"role":"user","content":"` + strings.Repeat("x", 24576) + `"}]}`},
		{name: "input over cap", body: `{"messages":[{"role":"user","content":"` + strings.Repeat("x", 24577) + `"}]}`, status: 413, code: CodeTooLarge},
		{name: "tools count towards cap", body: `{"tools":[` + toolJSON("f", `{"d":"`+strings.Repeat("x", 8000)+`"}`) + `],"messages":[{"role":"user","content":"` + strings.Repeat("x", 24576-100) + `"}]}`, status: 413, code: CodeTooLarge},
		{name: "depth 33", body: `{"messages":[{"role":"user","content":"x"}],"x":` + nested(32) + `}`, status: 400, code: CodeInvalidJSON},
		{name: "depth 32 ok", body: `{"messages":[{"role":"user","content":"x"}],"x":` + nested(31) + `}`},
		{name: "invalid utf8", body: "{\"messages\":[{\"role\":\"user\",\"content\":\"\xff\"}]}", status: 400, code: CodeInvalidJSON},
		{name: "not an object", body: `[1]`, status: 400, code: CodeInvalidJSON},
		{name: "model traversal", body: `{"model":"coder/../x",` + hello + `}`, status: 404, code: CodeModelNotFound},
		{name: "model query", body: `{"model":"coder?x=1",` + hello + `}`, status: 404, code: CodeModelNotFound},
		{name: "model encoded", body: `{"model":"%2e%2e",` + hello + `}`, status: 404, code: CodeModelNotFound},
		{name: "model 65 chars", body: `{"model":"` + strings.Repeat("c", 65) + `",` + hello + `}`, status: 404, code: CodeModelNotFound},
		{name: "model number", body: `{"model":7,` + hello + `}`, status: 400, code: CodeInvalidValue},
		{name: "model alias", body: `{"model":"coder",` + hello + `}`, check: field("model", "up/coder")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, n, e := run(t, tc.body, testPolicy())
			if tc.status != 0 {
				if e == nil {
					t.Fatalf("want %d %s, got success: %v", tc.status, tc.code, m)
				}
				if e.Status != tc.status || e.Code != tc.code || (tc.param != "" && e.Param != tc.param) {
					t.Fatalf("got %d %s param=%q (%s), want %d %s param=%q", e.Status, e.Code, e.Param, e.Message, tc.status, tc.code, tc.param)
				}
				return
			}
			if e != nil {
				t.Fatalf("unexpected error %d %s: %s", e.Status, e.Code, e.Message)
			}
			if tc.check != nil {
				tc.check(t, m, n)
			}
		})
	}
}

// TestForbiddenFieldsNeverForwarded is the security §5.6 fixture.
func TestForbiddenFieldsNeverForwarded(t *testing.T) {
	forbidden := []string{
		"keep_alive", "options", "format", "think", "raw", "template", "system", "n_probs", "cache_prompt", "id_slot",
		"lora", "grammar", "json_schema", "best_of", "use_beam_search", "length_penalty", "min_tokens", "ignore_eos",
		"min_p", "repetition_penalty", "stop_token_ids", "bad_words", "allowed_token_ids", "logit_bias",
		"logits_processors", "prompt_logprobs", "echo", "add_generation_prompt", "continue_final_message",
		"add_special_tokens", "documents", "chat_template", "mm_processor_kwargs", "truncate_prompt_tokens",
		"guided_json", "guided_regex", "guided_choice", "guided_grammar", "guided_decoding_backend",
		"structured_outputs", "priority", "request_id", "cache_salt", "kv_transfer_params", "vllm_xargs",
		"return_tokens_as_token_ids", "return_token_ids", "user", "metadata", "store", "service_tier",
		"prediction", "modalities", "audio", "web_search_options", "functions", "function_call",
		"max_completion_tokens", "logprobs", "top_logprobs", "_debug_render_only",
	}
	var b strings.Builder
	b.WriteString(`{"stream":true,` + hello)
	for _, f := range forbidden {
		fmt.Fprintf(&b, `,%q:1`, f)
	}
	b.WriteString(`}`)
	m, _, e := run(t, b.String(), testPolicy())
	if e != nil {
		t.Fatalf("unexpected error: %s", e.Message)
	}
	for _, f := range forbidden {
		if _, ok := m[f]; ok {
			t.Errorf("%s reached upstream", f)
		}
	}
}

func TestModelDefaultsMergeUnlessClientSet(t *testing.T) {
	temp, on := 0.7, false
	p := testPolicy()
	p.Resolve = func(string) (Model, bool) {
		return Model{Alias: "coder", Upstream: "u", Defaults: ModelDefaults{
			ReasoningEffort: "low", Temperature: &temp, EnableThinking: &on}}, true
	}
	m, _, e := run(t, `{`+hello+`}`, p)
	if e != nil {
		t.Fatal(e.Message)
	}
	if m["reasoning_effort"] != "low" || m["temperature"] != 0.7 || m["chat_template_kwargs"].(map[string]any)["enable_thinking"] != false {
		t.Errorf("defaults not applied: %v", m)
	}
	m, _, _ = run(t, `{"reasoning_effort":"high","temperature":0.1,"chat_template_kwargs":{"enable_thinking":true},`+hello+`}`, p)
	if m["reasoning_effort"] != "high" || m["temperature"] != 0.1 || m["chat_template_kwargs"].(map[string]any)["enable_thinking"] != true {
		t.Errorf("client values overridden: %v", m)
	}
}

func TestDeepArrayIsFastAndSafe(t *testing.T) {
	body := []byte(strings.Repeat("[", 1<<20))
	start := time.Now()
	_, e := NormalizeChat(body, testPolicy())
	if e == nil || e.Code != CodeInvalidJSON {
		t.Fatalf("want invalid_json, got %v", e)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("took %s", d)
	}
}

func FuzzNormalizeChat(f *testing.F) {
	for _, s := range []string{
		`{"model":"coder","messages":[{"role":"user","content":"Write a Go function"}],"max_tokens":256}`,
		`{"model":"coder","stream":true,"messages":[{"role":"system","content":"x"},{"role":"user","content":"y"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"text","text":"a"}]}],"tools":[{"type":"function","function":{"name":"f","parameters":{}}}]}`,
		`{"max_tokens":10,"MAX_TOKENS":999999,"messages":[{"role":"user","content":"x"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://x"}}]}]}`,
		`{"chat_template_kwargs":{"tokenize":true},"messages":[{"role":"user","content":"x"}]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		n, e := NormalizeChat(raw, testPolicy())
		if e != nil {
			return
		}
		b, err := json.Marshal(n.Request)
		if err != nil || !json.Valid(b) || !DepthOK(b, MaxDepth) {
			t.Fatalf("bad upstream body %s: %v", b, err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for k := range m {
			if !upstreamKeys[k] {
				t.Fatalf("key %q forwarded", k)
			}
		}
		if n.Request.MaxTokens < 1 || n.Request.MaxTokens > 1024 {
			t.Fatalf("max_tokens %d", n.Request.MaxTokens)
		}
		for _, msg := range m["messages"].([]any) {
			switch c := msg.(map[string]any)["content"]; c.(type) {
			case string, nil:
			default:
				t.Fatalf("content %T", c)
			}
		}
	})
}

func maxTokens(want int) func(*testing.T, map[string]any, Normalized) {
	return field("max_tokens", float64(want))
}

func field(key string, want any) func(*testing.T, map[string]any, Normalized) {
	return func(t *testing.T, m map[string]any, _ Normalized) {
		t.Helper()
		if m[key] != want {
			t.Errorf("%s = %v, want %v", key, m[key], want)
		}
	}
}

func toolJSON(name, params string) string {
	return fmt.Sprintf(`{"type":"function","function":{"name":%q,"parameters":%s}}`, name, params)
}

func repeat(s string, n int) string { return strings.TrimSuffix(strings.Repeat(s+",", n), ",") }

// nested returns an object nested depth levels deep.
func nested(depth int) string {
	return strings.Repeat(`{"a":`, depth-1) + `{}` + strings.Repeat(`}`, depth-1)
}

func partBody(typ string) string {
	return fmt.Sprintf(`{"messages":[{"role":"user","content":[{"type":%q}]}]}`, typ)
}
