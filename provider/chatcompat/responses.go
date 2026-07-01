package chatcompat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

type APIProtocol string

const (
	APIProtocolChat     APIProtocol = ""
	APIProtocolResponses APIProtocol = "responses"
)

type responsesRequest struct {
	Model              string                `json:"model"`
	Input              any                   `json:"input"`
	Instructions       string                `json:"instructions,omitempty"`
	Tools              []responsesTool       `json:"tools,omitempty"`
	Stream             bool                  `json:"stream,omitempty"`
	Temperature        *float64              `json:"temperature,omitempty"`
	MaxOutputTokens    *int64                `json:"max_output_tokens,omitempty"`
	Text               *responsesTextConfig  `json:"text,omitempty"`
	PreviousResponseID string                `json:"previous_response_id,omitempty"`
	Store              *bool                 `json:"store,omitempty"`
	Reasoning          *responsesReasoning   `json:"reasoning,omitempty"`
}

type responsesTextConfig struct {
	Format *responsesTextFormat `json:"format,omitempty"`
}

type responsesTextFormat struct {
	Type       string         `json:"type"`
	Name       string         `json:"name,omitempty"`
	Strict     bool           `json:"strict,omitempty"`
	Schema     map[string]any `json:"schema,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesTool struct {
	Type     string               `json:"type"`
	Name     string               `json:"name,omitempty"`
	Function *responsesToolFunc   `json:"function,omitempty"`
}

type responsesToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type responsesInputMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type responsesInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type responsesFunctionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type responsesResponse struct {
	ID     string               `json:"id"`
	Object string               `json:"object"`
	Status string               `json:"status"`
	Output []responsesOutputItem `json:"output"`
	Usage  *responsesUsage      `json:"usage,omitempty"`
	Error  *responsesError      `json:"error,omitempty"`
}

type responsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responsesOutputItem struct {
	Type      string                    `json:"type"`
	ID        string                    `json:"id,omitempty"`
	Status    string                    `json:"status,omitempty"`
	Role      string                    `json:"role,omitempty"`
	Content   []responsesOutputContent  `json:"content,omitempty"`
	Name      string                    `json:"name,omitempty"`
	CallID    string                    `json:"call_id,omitempty"`
	Arguments string                    `json:"arguments,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type responsesStreamEvent struct {
	Type   string          `json:"type"`
	Delta  string          `json:"delta,omitempty"`
	Item   json.RawMessage `json:"item,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
	Usage  *responsesUsage `json:"usage,omitempty"`
}

func ToResponsesInput(msgs []agentcore.Message) any {
	if len(msgs) == 1 && msgs[0].Role == agentcore.RoleUser && msgs[0].ToolCallID == "" && len(msgs[0].ToolCalls) == 0 {
		if len(msgs[0].Blocks) == 0 {
			return msgs[0].Content
		}
	}

	items := make([]any, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case agentcore.RoleSystem:
			items = append(items, responsesInputMessage{
				Role:    "developer",
				Content: m.Content,
			})

		case agentcore.RoleUser:
			if len(m.Blocks) == 0 {
				items = append(items, responsesInputMessage{
					Role:    "user",
					Content: m.Content,
				})
			} else {
				parts := make([]responsesInputContent, 0, len(m.Blocks)+1)
				if m.Content != "" {
					parts = append(parts, responsesInputContent{
						Type: "input_text",
						Text: m.Content,
					})
				}
				for _, bl := range m.Blocks {
					switch bl.Kind {
					case agentcore.BlockKindText:
						if bl.Text != "" {
							parts = append(parts, responsesInputContent{
								Type: "input_text",
								Text: bl.Text,
							})
						}
					case agentcore.BlockKindImage:
						if bl.URL != "" {
							parts = append(parts, responsesInputContent{
								Type:     "input_image",
								ImageURL: bl.URL,
								Detail:   bl.Detail,
							})
						}
					}
				}
				items = append(items, responsesInputMessage{
					Role:    "user",
					Content: parts,
				})
			}

		case agentcore.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					items = append(items, map[string]any{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Name,
						"arguments": tc.Arguments,
					})
				}
			} else {
				items = append(items, responsesInputMessage{
					Role:    "assistant",
					Content: m.Content,
				})
			}

		case agentcore.RoleTool:
			items = append(items, responsesFunctionCallOutput{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		}
	}
	return items
}

func ToResponsesTools(defs []agentcore.ToolDefinition) []responsesTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]responsesTool, len(defs))
	for i, d := range defs {
		out[i] = responsesTool{
			Type: "function",
			Function: &responsesToolFunc{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		}
	}
	return out
}

func ToResponsesTextFormat(format *agentcore.ResponseFormat) *responsesTextConfig {
	if format == nil {
		return nil
	}
	if format.Type == agentcore.ResponseFormatJSONSchema && format.JSONSchema != nil {
		return &responsesTextConfig{
			Format: &responsesTextFormat{
				Type:   "json_schema",
				Name:   format.JSONSchema.Name,
				Strict: format.JSONSchema.Strict,
				Schema: format.JSONSchema.Schema,
			},
		}
	}
	return nil
}

func (p *Provider) completeResponses(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	msgs := req.Messages
	if p.config.PrepareMessages != nil {
		msgs = p.config.PrepareMessages(msgs)
	}

	rr := p.buildResponsesRequest(req, msgs, false)

	httpResp, err := p.doHTTP(ctx, rr, nil)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("responses api error (status %d): %s", httpResp.StatusCode, formatError(body))
	}

	var resp responsesResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode responses api response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("responses api error: %s: %s", resp.Error.Code, resp.Error.Message)
	}

	return p.convertResponsesOutput(&resp, req)
}

func (p *Provider) streamResponses(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	msgs := req.Messages
	if p.config.PrepareMessages != nil {
		msgs = p.config.PrepareMessages(msgs)
	}

	rr := p.buildResponsesRequest(req, msgs, true)

	httpResp, err := p.doHTTP(ctx, rr, nil)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		httpResp.Body.Close()
		return nil, fmt.Errorf("responses api error (status %d): %s", httpResp.StatusCode, formatError(body))
	}

	ch := make(chan agentcore.StreamDelta, 64)

	go func() {
		defer httpResp.Body.Close()
		defer close(ch)

		var currentFunctionCall struct {
			ID       string
			CallID   string
			Name     string
			ArgsBuf  strings.Builder
		}

		scanner := bufio.NewScanner(httpResp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "event: ") {
				if strings.HasPrefix(line, "data: ") {
				}
				continue
			}
			eventType := strings.TrimPrefix(line, "event: ")

			if !scanner.Scan() {
				return
			}
			dataLine := scanner.Text()
			if !strings.HasPrefix(dataLine, "data: ") {
				continue
			}
			data := strings.TrimPrefix(dataLine, "data: ")

			var evt responsesStreamEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue
			}
			evt.Type = eventType

			switch evt.Type {
			case "response.output_text.delta":
				sd := agentcore.StreamDelta{Content: evt.Delta}
				if evt.Delta != "" {
					sd.Blocks = TextBlocks(evt.Delta)
				}
				select {
				case ch <- sd:
				case <-ctx.Done():
					return
				}

			case "response.function_call_arguments.delta":
				currentFunctionCall.ArgsBuf.WriteString(evt.Delta)

			case "response.output_item.added":
				var item responsesOutputItem
				if err := json.Unmarshal(evt.Item, &item); err == nil {
					if item.Type == "function_call" {
						currentFunctionCall.ID = item.ID
						currentFunctionCall.CallID = item.CallID
						currentFunctionCall.Name = item.Name
						currentFunctionCall.ArgsBuf.Reset()
					}
				}

			case "response.output_item.done":
				var item responsesOutputItem
				if err := json.Unmarshal(evt.Item, &item); err == nil {
					if item.Type == "function_call" && item.CallID != "" {
						args := currentFunctionCall.ArgsBuf.String()
						if args == "" {
							args = item.Arguments
						}
						sd := agentcore.StreamDelta{
							ToolCalls: []agentcore.ToolCallDelta{
								{
									ID:        currentFunctionCall.CallID,
									Name:      currentFunctionCall.Name,
									Arguments: args,
								},
							},
							Blocks: []agentcore.ContentBlock{
								{
									Kind:       agentcore.BlockKindToolCall,
									ToolCallID: currentFunctionCall.CallID,
									Name:       currentFunctionCall.Name,
									Arguments:  args,
								},
							},
						}
						select {
						case ch <- sd:
						case <-ctx.Done():
							return
						}
					}
				}

			case "response.completed":
				if evt.Usage != nil {
					sd := agentcore.StreamDelta{
						Usage: &agentcore.TokenUsage{
							PromptTokens:     evt.Usage.InputTokens,
							CompletionTokens: evt.Usage.OutputTokens,
							TotalTokens:      evt.Usage.TotalTokens,
						},
					}
					select {
					case ch <- sd:
					case <-ctx.Done():
						return
					}
				}
				return
			}
		}
	}()

	return ch, nil
}

func (p *Provider) buildResponsesRequest(req *agentcore.ProviderRequest, msgs []agentcore.Message, stream bool) responsesRequest {
	rr := responsesRequest{
		Model:  req.Model,
		Input:  ToResponsesInput(msgs),
		Tools:  ToResponsesTools(req.Tools),
		Stream: stream,
		Text:   ToResponsesTextFormat(req.ResponseFormat),
	}

	for _, m := range msgs {
		if m.Role == agentcore.RoleSystem {
			rr.Instructions = m.Content
			break
		}
	}

	if req.Temperature > 0 {
		t := req.Temperature
		rr.Temperature = &t
	}
	if req.MaxTokens > 0 {
		m := req.MaxTokens
		rr.MaxOutputTokens = &m
	}

	store := false
	rr.Store = &store

	return rr
}

func (p *Provider) convertResponsesOutput(resp *responsesResponse, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	out := &agentcore.ProviderResponse{}

	if resp.Usage != nil {
		out.Usage = agentcore.TokenUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					out.Content += c.Text
					out.Blocks = append(out.Blocks, agentcore.ContentBlock{
						Kind: agentcore.BlockKindText,
						Text: c.Text,
					})
				}
			}
		case "function_call":
			out.ToolCalls = append(out.ToolCalls, agentcore.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
			out.Blocks = append(out.Blocks, agentcore.ContentBlock{
				Kind:       agentcore.BlockKindToolCall,
				ToolCallID: item.CallID,
				Name:       item.Name,
				Arguments:  item.Arguments,
			})
		}
	}

	out.Structured = agentcore.ExtractStructuredContent(out.Content, req.ResponseFormat)
	return out, nil
}
