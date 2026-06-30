package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ProviderRequest is the input for a model completion call.
type ProviderRequest struct {
	Model          string
	Messages       []Message
	Tools          []ToolDefinition
	Temperature    float64
	MaxTokens      int64
	ResponseFormat *ResponseFormat
	Thinking       *ThinkingConfig

	// FastMode requests priority/low-latency processing where supported
	// (e.g. OpenAI Priority Processing, Anthropic Fast Mode).
	FastMode bool
}

// CallConfig is a reusable subset of per-call agent/provider options that can
// be applied as defaults, persisted per-thread, or overridden per request.
type CallConfig struct {
	Model          string          `json:"model,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Thinking       *ThinkingConfig `json:"thinking,omitempty"`
	Skills         []string        `json:"skills,omitempty"`
}

type ThinkingDisplay string

const (
	ThinkingDisplayDefault    ThinkingDisplay = ""
	ThinkingDisplaySummarized ThinkingDisplay = "summarized"
	ThinkingDisplayOmitted    ThinkingDisplay = "omitted"
)

type ThinkingEffort string

const (
	ThinkingEffortDefault ThinkingEffort = ""
	ThinkingEffortLow     ThinkingEffort = "low"
	ThinkingEffortMedium  ThinkingEffort = "medium"
	ThinkingEffortHigh    ThinkingEffort = "high"
	ThinkingEffortMax     ThinkingEffort = "max"
)

// ThinkingConfig requests provider-native reasoning / thought summaries when
// supported. Providers that do not support explicit thinking controls ignore it.
type ThinkingConfig struct {
	// IncludeThoughts asks the provider to return reasoning summaries as
	// `thinking` blocks in the response when available.
	IncludeThoughts bool `json:"include_thoughts,omitempty"`
	// Display controls whether providers should return summarized reasoning
	// blocks or omit their visible text while still keeping internal signatures.
	// If unset, providers infer a display from IncludeThoughts.
	Display ThinkingDisplay `json:"display,omitempty"`
	// Effort hints how much reasoning depth the provider should use when it
	// exposes such a control. Support is provider-specific.
	Effort ThinkingEffort `json:"effort,omitempty"`
	// Budget optionally caps internal reasoning tokens for providers that expose
	// that control. Zero means provider default. Negative values may map to a
	// provider-specific "dynamic" mode where supported.
	Budget int64 `json:"budget,omitempty"`
}

// VisibleThoughtsEnabled reports whether visible reasoning summaries should be
// requested from providers.
func (c *ThinkingConfig) VisibleThoughtsEnabled() bool {
	if c == nil {
		return false
	}
	switch c.Display {
	case ThinkingDisplaySummarized:
		return true
	case ThinkingDisplayOmitted:
		return false
	default:
		return c.IncludeThoughts
	}
}

// NormalizedDisplay returns the provider-facing display mode.
func (c *ThinkingConfig) NormalizedDisplay() ThinkingDisplay {
	if c == nil {
		return ThinkingDisplayDefault
	}
	if c.Display != ThinkingDisplayDefault {
		return c.Display
	}
	if c.IncludeThoughts {
		return ThinkingDisplaySummarized
	}
	return ThinkingDisplayOmitted
}

func CloneThinkingConfig(c *ThinkingConfig) *ThinkingConfig {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

func CloneResponseFormat(f *ResponseFormat) *ResponseFormat {
	if f == nil {
		return nil
	}
	cp := *f
	if f.JSONSchema != nil {
		schema := *f.JSONSchema
		cp.JSONSchema = &schema
	}
	return &cp
}

func CloneCallConfig(c *CallConfig) *CallConfig {
	if c == nil {
		return nil
	}
	return &CallConfig{
		Model:          c.Model,
		ResponseFormat: CloneResponseFormat(c.ResponseFormat),
		Thinking:       CloneThinkingConfig(c.Thinking),
		Skills:         CloneStringSlice(c.Skills),
	}
}

func CloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeCallConfig overlays non-zero fields from override onto base.
func MergeCallConfig(base, override *CallConfig) *CallConfig {
	if base == nil && override == nil {
		return nil
	}
	out := CloneCallConfig(base)
	if out == nil {
		out = &CallConfig{}
	}
	if override == nil {
		return out
	}
	if override.Model != "" {
		out.Model = override.Model
	}
	if override.ResponseFormat != nil {
		out.ResponseFormat = CloneResponseFormat(override.ResponseFormat)
	}
	if override.Thinking != nil {
		out.Thinking = CloneThinkingConfig(override.Thinking)
	}
	if len(override.Skills) > 0 {
		out.Skills = CloneStringSlice(override.Skills)
	}
	return out
}

type ResponseFormatType string

const (
	ResponseFormatText       ResponseFormatType = "text"
	ResponseFormatJSONObject ResponseFormatType = "json_object"
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat requests constrained model output from providers that support it.
type ResponseFormat struct {
	Type       ResponseFormatType              `json:"type"`
	JSONSchema *ResponseFormatJSONSchemaConfig `json:"json_schema,omitempty"`
}

// ResponseFormatJSONSchemaConfig configures a named JSON Schema response format.
type ResponseFormatJSONSchemaConfig struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema"`
	Strict      bool           `json:"strict,omitempty"`
}

// NewJSONObjectResponseFormat requests a valid JSON object response.
func NewJSONObjectResponseFormat() *ResponseFormat {
	return &ResponseFormat{Type: ResponseFormatJSONObject}
}

// NewJSONSchemaResponseFormat requests output that conforms to the given schema.
func NewJSONSchemaResponseFormat(name string, schema map[string]any) *ResponseFormat {
	return &ResponseFormat{
		Type: ResponseFormatJSONSchema,
		JSONSchema: &ResponseFormatJSONSchemaConfig{
			Name:   name,
			Schema: schema,
			Strict: true,
		},
	}
}

// PromptInstruction renders a provider-agnostic fallback instruction for
// providers that do not expose a first-class structured output API.
func (f *ResponseFormat) PromptInstruction() string {
	if f == nil {
		return ""
	}
	switch f.Type {
	case ResponseFormatJSONObject:
		return "Return only a valid JSON object. Do not wrap it in markdown fences or add extra commentary."
	case ResponseFormatJSONSchema:
		if f.JSONSchema == nil {
			return "Return only valid JSON that matches the requested schema."
		}
		schemaJSON, _ := json.MarshalIndent(f.JSONSchema.Schema, "", "  ")
		var b strings.Builder
		b.WriteString("Return only valid JSON that matches this schema exactly. ")
		b.WriteString("Do not wrap it in markdown fences or add extra commentary.")
		if f.JSONSchema.Name != "" {
			b.WriteString("\n\nSchema name: ")
			b.WriteString(f.JSONSchema.Name)
		}
		if f.JSONSchema.Description != "" {
			b.WriteString("\nDescription: ")
			b.WriteString(f.JSONSchema.Description)
		}
		if len(schemaJSON) > 0 {
			b.WriteString("\n\nJSON Schema:\n")
			b.Write(schemaJSON)
		}
		return b.String()
	default:
		return ""
	}
}

// ExtractStructuredContent returns the raw JSON payload when a structured output
// request was made and the provider returned valid JSON content.
func ExtractStructuredContent(content string, format *ResponseFormat) json.RawMessage {
	if format == nil {
		return nil
	}
	switch format.Type {
	case ResponseFormatJSONObject, ResponseFormatJSONSchema:
		if json.Valid([]byte(content)) {
			return json.RawMessage(content)
		}
	}
	return nil
}

func (f *ResponseFormat) String() string {
	if f == nil {
		return "<nil>"
	}
	if f.JSONSchema != nil && f.JSONSchema.Name != "" {
		return fmt.Sprintf("%s(%s)", f.Type, f.JSONSchema.Name)
	}
	return string(f.Type)
}

// ToolDefinition describes a tool's schema for the model.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ProviderResponse is the output of a non-streaming completion call.
type ProviderResponse struct {
	Content    string
	Blocks     []ContentBlock
	Structured json.RawMessage
	ToolCalls  []ToolCall
	Usage      TokenUsage
	// FinishReason reports why the model stopped generating. Common values:
	// "stop" (natural end), "length" (max_tokens truncated the output),
	// "tool_calls" (stopped to call tools), "content_filter".
	// When "length" and ToolCalls is non-empty, the tool-call arguments may be
	// truncated mid-JSON; the executor guards against this by validating JSON
	// before dispatch, but callers may also inspect FinishReason directly.
	FinishReason string
	// SuppressPersist skips storing this assistant response in conversation state.
	// Extensions may use this for internal control turns that should trigger
	// another model call without surfacing an intermediate assistant message.
	SuppressPersist bool
}

// TokenUsage tracks token consumption for a single request.
type TokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// StreamDelta is one incremental chunk from a streaming completion.
type StreamDelta struct {
	Content   string
	Blocks    []ContentBlock
	ToolCalls []ToolCallDelta
	Done      bool
	Usage     *TokenUsage // populated on the final chunk by some providers
	// FinishReason is populated on the final chunk by providers that expose it.
	// Common values: "stop", "length", "tool_calls", "content_filter".
	FinishReason string
}

// ToolCallDelta is an incremental fragment of a tool call during streaming.
type ToolCallDelta struct {
	Index     int64
	ID        string
	Name      string
	Arguments string
}

// Provider is the abstraction layer over different LLM backends.
type Provider interface {
	Complete(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error)
	Stream(ctx context.Context, req *ProviderRequest) (<-chan StreamDelta, error)
}
