package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	defaultVersion   = "2023-06-01"
	defaultMaxTokens = 4096
)

type Config struct {
	APIKey  string
	BaseURL string
	Version string       // API version header, defaults to "2023-06-01"
	Client  *http.Client // Optional: custom HTTP client, defaults to http.Client with 5m timeout
}

// Provider implements agentcore.Provider for the Anthropic Messages API.
type Provider struct {
	config Config
	client *http.Client
}

func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Version == "" {
		cfg.Version = defaultVersion
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Provider{config: cfg, client: client}
}

// --- Anthropic API wire types ---

type apiRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int64           `json:"max_tokens"`
	System      string          `json:"system,omitempty"`
	Messages    []apiMessage    `json:"messages"`
	Tools       []apiTool       `json:"tools,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Thinking    *thinkingConfig `json:"thinking,omitempty"`
}

type thinkingConfig struct {
	Type    string `json:"type"`
	Display string `json:"display,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

type apiMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type         string                   `json:"type"`
	Text         string                   `json:"text,omitempty"`         // text block
	Thinking     string                   `json:"thinking,omitempty"`     // thinking block
	Signature    string                   `json:"signature,omitempty"`    // thinking block integrity signature
	Source       *imageSource             `json:"source,omitempty"`       // image block
	ID           string                   `json:"id,omitempty"`           // tool_use
	Name         string                   `json:"name,omitempty"`         // tool_use
	Input        any                      `json:"input,omitempty"`        // tool_use
	ToolUseID    string                   `json:"tool_use_id,omitempty"`  // tool_result
	Content      string                   `json:"content,omitempty"`      // tool_result body
	CacheControl *agentcore.CacheControlMarker `json:"cache_control,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data"`
}

type apiTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type apiResponse struct {
	ID         string         `json:"id"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      apiUsage       `json:"usage"`
}

type apiUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// --- streaming wire types ---

type streamEventData struct {
	Type string `json:"type"`
}

type messageDeltaData struct {
	Type  string   `json:"type"`
	Usage apiUsage `json:"usage"`
}

type contentBlockStartData struct {
	Type         string       `json:"type"`
	Index        int64        `json:"index"`
	ContentBlock contentBlock `json:"content_block"`
}

type contentBlockDeltaData struct {
	Type  string     `json:"type"`
	Index int64      `json:"index"`
	Delta blockDelta `json:"delta"`
}

type blockDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Signature   string `json:"signature,omitempty"`
}

// --- message conversion ---

func ConvertMessages(msgs []agentcore.Message) (string, []apiMessage) {
	var system string
	var out []apiMessage

	for _, m := range msgs {
		switch m.Role {
		case agentcore.RoleSystem:
			system = m.Content

		case agentcore.RoleUser:
			blocks := BlocksFromUserMessage(m)
			blocks = applyCacheControl(blocks, m.CacheControl)
			out = append(out, apiMessage{
				Role:    "user",
				Content: blocks,
			})

		case agentcore.RoleAssistant:
			am := apiMessage{Role: "assistant"}
			if m.Content != "" {
				am.Content = append(am.Content, contentBlock{Type: "text", Text: m.Content})
			}
			for _, bl := range m.Blocks {
				switch bl.Kind {
				case agentcore.BlockKindText:
					if bl.Text != "" {
						am.Content = append(am.Content, contentBlock{Type: "text", Text: bl.Text})
					}
				case agentcore.BlockKindThinking:
					am.Content = append(am.Content, contentBlock{
						Type:      "thinking",
						Thinking:  bl.Text,
						Signature: bl.Signature,
					})
				}
			}
			for _, tc := range m.ToolCalls {
				var input any
				_ = json.Unmarshal([]byte(tc.Arguments), &input)
				am.Content = append(am.Content, contentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			am.Content = applyCacheControl(am.Content, m.CacheControl)
			out = append(out, am)

		case agentcore.RoleTool:
			result := contentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			}
			// Group consecutive tool results into a single user message.
			if n := len(out); n > 0 && out[n-1].Role == "user" && len(out[n-1].Content) > 0 && out[n-1].Content[0].Type == "tool_result" {
				out[n-1].Content = append(out[n-1].Content, result)
			} else {
				out = append(out, apiMessage{Role: "user", Content: []contentBlock{result}})
			}
		}
	}
	return system, out
}

func applyCacheControl(blocks []contentBlock, cc *agentcore.CacheControlMarker) []contentBlock {
	if cc == nil {
		return blocks
	}
	for i := range blocks {
		blocks[i].CacheControl = cc
	}
	return blocks
}

func BlocksFromUserMessage(m agentcore.Message) []contentBlock {
	blocks := make([]contentBlock, 0, len(m.Blocks)+1)
	if m.Content != "" {
		blocks = append(blocks, contentBlock{Type: "text", Text: m.Content})
	}
	for _, bl := range m.Blocks {
		switch bl.Kind {
		case agentcore.BlockKindText:
			if bl.Text != "" {
				blocks = append(blocks, contentBlock{Type: "text", Text: bl.Text})
			}
		case agentcore.BlockKindImage:
			if block, ok := ImageBlockFromContent(bl); ok {
				blocks = append(blocks, block)
			} else if bl.URL != "" {
				blocks = append(blocks, contentBlock{Type: "text", Text: "[image] " + bl.URL})
			}
		}
	}
	if len(blocks) == 0 {
		return []contentBlock{{Type: "text", Text: ""}}
	}
	return blocks
}

func ImageBlockFromContent(bl agentcore.ContentBlock) (contentBlock, bool) {
	if bl.URL == "" {
		return contentBlock{}, false
	}
	data, mime, ok := ParseDataURL(bl.URL, bl.MediaType)
	if !ok {
		return contentBlock{}, false
	}
	return contentBlock{
		Type: "image",
		Source: &imageSource{
			Type:      "base64",
			MediaType: mime,
			Data:      data,
		},
	}, true
}

func ParseDataURL(raw string, fallbackMIME string) (data string, mime string, ok bool) {
	if !strings.HasPrefix(raw, "data:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(raw, "data:")
	meta, encoded, found := strings.Cut(rest, ",")
	if !found {
		return "", "", false
	}
	if !strings.HasSuffix(meta, ";base64") {
		return "", "", false
	}
	mime = strings.TrimSuffix(meta, ";base64")
	if mime == "" {
		mime = fallbackMIME
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	return base64.StdEncoding.EncodeToString(decoded), mime, true
}

func ConvertTools(defs []agentcore.ToolDefinition) []apiTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]apiTool, len(defs))
	for i, d := range defs {
		out[i] = apiTool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.Parameters,
		}
	}
	return out
}

// --- Provider implementation ---

func (p *Provider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	ar := p.buildRequest(req, false)

	httpResp, err := p.doHTTP(ctx, ar)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("anthropic api error (status %d): %s", httpResp.StatusCode, body)
	}

	var resp apiResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.parseResponse(resp, req.ResponseFormat), nil
}

func (p *Provider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ar := p.buildRequest(req, true)

	httpResp, err := p.doHTTP(ctx, ar)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		httpResp.Body.Close()
		return nil, fmt.Errorf("anthropic api error (status %d): %s", httpResp.StatusCode, body)
	}

	ch := make(chan agentcore.StreamDelta, 64)

	go func() {
		defer httpResp.Body.Close()
		defer close(ch)

		// Track block types by index to distinguish text vs tool_use deltas.
		type blockInfo struct {
			kind string
			id   string
			name string
		}
		blocks := map[int64]*blockInfo{}

		scanner := bufio.NewScanner(httpResp.Body)
		var eventType string

		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				eventType = strings.TrimPrefix(line, "event: ")
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			switch eventType {
			case "content_block_start":
				var ev contentBlockStartData
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					continue
				}
				cb := ev.ContentBlock
				blocks[ev.Index] = &blockInfo{kind: cb.Type, id: cb.ID, name: cb.Name}
				if cb.Type == "tool_use" {
					sd := agentcore.StreamDelta{
						ToolCalls: []agentcore.ToolCallDelta{{
							Index: ev.Index,
							ID:    cb.ID,
							Name:  cb.Name,
						}},
						Blocks: []agentcore.ContentBlock{{
							Kind:       agentcore.BlockKindToolCall,
							ToolCallID: cb.ID,
							Name:       cb.Name,
						}},
					}
					select {
					case ch <- sd:
					case <-ctx.Done():
						return
					}
				}

			case "content_block_delta":
				var ev contentBlockDeltaData
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					continue
				}
				bi := blocks[ev.Index]
				if bi == nil {
					continue
				}
				var sd agentcore.StreamDelta
				switch ev.Delta.Type {
				case "text_delta":
					sd.Content = ev.Delta.Text
					if ev.Delta.Text != "" {
						sd.Blocks = []agentcore.ContentBlock{{
							Kind: agentcore.BlockKindText,
							Text: ev.Delta.Text,
						}}
					}
				case "thinking_delta":
					if ev.Delta.Thinking != "" {
						sd.Blocks = []agentcore.ContentBlock{{
							Kind: agentcore.BlockKindThinking,
							Text: ev.Delta.Thinking,
						}}
					}
				case "input_json_delta":
					sd.ToolCalls = []agentcore.ToolCallDelta{{
						Index:     ev.Index,
						Arguments: ev.Delta.PartialJSON,
					}}
					sd.Blocks = []agentcore.ContentBlock{{
						Kind:      agentcore.BlockKindToolCall,
						Arguments: ev.Delta.PartialJSON,
					}}
				case "signature_delta":
					if ev.Delta.Signature != "" {
						sd.Blocks = []agentcore.ContentBlock{{
							Kind:      agentcore.BlockKindThinking,
							Signature: ev.Delta.Signature,
						}}
					}
				default:
					continue
				}
				select {
				case ch <- sd:
				case <-ctx.Done():
					return
				}

			case "message_delta":
				var ev messageDeltaData
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					continue
				}
				if ev.Usage.OutputTokens > 0 {
					sd := agentcore.StreamDelta{
						Usage: &agentcore.TokenUsage{
							CompletionTokens: ev.Usage.OutputTokens,
							TotalTokens:      ev.Usage.InputTokens + ev.Usage.OutputTokens,
						},
					}
					select {
					case ch <- sd:
					case <-ctx.Done():
						return
					}
				}

			case "message_stop":
				return
			}

			eventType = ""
		}
	}()

	return ch, nil
}

// --- internal helpers ---

func (p *Provider) buildRequest(req *agentcore.ProviderRequest, stream bool) apiRequest {
	system, messages := ConvertMessages(req.Messages)
	if instruction := structuredOutputInstruction(req.ResponseFormat); instruction != "" {
		if system != "" {
			system += "\n\n" + instruction
		} else {
			system = instruction
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	ar := apiRequest{
		Model:     req.Model,
		System:    system,
		Messages:  messages,
		Tools:     ConvertTools(req.Tools),
		Stream:    stream,
		MaxTokens: maxTokens,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		ar.Temperature = &t
	}
	if req.Thinking != nil {
		ar.Thinking = &thinkingConfig{
			Type:    "adaptive",
			Display: string(req.Thinking.NormalizedDisplay()),
			Effort:  string(req.Thinking.Effort),
		}
	}
	return ar
}

func (p *Provider) parseResponse(resp apiResponse, format *agentcore.ResponseFormat) *agentcore.ProviderResponse {
	pr := &agentcore.ProviderResponse{
		Usage: agentcore.TokenUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}

	var textParts []string
	var blocks []agentcore.ContentBlock
	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			blocks = append(blocks, agentcore.ContentBlock{
				Kind:      agentcore.BlockKindThinking,
				Text:      block.Thinking,
				Signature: block.Signature,
			})
		case "text":
			textParts = append(textParts, block.Text)
			if block.Text != "" {
				blocks = append(blocks, agentcore.ContentBlock{
					Kind: agentcore.BlockKindText,
					Text: block.Text,
				})
			}
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			pr.ToolCalls = append(pr.ToolCalls, agentcore.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
			blocks = append(blocks, agentcore.ContentBlock{
				Kind:       agentcore.BlockKindToolCall,
				ToolCallID: block.ID,
				Name:       block.Name,
				Arguments:  string(argsJSON),
			})
		}
	}
	pr.Content = strings.Join(textParts, "")
	pr.Blocks = blocks
	pr.Structured = agentcore.ExtractStructuredContent(pr.Content, format)
	return pr
}

func structuredOutputInstruction(format *agentcore.ResponseFormat) string {
	if format == nil {
		return ""
	}
	return format.PromptInstruction()
}

func (p *Provider) doHTTP(ctx context.Context, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		p.config.BaseURL+"/v1/messages",
		bytes.NewReader(data),
	)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.config.APIKey)
	httpReq.Header.Set("anthropic-version", p.config.Version)

	return p.client.Do(httpReq)
}
