package gemini

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

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type Config struct {
	APIKey  string
	BaseURL string
	Model   string       // e.g. "gemini-2.5-flash", "gemini-2.5-pro"
	Client  *http.Client // Optional: custom HTTP client, defaults to http.Client with 5m timeout
}

// Provider implements agentcore.Provider for the Gemini native REST API.
type Provider struct {
	config Config
	client *http.Client
}

func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Provider{config: cfg, client: client}
}

// ---------------------------------------------------------------------------
// Gemini API wire types
// ---------------------------------------------------------------------------

type generateRequest struct {
	Contents          []content         `json:"contents"`
	Tools             []tool            `json:"tools,omitempty"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FileData         *fileData         `json:"fileData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type inlineData struct {
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data"`
}

type fileData struct {
	MIMEType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

type functionCall struct {
	Name string         `json:"name"`
	ID   string         `json:"id,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	ID       string         `json:"id,omitempty"`
	Response map[string]any `json:"response"`
}

type tool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type generationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	MaxOutputTokens  *int64          `json:"maxOutputTokens,omitempty"`
	ResponseMIMEType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   map[string]any  `json:"responseSchema,omitempty"`
	ThinkingConfig   *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts"`
	ThinkingBudget  *int64 `json:"thinkingBudget,omitempty"`
}

type generateResponse struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata,omitempty"`
}

type candidate struct {
	Content      content `json:"content"`
	FinishReason string  `json:"finishReason,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount     int64 `json:"promptTokenCount"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	TotalTokenCount      int64 `json:"totalTokenCount"`
}

// ---------------------------------------------------------------------------
// Message conversion
// ---------------------------------------------------------------------------

func ConvertMessages(msgs []agentcore.Message) (*content, []content) {
	var system *content
	var out []content

	for _, m := range msgs {
		switch m.Role {
		case agentcore.RoleSystem:
			system = &content{
				Parts: PartsFromMessage(m),
			}

		case agentcore.RoleUser:
			out = append(out, content{
				Role:  "user",
				Parts: PartsFromMessage(m),
			})

		case agentcore.RoleAssistant:
			c := content{Role: "model", Parts: PartsFromMessage(m)}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Arguments), &args)
				c.Parts = append(c.Parts, part{
					FunctionCall: &functionCall{
						Name: tc.Name,
						ID:   tc.ID,
						Args: args,
					},
				})
			}
			if len(c.Parts) == 0 {
				c.Parts = []part{{Text: ""}}
			}
			out = append(out, c)

		case agentcore.RoleTool:
			var respData map[string]any
			if err := json.Unmarshal([]byte(m.Content), &respData); err != nil {
				respData = map[string]any{"result": m.Content}
			}
			fr := part{
				FunctionResponse: &functionResponse{
					Name:     m.Name,
					ID:       m.ToolCallID,
					Response: respData,
				},
			}
			// Group consecutive function responses into one "function" content
			if n := len(out); n > 0 && out[n-1].Role == "function" {
				out[n-1].Parts = append(out[n-1].Parts, fr)
			} else {
				out = append(out, content{
					Role:  "function",
					Parts: []part{fr},
				})
			}
		}
	}
	return system, out
}

func PartsFromMessage(m agentcore.Message) []part {
	parts := make([]part, 0, len(m.Blocks)+1)
	if m.Content != "" {
		parts = append(parts, part{Text: m.Content})
	}
	for _, bl := range m.Blocks {
		switch bl.Kind {
		case agentcore.BlockKindText:
			if bl.Text != "" {
				parts = append(parts, part{Text: bl.Text})
			}
		case agentcore.BlockKindThinking:
			parts = append(parts, part{
				Text:             bl.Text,
				Thought:          true,
				ThoughtSignature: bl.Signature,
			})
		case agentcore.BlockKindImage:
			if p, ok := ImagePartFromBlock(bl); ok {
				parts = append(parts, p)
			} else if bl.URL != "" {
				parts = append(parts, part{Text: "[image] " + bl.URL})
			}
		}
	}
	if len(parts) == 0 {
		return []part{{Text: ""}}
	}
	return parts
}

func ImagePartFromBlock(bl agentcore.ContentBlock) (part, bool) {
	if bl.URL == "" {
		return part{}, false
	}
	if data, mime, ok := ParseDataURL(bl.URL, bl.MediaType); ok {
		return part{
			InlineData: &inlineData{
				MIMEType: mime,
				Data:     data,
			},
		}, true
	}
	if strings.HasPrefix(bl.URL, "gs://") || strings.HasPrefix(bl.URL, "file://") {
		return part{
			FileData: &fileData{
				MIMEType: bl.MediaType,
				FileURI:  bl.URL,
			},
		}, true
	}
	return part{}, false
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

func ConvertTools(defs []agentcore.ToolDefinition) []tool {
	if len(defs) == 0 {
		return nil
	}
	fds := make([]functionDeclaration, len(defs))
	for i, d := range defs {
		fds[i] = functionDeclaration{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
		}
	}
	return []tool{{FunctionDeclarations: fds}}
}

// ---------------------------------------------------------------------------
// Provider implementation
// ---------------------------------------------------------------------------

func (p *Provider) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	gr := p.buildRequest(req)

	model := p.resolveModel(req.Model)
	url := fmt.Sprintf("%s/models/%s:generateContent", p.config.BaseURL, model)

	httpResp, err := p.doHTTP(ctx, url, gr)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("gemini api error (status %d): %s", httpResp.StatusCode, body)
	}

	var resp generateResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.parseResponse(resp, req.ResponseFormat), nil
}

func (p *Provider) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	gr := p.buildRequest(req)

	model := p.resolveModel(req.Model)
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", p.config.BaseURL, model)

	httpResp, err := p.doHTTP(ctx, url, gr)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		httpResp.Body.Close()
		return nil, fmt.Errorf("gemini api error (status %d): %s", httpResp.StatusCode, body)
	}

	ch := make(chan agentcore.StreamDelta, 64)

	go func() {
		defer httpResp.Body.Close()
		defer close(ch)

		var toolCallIndex int64
		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 256*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" {
				continue
			}

			var chunk generateResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if len(chunk.Candidates) == 0 {
				if chunk.UsageMetadata != nil {
					sd := agentcore.StreamDelta{
						Usage: &agentcore.TokenUsage{
							PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
							CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
							TotalTokens:      chunk.UsageMetadata.TotalTokenCount,
						},
					}
					select {
					case ch <- sd:
					case <-ctx.Done():
						return
					}
				}
				continue
			}

			cand := chunk.Candidates[0]
			var sd agentcore.StreamDelta

			for _, p := range cand.Content.Parts {
				if p.Text != "" {
					if p.Thought {
						sd.Blocks = agentcore.MergeContentBlocks(sd.Blocks, agentcore.ContentBlock{
							Kind:      agentcore.BlockKindThinking,
							Text:      p.Text,
							Signature: p.ThoughtSignature,
						})
					} else {
						sd.Content += p.Text
						sd.Blocks = agentcore.MergeContentBlocks(sd.Blocks, agentcore.ContentBlock{
							Kind: agentcore.BlockKindText,
							Text: p.Text,
						})
					}
				}
				if p.FunctionCall != nil {
					argsJSON, _ := json.Marshal(p.FunctionCall.Args)
					sd.ToolCalls = append(sd.ToolCalls, agentcore.ToolCallDelta{
						Index:     toolCallIndex,
						ID:        p.FunctionCall.ID,
						Name:      p.FunctionCall.Name,
						Arguments: string(argsJSON),
					})
					sd.Blocks = agentcore.MergeContentBlocks(sd.Blocks, agentcore.ContentBlock{
						Kind:       agentcore.BlockKindToolCall,
						ToolCallID: p.FunctionCall.ID,
						Name:       p.FunctionCall.Name,
						Arguments:  string(argsJSON),
						Signature:  p.ThoughtSignature,
					})
					toolCallIndex++
				}
			}

			if chunk.UsageMetadata != nil {
				sd.Usage = &agentcore.TokenUsage{
					PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
					CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
					TotalTokens:      chunk.UsageMetadata.TotalTokenCount,
				}
			}

			if cand.FinishReason == "STOP" || cand.FinishReason == "MAX_TOKENS" {
				sd.Done = true
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

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (p *Provider) buildRequest(req *agentcore.ProviderRequest) generateRequest {
	system, contents := ConvertMessages(req.Messages)

	gr := generateRequest{
		Contents:          contents,
		Tools:             ConvertTools(req.Tools),
		SystemInstruction: system,
	}

	if req.Temperature > 0 || req.MaxTokens > 0 {
		gc := &generationConfig{}
		if req.Temperature > 0 {
			t := req.Temperature
			gc.Temperature = &t
		}
		if req.MaxTokens > 0 {
			m := req.MaxTokens
			gc.MaxOutputTokens = &m
		}
		gr.GenerationConfig = gc
	}
	if req.ResponseFormat != nil {
		if gr.GenerationConfig == nil {
			gr.GenerationConfig = &generationConfig{}
		}
		switch req.ResponseFormat.Type {
		case agentcore.ResponseFormatJSONObject:
			gr.GenerationConfig.ResponseMIMEType = "application/json"
		case agentcore.ResponseFormatJSONSchema:
			gr.GenerationConfig.ResponseMIMEType = "application/json"
			if req.ResponseFormat.JSONSchema != nil {
				gr.GenerationConfig.ResponseSchema = req.ResponseFormat.JSONSchema.Schema
			}
		}
	}
	if req.Thinking != nil {
		if gr.GenerationConfig == nil {
			gr.GenerationConfig = &generationConfig{}
		}
		tc := &thinkingConfig{
			IncludeThoughts: req.Thinking.VisibleThoughtsEnabled(),
		}
		if req.Thinking.Budget != 0 {
			b := req.Thinking.Budget
			tc.ThinkingBudget = &b
		}
		gr.GenerationConfig.ThinkingConfig = tc
	}

	return gr
}

func (p *Provider) resolveModel(reqModel string) string {
	if reqModel != "" {
		return reqModel
	}
	if p.config.Model != "" {
		return p.config.Model
	}
	return "gemini-2.5-flash"
}

func (p *Provider) parseResponse(resp generateResponse, format *agentcore.ResponseFormat) *agentcore.ProviderResponse {
	pr := &agentcore.ProviderResponse{}

	if resp.UsageMetadata != nil {
		pr.Usage = agentcore.TokenUsage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		}
	}

	if len(resp.Candidates) == 0 {
		return pr
	}

	cand := resp.Candidates[0]
	var textParts []string
	var blocks []agentcore.ContentBlock

	for _, p := range cand.Content.Parts {
		if p.Text != "" {
			if p.Thought {
				blocks = append(blocks, agentcore.ContentBlock{
					Kind:      agentcore.BlockKindThinking,
					Text:      p.Text,
					Signature: p.ThoughtSignature,
				})
			} else {
				textParts = append(textParts, p.Text)
				blocks = append(blocks, agentcore.ContentBlock{
					Kind: agentcore.BlockKindText,
					Text: p.Text,
				})
			}
		}
		if p.FunctionCall != nil {
			argsJSON, _ := json.Marshal(p.FunctionCall.Args)
			id := p.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("call_%s", p.FunctionCall.Name)
			}
			pr.ToolCalls = append(pr.ToolCalls, agentcore.ToolCall{
				ID:        id,
				Name:      p.FunctionCall.Name,
				Arguments: string(argsJSON),
			})
			blocks = append(blocks, agentcore.ContentBlock{
				Kind:       agentcore.BlockKindToolCall,
				ToolCallID: id,
				Name:       p.FunctionCall.Name,
				Arguments:  string(argsJSON),
				Signature:  p.ThoughtSignature,
			})
		}
	}

	pr.Content = strings.Join(textParts, "")
	pr.Blocks = blocks
	pr.Structured = agentcore.ExtractStructuredContent(pr.Content, format)
	return pr
}

func (p *Provider) doHTTP(ctx context.Context, url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.config.APIKey)

	return p.client.Do(httpReq)
}
