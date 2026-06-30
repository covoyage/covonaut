package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type ServerCapabilities struct {
	Tools     ToolCapabilities     `json:"tools,omitempty"`
	Resources ResourceCapabilities `json:"resources,omitempty"`
	Prompts   PromptCapabilities   `json:"prompts,omitempty"`
}

type ToolCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourceCapabilities struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptCapabilities struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type capabilityState struct {
	mu           sync.RWMutex
	capabilities ServerCapabilities
	hooks        []func(context.Context, ServerCapabilities)
}

func newCapabilityState() *capabilityState {
	return &capabilityState{}
}

func (s *capabilityState) get() ServerCapabilities {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.capabilities
}

func (s *capabilityState) set(ctx context.Context, capabilities ServerCapabilities) {
	s.mu.Lock()
	s.capabilities = capabilities
	hooks := append([]func(context.Context, ServerCapabilities){}, s.hooks...)
	s.mu.Unlock()
	for _, hook := range hooks {
		hook(ctx, capabilities)
	}
}

func (s *capabilityState) addHook(h func(context.Context, ServerCapabilities)) {
	if h == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, h)
}

func (c *Client) Capabilities() ServerCapabilities {
	if c.capState == nil {
		return ServerCapabilities{}
	}
	return c.capState.get()
}

func (c *HTTPClient) Capabilities() ServerCapabilities {
	if c.capState == nil {
		return ServerCapabilities{}
	}
	return c.capState.get()
}

func (c *Client) AddCapabilityHook(h func(context.Context, ServerCapabilities)) {
	if c.capState != nil {
		c.capState.addHook(h)
	}
}

func (c *HTTPClient) AddCapabilityHook(h func(context.Context, ServerCapabilities)) {
	if c.capState != nil {
		c.capState.addHook(h)
	}
}

func (c *Client) SupportsToolListChanged() bool {
	return c.Capabilities().Tools.ListChanged
}

func (c *HTTPClient) SupportsToolListChanged() bool {
	return c.Capabilities().Tools.ListChanged
}

func (c *Client) SupportsResourceSubscribe() bool {
	return c.Capabilities().Resources.Subscribe
}

func (c *HTTPClient) SupportsResourceSubscribe() bool {
	return c.Capabilities().Resources.Subscribe
}

func (c *Client) SupportsResourceListChanged() bool {
	return c.Capabilities().Resources.ListChanged
}

func (c *HTTPClient) SupportsResourceListChanged() bool {
	return c.Capabilities().Resources.ListChanged
}

func (c *Client) SupportsPromptListChanged() bool {
	return c.Capabilities().Prompts.ListChanged
}

func (c *HTTPClient) SupportsPromptListChanged() bool {
	return c.Capabilities().Prompts.ListChanged
}

func decodeCapabilities(raw json.RawMessage) (ServerCapabilities, error) {
	if len(raw) == 0 {
		return ServerCapabilities{}, nil
	}
	var caps ServerCapabilities
	if err := json.Unmarshal(raw, &caps); err != nil {
		return ServerCapabilities{}, fmt.Errorf("mcp decode capabilities: %w", err)
	}
	return caps, nil
}
