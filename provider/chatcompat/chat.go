package chatcompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

const defaultBaseURL = "https://api.openai.com/v1"

type Config struct {
	APIKey  string
	BaseURL string
	OrgID   string
	Client  *http.Client // Optional: custom HTTP client, defaults to http.Client with 5m timeout

	// Protocol selects which API protocol to use.
	//   "" (default)          — Chat Completions API (/v1/chat/completions)
	//   "responses"           — Responses API (/v1/responses)
	Protocol APIProtocol

	// EndpointPath overrides the chat completions endpoint path.
	// Defaults to "/chat/completions". Useful for providers that use a
	// different path while remaining Chat Completions-compatible.
	// Ignored when Protocol is "responses".
	EndpointPath string

	// ExtraHeaders are additional HTTP headers merged into every request.
	// Keys override defaults when overlapping (except Content-Type).
	ExtraHeaders map[string]string

	// PrepareMessages optionally transforms messages before they are
	// converted to the wire format. Return the input unchanged if no
	// transformation is needed.
	PrepareMessages func([]agentcore.Message) []agentcore.Message

	// BuildExtraBody optionally returns additional top-level JSON fields
	// to merge into the request body. Return nil if no extra fields are
	// needed.
	BuildExtraBody func(*agentcore.ProviderRequest) map[string]any

	// DisableSystemPrompt strips system messages from the request before
	// sending. Needed for providers (e.g. DeepSeek reasoner) that do not
	// support a system role. Applied after PrepareMessages when both are set.
	DisableSystemPrompt bool
}

// Provider implements agentcore.Provider for the Chat Completions
// protocol, which is also used by DeepSeek, Qwen, Moonshot, Groq, Together,
// Mistral, and dozens of other vendors. See doc.go for the full list.
type Provider struct {
	config Config
	client *http.Client
}

func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.EndpointPath == "" {
		if cfg.Protocol == APIProtocolResponses {
			cfg.EndpointPath = "/responses"
		} else {
			cfg.EndpointPath = "/chat/completions"
		}
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Provider{
		config: cfg,
		client: client,
	}
}

// --- Chat Completions wire types ---

type chatRequest struct {
	Model            string              `json:"model"`
	Messages         []chatMessage       `json:"messages"`
	Tools            []chatTool          `json:"tools,omitempty"`
	Stream           bool                `json:"stream,omitempty"`
	Temperature      *float64            `json:"temperature,omitempty"`
	FrequencyPenalty *float64            `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64            `json:"presence_penalty,omitempty"`
	MaxTokens        *int64              `json:"max_tokens,omitempty"`
	ResponseFormat   *chatResponseFormat `json:"response_format,omitempty"`
	StreamOptions    *streamOptions      `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatResponseFormat struct {
	Type       string                  `json:"type"`
	JSONSchema *chatResponseJSONSchema `json:"json_schema,omitempty"`
}

type chatResponseJSONSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema"`
	Strict      bool           `json:"strict,omitempty"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          any            `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	Name             string         `json:"name,omitempty"`
}

type chatResponseMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	Name             string         `json:"name,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type chatChunk struct {
	ID      string        `json:"id"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chatUsage    `json:"usage,omitempty"`
}

type chunkChoice struct {
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []chunkToolCall `json:"tool_calls,omitempty"`
}

const cumulativeSnapshotMinPrefix = 32

type streamTextNormalizer struct {
	previous   string
	cumulative bool
}

func (n *streamTextNormalizer) delta(text string) string {
	if text == "" {
		return ""
	}

	previous := n.previous
	n.previous = text
	if previous == "" {
		return text
	}
	if strings.HasPrefix(text, previous) &&
		(n.cumulative || len(previous) >= cumulativeSnapshotMinPrefix) {
		n.cumulative = true
		return strings.TrimPrefix(text, previous)
	}
	if n.cumulative && strings.HasPrefix(previous, text) {
		return ""
	}

	n.cumulative = false
	return text
}

type chunkToolCall struct {
	Index    int64             `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function chunkFunctionCall `json:"function,omitempty"`
}

type chunkFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// --- type conversion helpers ---

func ToMessages(msgs []agentcore.Message) []chatMessage {
	out := make([]chatMessage, len(msgs))
	for i, m := range msgs {
		cm := chatMessage{
			Role:       string(m.Role),
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		cm.Content = MessageContent(m)
		if m.Role == agentcore.RoleAssistant {
			var reasoningParts []string
			for _, bl := range m.Blocks {
				if bl.Kind == agentcore.BlockKindThinking && bl.Text != "" {
					reasoningParts = append(reasoningParts, bl.Text)
				}
			}
			if len(reasoningParts) > 0 {
				cm.ReasoningContent = strings.Join(reasoningParts, "\n")
			}
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, chatToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: chatFunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		out[i] = cm
	}
	return out
}

func MessageContent(m agentcore.Message) any {
	if len(m.Blocks) == 0 {
		return m.Content
	}
	parts := make([]chatContentPart, 0, len(m.Blocks)+1)
	if m.Content != "" {
		parts = append(parts, chatContentPart{Type: "text", Text: m.Content})
	}
	for _, bl := range m.Blocks {
		switch bl.Kind {
		case agentcore.BlockKindText:
			if bl.Text != "" {
				parts = append(parts, chatContentPart{Type: "text", Text: bl.Text})
			}
		case agentcore.BlockKindImage:
			if bl.URL == "" {
				continue
			}
			parts = append(parts, chatContentPart{
				Type: "image_url",
				ImageURL: &chatImageURL{
					URL:    bl.URL,
					Detail: bl.Detail,
				},
			})
		}
	}
	if len(parts) == 0 {
		return m.Content
	}
	return parts
}

func ToTools(defs []agentcore.ToolDefinition) []chatTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]chatTool, len(defs))
	for i, d := range defs {
		out[i] = chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		}
	}
	return out
}

// --- Provider interface implementation ---

func (p *Provider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	if p.config.Protocol == APIProtocolResponses {
		return p.completeResponses(ctx, req)
	}
	cr, extra := p.buildRequest(req, false)

	httpResp, err := p.doHTTP(ctx, cr, extra)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("api error (status %d): %s", httpResp.StatusCode, formatError(body))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in response")
	}

	choice := chatResp.Choices[0]
	resp := &agentcore.ProviderResponse{
		Content: choice.Message.Content,
		Blocks:  TextBlocks(choice.Message.Content),
		Usage: agentcore.TokenUsage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		},
		FinishReason: choice.FinishReason,
	}
	if rc := choice.Message.ReasoningContent; rc != "" {
		resp.Blocks = agentcore.MergeContentBlocks([]agentcore.ContentBlock{{
			Kind: agentcore.BlockKindThinking,
			Text: rc,
		}}, resp.Blocks...)
	}
	resp.Structured = agentcore.ExtractStructuredContent(choice.Message.Content, req.ResponseFormat)
	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, agentcore.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
		resp.Blocks = agentcore.MergeContentBlocks(resp.Blocks, agentcore.ContentBlock{
			Kind:       agentcore.BlockKindToolCall,
			ToolCallID: tc.ID,
			Name:       tc.Function.Name,
			Arguments:  tc.Function.Arguments,
		})
	}
	return resp, nil
}

func (p *Provider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	if p.config.Protocol == APIProtocolResponses {
		return p.streamResponses(ctx, req)
	}
	cr, extra := p.buildRequest(req, true)

	httpResp, err := p.doHTTP(ctx, cr, extra)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		httpResp.Body.Close()
		return nil, fmt.Errorf("api error (status %d): %s", httpResp.StatusCode, formatError(body))
	}

	ch := make(chan agentcore.StreamDelta, 64)

	go func() {
		defer httpResp.Body.Close()
		defer close(ch)

		var contentNormalizer streamTextNormalizer
		var reasoningNormalizer streamTextNormalizer
		scanner := bufio.NewScanner(httpResp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}

			var chunk chatChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			// Final chunk may have usage but no choices
			if chunk.Usage != nil {
				sd := agentcore.StreamDelta{
					Usage: &agentcore.TokenUsage{
						PromptTokens:     chunk.Usage.PromptTokens,
						CompletionTokens: chunk.Usage.CompletionTokens,
						TotalTokens:      chunk.Usage.TotalTokens,
					},
				}
				select {
				case ch <- sd:
				case <-ctx.Done():
					return
				}
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta
			contentDelta := contentNormalizer.delta(delta.Content)
			reasoningDelta := reasoningNormalizer.delta(delta.ReasoningContent)
			if reasoningDelta == contentDelta {
				reasoningDelta = ""
			}
			sd := agentcore.StreamDelta{Content: contentDelta}
			if fr := chunk.Choices[0].FinishReason; fr != nil && *fr != "" {
				sd.FinishReason = *fr
			}
			if contentDelta != "" {
				sd.Blocks = TextBlocks(contentDelta)
			}
			if reasoningDelta != "" {
				sd.Blocks = agentcore.MergeContentBlocks(sd.Blocks, agentcore.ContentBlock{
					Kind: agentcore.BlockKindThinking,
					Text: reasoningDelta,
				})
			}

			for _, tc := range delta.ToolCalls {
				sd.ToolCalls = append(sd.ToolCalls, agentcore.ToolCallDelta{
					Index:     tc.Index,
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
				sd.Blocks = agentcore.MergeContentBlocks(sd.Blocks, agentcore.ContentBlock{
					Kind:       agentcore.BlockKindToolCall,
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Arguments:  tc.Function.Arguments,
				})
			}

			select {
			case ch <- sd:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// --- internal helpers ---

func (p *Provider) buildRequest(req *agentcore.ProviderRequest, stream bool) (chatRequest, map[string]any) {
	msgs := req.Messages
	if p.config.PrepareMessages != nil {
		msgs = p.config.PrepareMessages(msgs)
	}
	if p.config.DisableSystemPrompt {
		filtered := make([]agentcore.Message, 0, len(msgs))
		for _, m := range msgs {
			if m.Role != agentcore.RoleSystem {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}

	cr := chatRequest{
		Model:          req.Model,
		Messages:       ToMessages(msgs),
		Tools:          ToTools(req.Tools),
		Stream:         stream,
		ResponseFormat: ToResponseFormat(req.ResponseFormat),
	}
	if req.Temperature > 0 {
		t := req.Temperature
		cr.Temperature = &t
	}
	// Penalties are opt-in (0 = unset/provider default) since not all models
	// accept them (some reasoning models reject non-zero penalties), unlike
	// Temperature they support negative values too, so we gate on != 0.
	if req.FrequencyPenalty != 0 {
		fp := req.FrequencyPenalty
		cr.FrequencyPenalty = &fp
	}
	if req.PresencePenalty != 0 {
		pp := req.PresencePenalty
		cr.PresencePenalty = &pp
	}
	if req.MaxTokens > 0 {
		m := req.MaxTokens
		cr.MaxTokens = &m
	}
	if stream {
		cr.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	var extra map[string]any
	if p.config.BuildExtraBody != nil {
		extra = p.config.BuildExtraBody(req)
	}
	return cr, extra
}

func ToResponseFormat(format *agentcore.ResponseFormat) *chatResponseFormat {
	if format == nil {
		return nil
	}
	out := &chatResponseFormat{Type: string(format.Type)}
	if format.JSONSchema != nil {
		out.JSONSchema = &chatResponseJSONSchema{
			Name:        format.JSONSchema.Name,
			Description: format.JSONSchema.Description,
			Schema:      format.JSONSchema.Schema,
			Strict:      format.JSONSchema.Strict,
		}
	}
	return out
}

func TextBlocks(text string) []agentcore.ContentBlock {
	if text == "" {
		return nil
	}
	return []agentcore.ContentBlock{{Kind: agentcore.BlockKindText, Text: text}}
}

func (p *Provider) doHTTP(ctx context.Context, body any, extra map[string]any) (*http.Response, error) {
	var data []byte
	var err error

	if len(extra) > 0 {
		var base map[string]any
		raw, merr := json.Marshal(body)
		if merr != nil {
			return nil, fmt.Errorf("marshal request: %w", merr)
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			return nil, fmt.Errorf("unmarshal request: %w", err)
		}
		for k, v := range extra {
			base[k] = v
		}
		data, err = json.Marshal(base)
	} else {
		data, err = json.Marshal(body)
	}
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		p.config.BaseURL+p.config.EndpointPath,
		bytes.NewReader(data),
	)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	if p.config.OrgID != "" {
		httpReq.Header.Set("OpenAI-Organization", p.config.OrgID)
	}
	for k, v := range p.config.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}

	return p.client.Do(httpReq)
}

// formatError attempts to extract a structured error message from API error
// responses. Handles multiple vendor formats:
//
//	{"error": {"message": "..."}}           — OpenAI / DeepSeek / Moonshot / Groq
//	{"code": "...", "message": "..."}       — Qwen (DashScope)
//
// Falls back to the raw body if no structured format is detected.
func formatError(body []byte) string {
	if len(body) == 0 {
		return "empty response body"
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return string(body)
	}
	if e, ok := raw["error"]; ok {
		if em, ok := e.(map[string]any); ok {
			if msg, ok := em["message"].(string); ok && msg != "" {
				if code, ok := em["code"].(string); ok && code != "" {
					return fmt.Sprintf("[%s] %s", code, msg)
				}
				if typ, ok := em["type"].(string); ok && typ != "" {
					return fmt.Sprintf("[%s] %s", typ, msg)
				}
				return msg
			}
		}
	}
	if code, ok := raw["code"].(string); ok && code != "" {
		if msg, ok := raw["message"].(string); ok && msg != "" {
			return fmt.Sprintf("[%s] %s", code, msg)
		}
	}
	return strings.TrimSpace(string(body))
}
