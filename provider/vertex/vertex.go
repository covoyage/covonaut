// Package vertex implements GCP Vertex AI providers for Gemini and Anthropic models.
//
// Gemini on Vertex uses the standard Gemini REST API endpoint.
// Anthropic on Vertex uses Vertex's streamRawPredict endpoint with the same
// request/response schema as the Anthropic Messages API.
package vertex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/gemini"
)

// ---- Config ----

type Config struct {
	Project     string // GCP project ID
	Region      string // e.g. "us-central1"
	AccessToken string // GCP access token; auto-detected if empty
	Client      *http.Client
}

// ---- Token resolution ----

func resolveToken(cfg Config) (string, error) {
	if cfg.AccessToken != "" {
		return cfg.AccessToken, nil
	}
	if token, err := exec.Command("gcloud", "auth", "print-access-token").Output(); err == nil {
		return strings.TrimSpace(string(token)), nil
	}
	return "", fmt.Errorf("vertex: no access token; set VERTEX_ACCESS_TOKEN, GOOGLE_APPLICATION_CREDENTIALS, or run 'gcloud auth login'")
}

// ---- Gemini on Vertex ----

func NewGemini(cfg Config) (agentcore.Provider, error) {
	token, err := resolveToken(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Project == "" || cfg.Region == "" {
		return nil, fmt.Errorf("vertex: project and region are required")
	}
	baseURL := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google",
		cfg.Region, cfg.Project, cfg.Region)
	return gemini.New(gemini.Config{
		APIKey:        token,
		BaseURL:       baseURL,
		UseBearerAuth: true,
	}), nil
}

// ---- Anthropic on Vertex ----

func NewAnthropic(cfg Config) (agentcore.Provider, error) {
	token, err := resolveToken(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Project == "" || cfg.Region == "" {
		return nil, fmt.Errorf("vertex: project and region are required")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &anthropicVertex{
		token:   token,
		project: cfg.Project,
		region:  cfg.Region,
		client:  client,
	}, nil
}

type anthropicVertex struct {
	token, project, region string
	client                 *http.Client
}

func (p *anthropicVertex) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return p.call(ctx, req, false)
}

func (p *anthropicVertex) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return p.callStream(ctx, req)
}

func (p *anthropicVertex) Close() error { return nil }

// ---- Anthropic Vertex HTTP ----

// vertexAnthropicRequest mirrors the Anthropic Messages API format.
type vertexAnthropicRequest struct {
	Model        string           `json:"model"`
	Messages     []vertexMessage  `json:"messages"`
	System       string           `json:"system,omitempty"`
	MaxTokens    int64            `json:"max_tokens"`
	Temperature  *float64         `json:"temperature,omitempty"`
	Stream       bool             `json:"stream,omitempty"`
	Thinking     *vertexThinking  `json:"thinking,omitempty"`
}

type vertexMessage struct {
	Role    string              `json:"role"`
	Content json.RawMessage     `json:"content"`
}

type vertexThinking struct {
	Type         string `json:"type"`
	BudgetTokens int64  `json:"budget_tokens,omitempty"`
}

type vertexAnthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (p *anthropicVertex) call(ctx context.Context, req *agentcore.ProviderRequest, stream bool) (*agentcore.ProviderResponse, error) {
	body, err := p.buildRequest(req, stream)
	if err != nil {
		return nil, err
	}
	url := p.endpoint(req.Model)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vertex: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("vertex: HTTP %d: %s", resp.StatusCode, string(b))
	}

	var vr vertexAnthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return nil, fmt.Errorf("vertex decode: %w", err)
	}

	var textParts []string
	for _, c := range vr.Content {
		if c.Type == "text" && c.Text != "" {
			textParts = append(textParts, c.Text)
		}
	}

	return &agentcore.ProviderResponse{
		Content: strings.Join(textParts, ""),
		Usage: agentcore.TokenUsage{
			PromptTokens:     vr.Usage.InputTokens,
			CompletionTokens: vr.Usage.OutputTokens,
			TotalTokens:      vr.Usage.InputTokens + vr.Usage.OutputTokens,
		},
	}, nil
}



func (p *anthropicVertex) endpoint(model string) string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:streamRawPredict",
		p.region, p.project, p.region, model)
}

func (p *anthropicVertex) buildRequest(req *agentcore.ProviderRequest, stream bool) ([]byte, error) {
	vr := vertexAnthropicRequest{
		Model:     req.Model,
		MaxTokens: 4096,
		Stream:    stream,
	}

	if req.MaxTokens > 0 {
		vr.MaxTokens = req.MaxTokens
	}
	if req.Temperature > 0 {
		t := req.Temperature
		vr.Temperature = &t
	}

	for _, msg := range req.Messages {
		content, _ := json.Marshal([]map[string]string{{"type": "text", "text": msg.Content}})
		switch msg.Role {
		case agentcore.RoleSystem:
			if msg.Content != "" {
				vr.System = msg.Content
			}
		case agentcore.RoleUser:
			vr.Messages = append(vr.Messages, vertexMessage{
				Role:    "user",
				Content: content,
			})
		case agentcore.RoleAssistant:
			vr.Messages = append(vr.Messages, vertexMessage{
				Role:    "assistant",
				Content: content,
			})
		}
	}

	if req.Thinking != nil && req.Thinking.Effort != "" {
		vr.Thinking = &vertexThinking{Type: "enabled"}
	}

	return json.Marshal(vr)
}

// --- Anthropic Vertex SSE streaming types ---

type vertexAnthropicContentBlockStart struct {
	Type         string                           `json:"type"`
	Index        int64                            `json:"index"`
	ContentBlock vertexAnthropicContentBlock      `json:"content_block"`
}

type vertexAnthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type vertexAnthropicContentBlockDelta struct {
	Type  string                    `json:"type"`
	Index int64                     `json:"index"`
	Delta vertexAnthropicBlockDelta `json:"delta"`
}

type vertexAnthropicBlockDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Signature   string `json:"signature,omitempty"`
}

type vertexMessageDelta struct {
	Type  string           `json:"type"`
	Delta vertexDeltaField `json:"delta"`
	Usage vertexUsage      `json:"usage"`
}

type vertexDeltaField struct {
	StopReason   string `json:"stop_reason"`
	StopSequence string `json:"stop_sequence"`
}

type vertexUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func (p *anthropicVertex) callStream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	body, err := p.buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	url := p.endpoint(req.Model)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vertex stream: %w", err)
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("vertex stream: HTTP %d: %s", resp.StatusCode, string(b))
	}

	ch := make(chan agentcore.StreamDelta, 64)

	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
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
				var ev vertexAnthropicContentBlockStart
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					continue
				}
				if ev.ContentBlock.Type == "tool_use" {
					sd := agentcore.StreamDelta{
						ToolCalls: []agentcore.ToolCallDelta{{
							Index: ev.Index,
							ID:    ev.ContentBlock.ID,
							Name:  ev.ContentBlock.Name,
						}},
						Blocks: []agentcore.ContentBlock{{
							Kind:       agentcore.BlockKindToolCall,
							ToolCallID: ev.ContentBlock.ID,
							Name:       ev.ContentBlock.Name,
						}},
					}
					select {
					case ch <- sd:
					case <-ctx.Done():
						return
					}
				}

			case "content_block_delta":
				var ev vertexAnthropicContentBlockDelta
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
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
				var ev vertexMessageDelta
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
		}
	}()

	return ch, nil
}

var _ agentcore.Provider = (*anthropicVertex)(nil)

func init() { _ = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") }
