package oai

import "encoding/json"

// ChatRequest is the upstream chat-completion body. The gateway always builds
// it from validated values and never forwards raw client bytes.
type ChatRequest struct {
	Model              string              `json:"model"`
	Messages           []Message           `json:"messages"`
	Stream             bool                `json:"stream"`
	StreamOptions      *StreamOptions      `json:"stream_options,omitempty"`
	MaxTokens          int                 `json:"max_tokens"`
	Temperature        *float64            `json:"temperature,omitempty"`
	TopP               *float64            `json:"top_p,omitempty"`
	TopK               *int                `json:"top_k,omitempty"`
	Stop               []string            `json:"stop,omitempty"`
	Seed               *int64              `json:"seed,omitempty"`
	FrequencyPenalty   *float64            `json:"frequency_penalty,omitempty"`
	PresencePenalty    *float64            `json:"presence_penalty,omitempty"`
	ResponseFormat     *ResponseFormat     `json:"response_format,omitempty"`
	Tools              []Tool              `json:"tools,omitempty"`
	ToolChoice         string              `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool               `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort    string              `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs *ChatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
}

// Message is one chat message. Content is nil only for an assistant message
// that carries tool calls; it is then sent as JSON null.
type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// TextMessage returns a message with plain text content.
func TextMessage(role, content string) Message {
	return Message{Role: role, Content: &content}
}

// ToolCall is an assistant tool call echoed back in the conversation history.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall is the function part of a ToolCall.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function tool definition.
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef describes a callable function. Parameters is the only field the
// gateway forwards as raw JSON, after size and depth checks (security §5.1).
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// StreamOptions is the OpenAI stream_options object.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ResponseFormat is restricted to {"type": "text" | "json_object"}.
type ResponseFormat struct {
	Type string `json:"type"`
}

// ChatTemplateKwargs is restricted to {"enable_thinking": bool} (security #4).
type ChatTemplateKwargs struct {
	EnableThinking bool `json:"enable_thinking"`
}

// Usage is the OpenAI token usage object.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChunkPeek is the minimal decode of one SSE data payload: just enough to spot
// tokens, usage and errors without holding the content.
type ChunkPeek struct {
	Choices []struct {
		Delta struct {
			Content          *string         `json:"content"`
			Reasoning        *string         `json:"reasoning"`
			ReasoningContent *string         `json:"reasoning_content"`
			ToolCalls        json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage  *Usage          `json:"usage"`
	Error  json.RawMessage `json:"error"`
	Object string          `json:"object"`
}

// HasToken reports whether the first choice carries generated text, reasoning
// or a tool call (a role-only first chunk does not count).
func (c *ChunkPeek) HasToken() bool {
	if len(c.Choices) == 0 {
		return false
	}
	d := c.Choices[0].Delta
	nonEmpty := func(s *string) bool { return s != nil && *s != "" }
	return nonEmpty(d.Content) || nonEmpty(d.Reasoning) || nonEmpty(d.ReasoningContent) ||
		(len(d.ToolCalls) > 0 && !isNull(d.ToolCalls))
}

// IsError reports whether the chunk is an in-band error object.
func (c *ChunkPeek) IsError() bool {
	return (len(c.Error) > 0 && !isNull(c.Error)) || c.Object == "error"
}

// UsageOnly reports whether this is the final usage chunk (no choices).
func (c *ChunkPeek) UsageOnly() bool {
	return len(c.Choices) == 0 && c.Usage != nil
}
