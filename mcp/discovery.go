package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

type Icon struct {
	Src      string   `json:"src"`
	MIMEType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
}

type Annotations struct {
	Audience     []string `json:"audience,omitempty"`
	Priority     float64  `json:"priority,omitempty"`
	LastModified string   `json:"lastModified,omitempty"`
}

type Resource struct {
	URI         string       `json:"uri"`
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	MIMEType    string       `json:"mimeType,omitempty"`
	Size        int64        `json:"size,omitempty"`
	Icons       []Icon       `json:"icons,omitempty"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string       `json:"uriTemplate"`
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	MIMEType    string       `json:"mimeType,omitempty"`
	Icons       []Icon       `json:"icons,omitempty"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

type ReadResourceResult struct {
	Contents []EmbeddedResource `json:"contents"`
}

type Prompt struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Icons       []Icon           `json:"icons,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

type PromptMessage struct {
	Role    string        `json:"role"`
	Content PromptContent `json:"content"`
}

type PromptContent struct {
	Type        string           `json:"type"`
	Text        string           `json:"text,omitempty"`
	Data        string           `json:"data,omitempty"`
	MIMEType    string           `json:"mimeType,omitempty"`
	Resource    *EmbeddedResource `json:"resource,omitempty"`
	Annotations *Annotations     `json:"annotations,omitempty"`
}

type resourceListResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type resourceTemplateListResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string             `json:"nextCursor,omitempty"`
}

type promptListResult struct {
	Prompts    []Prompt `json:"prompts"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

type discoveryRPC func(ctx context.Context, method string, params any, out any) error

type DiscoveryClient interface {
	ListResources(ctx context.Context) ([]Resource, error)
	ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error)
	ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error)
	SubscribeResource(ctx context.Context, uri string) error
	UnsubscribeResource(ctx context.Context, uri string) error
	ListPrompts(ctx context.Context) ([]Prompt, error)
	GetPrompt(ctx context.Context, name string, arguments map[string]any) (*PromptResult, error)
}

type DiscoveryToolConfig struct {
	Name             string
	ToolPrefix       string
	IncludeResources bool
	IncludePrompts   bool
}

type DiscoveryExtension struct {
	name  string
	tools []*agentcore.Tool
}

type DiscoveryConfig struct {
	ResourceUpdatedHandler      func(context.Context, string, *ReadResourceResult)
	ResourcesListChangedHandler func(context.Context)
	PromptsListChangedHandler   func(context.Context)
	AsyncRefreshErrorHandler    func(context.Context, error)
}

type discoveryState struct {
	mu sync.RWMutex

	cfg DiscoveryConfig

	resources              []Resource
	resourcesLoaded        bool
	resourceTemplates      []ResourceTemplate
	resourceTemplatesLoaded bool
	prompts                []Prompt
	promptsLoaded          bool
	resourceContents       map[string]*ReadResourceResult
	promptResults          map[string]*PromptResult
	subscribedResources    map[string]struct{}
}

func NewDiscoveryExtension(client DiscoveryClient, cfg DiscoveryToolConfig) (*DiscoveryExtension, error) {
	if client == nil {
		return nil, fmt.Errorf("mcp: discovery client is required")
	}
	var tools []*agentcore.Tool
	if cfg.IncludeResources {
		tools = append(tools, discoveryResourceTools(client, cfg.ToolPrefix)...)
	}
	if cfg.IncludePrompts {
		tools = append(tools, discoveryPromptTools(client, cfg.ToolPrefix)...)
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("mcp: no discovery tools enabled")
	}
	name := cfg.Name
	if name == "" {
		name = "mcp-discovery"
	}
	return &DiscoveryExtension{name: name, tools: tools}, nil
}

func (e *DiscoveryExtension) Name() string                                           { return e.name }
func (e *DiscoveryExtension) Init(ctx context.Context, agent *agentcore.Agent) error { return nil }
func (e *DiscoveryExtension) Dispose() error                                          { return nil }
func (e *DiscoveryExtension) Tools() []*agentcore.Tool                                { return e.tools }

func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	return listResources(ctx, c.discovery, c.invokeDiscovery)
}

func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	return readResource(ctx, c.discovery, c.invokeDiscovery, uri)
}

func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	return listResourceTemplates(ctx, c.discovery, c.invokeDiscovery)
}

func (c *Client) SubscribeResource(ctx context.Context, uri string) error {
	if !c.SupportsResourceSubscribe() {
		return fmt.Errorf("mcp: server does not advertise resources.subscribe")
	}
	return subscribeResource(ctx, c.discovery, c.invokeDiscovery, uri)
}

func (c *Client) UnsubscribeResource(ctx context.Context, uri string) error {
	if !c.SupportsResourceSubscribe() {
		return fmt.Errorf("mcp: server does not advertise resources.subscribe")
	}
	return unsubscribeResource(ctx, c.discovery, c.invokeDiscovery, uri)
}

func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	return listPrompts(ctx, c.discovery, c.invokeDiscovery)
}

func (c *Client) GetPrompt(ctx context.Context, name string, arguments map[string]any) (*PromptResult, error) {
	return getPrompt(ctx, c.discovery, c.invokeDiscovery, name, arguments)
}

func (c *HTTPClient) ListResources(ctx context.Context) ([]Resource, error) {
	return listResources(ctx, c.discovery, c.invokeDiscovery)
}

func (c *HTTPClient) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	return readResource(ctx, c.discovery, c.invokeDiscovery, uri)
}

func (c *HTTPClient) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	return listResourceTemplates(ctx, c.discovery, c.invokeDiscovery)
}

func (c *HTTPClient) SubscribeResource(ctx context.Context, uri string) error {
	if !c.SupportsResourceSubscribe() {
		return fmt.Errorf("mcp: server does not advertise resources.subscribe")
	}
	return subscribeResource(ctx, c.discovery, c.invokeDiscovery, uri)
}

func (c *HTTPClient) UnsubscribeResource(ctx context.Context, uri string) error {
	if !c.SupportsResourceSubscribe() {
		return fmt.Errorf("mcp: server does not advertise resources.subscribe")
	}
	return unsubscribeResource(ctx, c.discovery, c.invokeDiscovery, uri)
}

func (c *HTTPClient) ListPrompts(ctx context.Context) ([]Prompt, error) {
	return listPrompts(ctx, c.discovery, c.invokeDiscovery)
}

func (c *HTTPClient) GetPrompt(ctx context.Context, name string, arguments map[string]any) (*PromptResult, error) {
	return getPrompt(ctx, c.discovery, c.invokeDiscovery, name, arguments)
}

func (c *Client) invokeDiscovery(ctx context.Context, method string, params any, out any) error {
	return c.call(ctx, method, params, out)
}

func (c *HTTPClient) invokeDiscovery(ctx context.Context, method string, params any, out any) error {
	_, err := c.call(ctx, method, params, out)
	return err
}

func listResources(ctx context.Context, state *discoveryState, rpc discoveryRPC) ([]Resource, error) {
	if resources, ok := state.cachedResources(); ok {
		return resources, nil
	}
	var out []Resource
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result resourceListResult
		if err := rpc(ctx, "resources/list", params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Resources...)
		if result.NextCursor == "" {
			state.storeResources(out)
			return out, nil
		}
		cursor = result.NextCursor
	}
}

func readResource(ctx context.Context, state *discoveryState, rpc discoveryRPC, uri string) (*ReadResourceResult, error) {
	if result, ok := state.cachedResource(uri); ok {
		return result, nil
	}
	var result ReadResourceResult
	if err := rpc(ctx, "resources/read", map[string]any{"uri": uri}, &result); err != nil {
		return nil, err
	}
	state.storeResource(uri, &result)
	return &result, nil
}

func listResourceTemplates(ctx context.Context, state *discoveryState, rpc discoveryRPC) ([]ResourceTemplate, error) {
	if templates, ok := state.cachedResourceTemplates(); ok {
		return templates, nil
	}
	var out []ResourceTemplate
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result resourceTemplateListResult
		if err := rpc(ctx, "resources/templates/list", params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.ResourceTemplates...)
		if result.NextCursor == "" {
			state.storeResourceTemplates(out)
			return out, nil
		}
		cursor = result.NextCursor
	}
}

func listPrompts(ctx context.Context, state *discoveryState, rpc discoveryRPC) ([]Prompt, error) {
	if prompts, ok := state.cachedPrompts(); ok {
		return prompts, nil
	}
	var out []Prompt
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result promptListResult
		if err := rpc(ctx, "prompts/list", params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Prompts...)
		if result.NextCursor == "" {
			state.storePrompts(out)
			return out, nil
		}
		cursor = result.NextCursor
	}
}

func getPrompt(ctx context.Context, state *discoveryState, rpc discoveryRPC, name string, arguments map[string]any) (*PromptResult, error) {
	key, err := promptCacheKey(name, arguments)
	if err != nil {
		return nil, err
	}
	if result, ok := state.cachedPrompt(key); ok {
		return result, nil
	}
	params := map[string]any{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	var result PromptResult
	if err := rpc(ctx, "prompts/get", params, &result); err != nil {
		return nil, err
	}
	state.storePrompt(key, &result)
	return &result, nil
}

func subscribeResource(ctx context.Context, state *discoveryState, rpc discoveryRPC, uri string) error {
	if state == nil {
		return fmt.Errorf("mcp: discovery state is required")
	}
	state.markSubscribed(uri, true)
	var result map[string]any
	if err := rpc(ctx, "resources/subscribe", map[string]any{"uri": uri}, &result); err != nil {
		state.markSubscribed(uri, false)
		return err
	}
	return nil
}

func unsubscribeResource(ctx context.Context, state *discoveryState, rpc discoveryRPC, uri string) error {
	var result map[string]any
	if err := rpc(ctx, "resources/unsubscribe", map[string]any{"uri": uri}, &result); err != nil {
		return err
	}
	state.markSubscribed(uri, false)
	return nil
}

func (r PromptResult) AgentMessages() []agentcore.Message {
	out := make([]agentcore.Message, 0, len(r.Messages))
	for _, msg := range r.Messages {
		out = append(out, msg.AgentMessage())
	}
	return out
}

func (m PromptMessage) AgentMessage() agentcore.Message {
	out := agentcore.Message{}
	switch m.Role {
	case "assistant":
		out.Role = agentcore.RoleAssistant
	default:
		out.Role = agentcore.RoleUser
	}
	switch m.Content.Type {
	case "text":
		out.Content = m.Content.Text
	case "image":
		out = out.AppendImageURLBlock(dataURL(m.Content.MIMEType, m.Content.Data))
	case "resource":
		if m.Content.Resource != nil {
			if m.Content.Resource.Text != "" {
				out.Content = m.Content.Resource.Text
			} else {
				out.Content = m.Content.Resource.URI
			}
		}
	default:
		if m.Content.Text != "" {
			out.Content = m.Content.Text
		} else if m.Content.Resource != nil && m.Content.Resource.Text != "" {
			out.Content = m.Content.Resource.Text
		}
	}
	return out
}

func discoveryResourceTools(client DiscoveryClient, prefix string) []*agentcore.Tool {
	return []*agentcore.Tool{
		{
			Name:        qualifyToolName(prefix, "resources.list"),
			Description: "List available MCP resources.",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				resources, err := client.ListResources(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"resources": resources}, nil
			},
		},
		{
			Name:        qualifyToolName(prefix, "resources.templates.list"),
			Description: "List available MCP resource templates.",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				templates, err := client.ListResourceTemplates(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"resource_templates": templates}, nil
			},
		},
		{
			Name:        qualifyToolName(prefix, "resources.subscribe"),
			Description: "Subscribe to MCP resource updates by URI.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"uri": map[string]any{"type": "string"},
				},
				"required":             []string{"uri"},
				"additionalProperties": false,
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				var in struct {
					URI string `json:"uri"`
				}
				if err := decodeObjectArgs(args, &in, "mcp decode subscribe resource args"); err != nil {
					return nil, err
				}
				if err := client.SubscribeResource(ctx, in.URI); err != nil {
					return nil, err
				}
				return map[string]any{"subscribed": true, "uri": in.URI}, nil
			},
		},
		{
			Name:        qualifyToolName(prefix, "resources.unsubscribe"),
			Description: "Unsubscribe from MCP resource updates by URI.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"uri": map[string]any{"type": "string"},
				},
				"required":             []string{"uri"},
				"additionalProperties": false,
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				var in struct {
					URI string `json:"uri"`
				}
				if err := decodeObjectArgs(args, &in, "mcp decode unsubscribe resource args"); err != nil {
					return nil, err
				}
				if err := client.UnsubscribeResource(ctx, in.URI); err != nil {
					return nil, err
				}
				return map[string]any{"subscribed": false, "uri": in.URI}, nil
			},
		},
		{
			Name:        qualifyToolName(prefix, "resources.read"),
			Description: "Read one MCP resource by URI.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"uri": map[string]any{"type": "string"},
				},
				"required":             []string{"uri"},
				"additionalProperties": false,
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				var in struct {
					URI string `json:"uri"`
				}
				if err := decodeObjectArgs(args, &in, "mcp decode read resource args"); err != nil {
					return nil, err
				}
				return client.ReadResource(ctx, in.URI)
			},
		},
	}
}

func discoveryPromptTools(client DiscoveryClient, prefix string) []*agentcore.Tool {
	return []*agentcore.Tool{
		{
			Name:        qualifyToolName(prefix, "prompts.list"),
			Description: "List available MCP prompts.",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				prompts, err := client.ListPrompts(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"prompts": prompts}, nil
			},
		},
		{
			Name:        qualifyToolName(prefix, "prompts.get"),
			Description: "Get one MCP prompt with optional arguments.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
					"arguments": map[string]any{
						"type": "object",
					},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				var in struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				}
				if err := decodeObjectArgs(args, &in, "mcp decode get prompt args"); err != nil {
					return nil, err
				}
				return client.GetPrompt(ctx, in.Name, in.Arguments)
			},
		},
	}
}

func dataURL(mimeType, data string) string {
	if data == "" {
		return ""
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + data
}

func (c *Client) handleDiscoveryNotification(ctx context.Context, method string, params json.RawMessage) error {
	return handleDiscoveryNotification(ctx, c.discovery, method, params, c.refreshResourceAsync, c.refreshResourcesListAsync, c.refreshPromptsListAsync)
}

func (c *HTTPClient) handleDiscoveryNotification(ctx context.Context, method string, params json.RawMessage) error {
	return handleDiscoveryNotification(ctx, c.discovery, method, params, c.refreshResourceAsync, c.refreshResourcesListAsync, c.refreshPromptsListAsync)
}

func handleDiscoveryNotification(
	ctx context.Context,
	state *discoveryState,
	method string,
	params json.RawMessage,
	refreshResource func(string),
	refreshResourcesList func(),
	refreshPromptsList func(),
) error {
	switch method {
	case "notifications/resources/updated":
		var in struct {
			URI string `json:"uri"`
		}
		if len(params) > 0 && string(params) != "null" {
			if err := json.Unmarshal(params, &in); err != nil {
				return fmt.Errorf("mcp decode resources updated params: %w", err)
			}
		}
		if in.URI == "" {
			return nil
		}
		if subscribed := state.invalidateResource(in.URI); subscribed {
			refreshResource(in.URI)
		} else {
			state.onResourceUpdated(ctx, in.URI, nil)
		}
	case "notifications/resources/list_changed":
		state.invalidateResourcesList()
		state.onResourcesListChanged(ctx)
		refreshResourcesList()
	case "notifications/prompts/list_changed":
		state.invalidatePromptsList()
		state.onPromptsListChanged(ctx)
		refreshPromptsList()
	}
	return nil
}

func (c *Client) refreshResourceAsync(uri string) {
	go func() {
		result, err := readResource(context.Background(), c.discovery, c.invokeDiscovery, uri)
		if err != nil {
			c.discovery.onAsyncRefreshError(context.Background(), err)
			return
		}
		c.discovery.onResourceUpdated(context.Background(), uri, result)
	}()
}

func (c *Client) refreshResourcesListAsync() {
	go func() {
		if _, err := listResources(context.Background(), c.discovery, c.invokeDiscovery); err != nil {
			c.discovery.onAsyncRefreshError(context.Background(), err)
			return
		}
		if _, err := listResourceTemplates(context.Background(), c.discovery, c.invokeDiscovery); err != nil {
			c.discovery.onAsyncRefreshError(context.Background(), err)
		}
	}()
}

func (c *Client) refreshPromptsListAsync() {
	go func() {
		if _, err := listPrompts(context.Background(), c.discovery, c.invokeDiscovery); err != nil {
			c.discovery.onAsyncRefreshError(context.Background(), err)
		}
	}()
}

func (c *HTTPClient) refreshResourceAsync(uri string) {
	go func() {
		result, err := readResource(c.bgCtx, c.discovery, c.invokeDiscovery, uri)
		if err != nil {
			c.discovery.onAsyncRefreshError(c.bgCtx, err)
			return
		}
		c.discovery.onResourceUpdated(c.bgCtx, uri, result)
	}()
}

func (c *HTTPClient) refreshResourcesListAsync() {
	go func() {
		if _, err := listResources(c.bgCtx, c.discovery, c.invokeDiscovery); err != nil {
			c.discovery.onAsyncRefreshError(c.bgCtx, err)
			return
		}
		if _, err := listResourceTemplates(c.bgCtx, c.discovery, c.invokeDiscovery); err != nil {
			c.discovery.onAsyncRefreshError(c.bgCtx, err)
		}
	}()
}

func (c *HTTPClient) refreshPromptsListAsync() {
	go func() {
		if _, err := listPrompts(c.bgCtx, c.discovery, c.invokeDiscovery); err != nil {
			c.discovery.onAsyncRefreshError(c.bgCtx, err)
		}
	}()
}

func newDiscoveryState(cfg DiscoveryConfig) *discoveryState {
	return &discoveryState{
		cfg:                 cfg,
		resourceContents:    make(map[string]*ReadResourceResult),
		promptResults:       make(map[string]*PromptResult),
		subscribedResources: make(map[string]struct{}),
	}
}

func (s *discoveryState) cachedResources() ([]Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.resourcesLoaded {
		return nil, false
	}
	return append([]Resource(nil), s.resources...), true
}

func (s *discoveryState) storeResources(resources []Resource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources = append([]Resource(nil), resources...)
	s.resourcesLoaded = true
}

func (s *discoveryState) cachedResource(uri string) (*ReadResourceResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res, ok := s.resourceContents[uri]
	if !ok {
		return nil, false
	}
	clone := *res
	clone.Contents = append([]EmbeddedResource(nil), res.Contents...)
	return &clone, true
}

func (s *discoveryState) storeResource(uri string, result *ReadResourceResult) {
	if result == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *result
	clone.Contents = append([]EmbeddedResource(nil), result.Contents...)
	s.resourceContents[uri] = &clone
}

func (s *discoveryState) cachedResourceTemplates() ([]ResourceTemplate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.resourceTemplatesLoaded {
		return nil, false
	}
	return append([]ResourceTemplate(nil), s.resourceTemplates...), true
}

func (s *discoveryState) storeResourceTemplates(templates []ResourceTemplate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resourceTemplates = append([]ResourceTemplate(nil), templates...)
	s.resourceTemplatesLoaded = true
}

func (s *discoveryState) cachedPrompts() ([]Prompt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.promptsLoaded {
		return nil, false
	}
	return append([]Prompt(nil), s.prompts...), true
}

func (s *discoveryState) storePrompts(prompts []Prompt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append([]Prompt(nil), prompts...)
	s.promptsLoaded = true
}

func (s *discoveryState) cachedPrompt(key string) (*PromptResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res, ok := s.promptResults[key]
	if !ok {
		return nil, false
	}
	clone := *res
	clone.Messages = append([]PromptMessage(nil), res.Messages...)
	return &clone, true
}

func (s *discoveryState) storePrompt(key string, result *PromptResult) {
	if result == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *result
	clone.Messages = append([]PromptMessage(nil), result.Messages...)
	s.promptResults[key] = &clone
}

func (s *discoveryState) markSubscribed(uri string, subscribed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if subscribed {
		s.subscribedResources[uri] = struct{}{}
		return
	}
	delete(s.subscribedResources, uri)
}

func (s *discoveryState) invalidateResource(uri string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.resourceContents, uri)
	_, subscribed := s.subscribedResources[uri]
	return subscribed
}

func (s *discoveryState) invalidateResourcesList() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources = nil
	s.resourcesLoaded = false
	s.resourceTemplates = nil
	s.resourceTemplatesLoaded = false
}

func (s *discoveryState) invalidatePromptsList() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = nil
	s.promptsLoaded = false
	s.promptResults = make(map[string]*PromptResult)
}

func (s *discoveryState) onResourceUpdated(ctx context.Context, uri string, result *ReadResourceResult) {
	if s.cfg.ResourceUpdatedHandler != nil {
		s.cfg.ResourceUpdatedHandler(ctx, uri, result)
	}
}

func (s *discoveryState) onResourcesListChanged(ctx context.Context) {
	if s.cfg.ResourcesListChangedHandler != nil {
		s.cfg.ResourcesListChangedHandler(ctx)
	}
}

func (s *discoveryState) onPromptsListChanged(ctx context.Context) {
	if s.cfg.PromptsListChangedHandler != nil {
		s.cfg.PromptsListChangedHandler(ctx)
	}
}

func (s *discoveryState) onAsyncRefreshError(ctx context.Context, err error) {
	if err != nil && s.cfg.AsyncRefreshErrorHandler != nil {
		s.cfg.AsyncRefreshErrorHandler(ctx, err)
	}
}

func promptCacheKey(name string, arguments map[string]any) (string, error) {
	if arguments == nil {
		return name, nil
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("mcp encode prompt cache key: %w", err)
	}
	return name + "\x00" + string(data), nil
}

var _ agentcore.Extension = (*DiscoveryExtension)(nil)
var _ agentcore.ToolProvider = (*DiscoveryExtension)(nil)
