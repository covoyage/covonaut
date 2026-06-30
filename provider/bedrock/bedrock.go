package bedrock

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

// Config holds Bedrock-specific configuration.
type Config struct {
	AccessKey    string
	SecretKey    string
	SessionToken string
	Region       string
	Client       *http.Client
}

// New creates a Bedrock Converse API provider.
func New(cfg Config) (agentcore.Provider, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("bedrock: region is required")
	}
	if cfg.AccessKey == "" {
		return nil, fmt.Errorf("bedrock: AWS credentials required")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &bedrockProvider{
		accessKey:    cfg.AccessKey,
		secretKey:    cfg.SecretKey,
		sessionToken: cfg.SessionToken,
		region:       cfg.Region,
		client:       client,
	}, nil
}

type bedrockProvider struct {
	accessKey, secretKey, sessionToken, region string
	client                                     *http.Client
}

func (p *bedrockProvider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return p.converse(ctx, req)
}

func (p *bedrockProvider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return p.converseStream(ctx, req)
}

func (p *bedrockProvider) Close() error { return nil }

// ---- Converse (non-streaming) ----

type converseResponse struct {
	Output struct {
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string       `json:"stopReason"`
	Usage      converseUsage `json:"usage"`
}

type converseUsage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	TotalTokens  int64 `json:"totalTokens"`
}

func (p *bedrockProvider) converse(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	body, err := buildBody(req)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", p.region, req.Model)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
	resp, err := signedDo(httpReq, body, p.accessKey, p.secretKey, p.sessionToken, p.region, p.client)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("bedrock: HTTP %d: %s", resp.StatusCode, string(b))
	}
	var cr converseResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	content, blocks := extractResponse(cr.Output.Message.Content)
	return &agentcore.ProviderResponse{
		Content: content,
		Blocks:  blocks,
		Usage: agentcore.TokenUsage{
			PromptTokens:     cr.Usage.InputTokens,
			CompletionTokens: cr.Usage.OutputTokens,
			TotalTokens:      cr.Usage.TotalTokens,
		},
	}, nil
}

// ---- Converse Stream ----

type converseStreamEvent struct {
	ContentBlockDelta *struct {
		Delta json.RawMessage `json:"delta"`
	} `json:"contentBlockDelta"`
	Metadata *struct {
		Usage converseUsage `json:"usage"`
	} `json:"metadata"`
}

func (p *bedrockProvider) converseStream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	body, err := buildBody(req)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse-stream", p.region, req.Model)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
	httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")
	resp, err := signedDo(httpReq, body, p.accessKey, p.secretKey, p.sessionToken, p.region, p.client)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("bedrock stream: HTTP %d: %s", resp.StatusCode, string(b))
	}
	ch := make(chan agentcore.StreamDelta, 10)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			idx := bytes.IndexByte(line, '{')
			if idx < 0 {
				continue
			}
			var ev converseStreamEvent
			if json.Unmarshal(line[idx:], &ev) != nil {
				continue
			}
			if ev.ContentBlockDelta != nil {
				content, blocks := extractResponse(ev.ContentBlockDelta.Delta)
				ch <- agentcore.StreamDelta{Content: content, Blocks: blocks}
			}
			if ev.Metadata != nil {
				ch <- agentcore.StreamDelta{
					Done: true,
					Usage: &agentcore.TokenUsage{
						PromptTokens:     ev.Metadata.Usage.InputTokens,
						CompletionTokens: ev.Metadata.Usage.OutputTokens,
						TotalTokens:      ev.Metadata.Usage.TotalTokens,
					},
				}
			}
		}
	}()
	return ch, nil
}

// ---- Request building ----

func buildBody(req *agentcore.ProviderRequest) ([]byte, error) {
	msgs, system := convertMessages(req.Messages)
	body := map[string]any{"messages": msgs}
	if len(system) > 0 {
		body["system"] = system
	}
	if ic := buildInferenceConfig(req); len(ic) > 0 {
		body["inferenceConfig"] = ic
	}
	return json.Marshal(body)
}

func convertMessages(msgs []agentcore.Message) (converseMsgs []map[string]any, system []map[string]any) {
	for _, msg := range msgs {
		if msg.Role == agentcore.RoleSystem && msg.Content != "" {
			system = append(system, map[string]any{"text": msg.Content})
			continue
		}
		role := "user"
		if msg.Role == agentcore.RoleAssistant {
			role = "assistant"
		}
		converseMsgs = append(converseMsgs, map[string]any{
			"role":    role,
			"content": []map[string]any{{"text": msg.Content}},
		})
	}
	return
}

func buildInferenceConfig(req *agentcore.ProviderRequest) map[string]any {
	ic := map[string]any{}
	if req.MaxTokens > 0 {
		ic["maxTokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		ic["temperature"] = req.Temperature
	}
	return ic
}

// ---- Response extraction ----

type contentBlock struct {
	Text             string `json:"text"`
	ReasoningContent *struct {
		Text string `json:"text"`
	} `json:"reasoningContent"`
}

func extractResponse(raw json.RawMessage) (text string, blocks []agentcore.ContentBlock) {
	if len(raw) == 0 {
		return
	}
	var cbs []contentBlock
	if json.Unmarshal(raw, &cbs) != nil {
		return
	}
	var parts []string
	for _, b := range cbs {
		if b.ReasoningContent != nil && b.ReasoningContent.Text != "" {
			blocks = append(blocks, agentcore.ContentBlock{
				Kind: agentcore.BlockKindThinking,
				Text: b.ReasoningContent.Text,
			})
		}
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	text = strings.Join(parts, "")
	return
}

var _ agentcore.Provider = (*bedrockProvider)(nil)
