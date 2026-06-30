package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/pkg/util"
)

const protocolVersion = "2025-11-25"
const stderrContextMaxBytes = 4 * 1024

type StdioConfig struct {
	Name                string
	Command             string
	Args                []string
	Env                 []string
	Dir                 string
	ToolPrefix          string
	ClientName          string
	ClientVersion       string
	RequestTimeout      time.Duration
	NotificationHandler func(context.Context, string, json.RawMessage) error
	RequestHandler      func(context.Context, string, json.RawMessage) (any, error)
	ErrorHandler        func(context.Context, error)
	Discovery           DiscoveryConfig
}

type Client struct {
	cfg    StdioConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan rpcResponse
	closed  bool
	closeCh chan struct{}
	readErr error

	nextID            atomic.Int64
	errBuf            bytes.Buffer
	discovery         *discoveryState
	capState          *capabilityState
	eventSink         runtimeEventSink
	hooksMu           sync.RWMutex
	notificationHooks []func(context.Context, string, json.RawMessage) error

	reconnectMu     sync.Mutex
	reconnectBackoff time.Duration
}

const (
	maxReconnectBackoff = 30 * time.Second
	initialReconnectDelay = 500 * time.Millisecond
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

type toolListResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type ToolResult struct {
	Content           []ToolResultContent `json:"content,omitempty"`
	StructuredContent any                 `json:"structuredContent,omitempty"`
	IsError           bool                `json:"isError,omitempty"`
}

type ToolResultContent struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	Data     string            `json:"data,omitempty"`
	MIMEType string            `json:"mimeType,omitempty"`
	URI      string            `json:"uri,omitempty"`
	Name     string            `json:"name,omitempty"`
	Resource *EmbeddedResource `json:"resource,omitempty"`
}

type EmbeddedResource struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type toolBridge interface {
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error)
}

type StdioExtension struct {
	name              string
	cfg               StdioConfig
	client            *Client
	tools             []*agentcore.Tool
	agent             *agentcore.Agent
	toolNames         []string
	refreshMu         sync.Mutex
	refreshScheduleMu sync.Mutex
	refreshInFlight   bool
	refreshPending    bool
}

func NewStdioExtension(ctx context.Context, cfg StdioConfig) (*StdioExtension, error) {
	client, err := NewStdioClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	tools, err := client.AgentTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	name := cfg.Name
	if name == "" {
		name = "mcp"
	}
	return &StdioExtension{
		name:   name,
		cfg:    cfg,
		client: client,
		tools:  tools,
	}, nil
}

func (e *StdioExtension) Name() string { return e.name }
func (e *StdioExtension) Init(ctx context.Context, agent *agentcore.Agent) error {
	e.agent = agent
	e.toolNames = toolNames(e.tools)
	if e.client != nil {
		e.client.SetEventSink(agent.EmitEvent)
		e.client.AddNotificationHook(func(ctx context.Context, method string, params json.RawMessage) error {
			if method != "notifications/tools/list_changed" || !e.client.SupportsToolListChanged() {
				return nil
			}
			e.scheduleRefresh()
			return nil
		})
		e.client.AddCapabilityHook(func(ctx context.Context, caps ServerCapabilities) {
			e.emitCapabilitiesEvent(caps)
		})
		e.emitCapabilitiesEvent(e.client.Capabilities())
	}
	return nil
}
func (e *StdioExtension) Client() *Client { return e.client }
func (e *StdioExtension) Dispose() error {
	if e.client == nil {
		return nil
	}
	return e.client.Close()
}
func (e *StdioExtension) Tools() []*agentcore.Tool {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	return append([]*agentcore.Tool(nil), e.tools...)
}
func (e *StdioExtension) SnapshotEvents() []agentcore.Event {
	if e.client == nil {
		return nil
	}
	return []agentcore.Event{CapabilitiesUpdatedEvent{
		At:           time.Now(),
		Extension:    e.name,
		Transport:    "stdio",
		Capabilities: e.client.Capabilities(),
	}}
}

func NewStdioClient(ctx context.Context, cfg StdioConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("mcp: command is required")
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	if len(cfg.Env) > 0 {
		cmd.Env = append(cmd.Environ(), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start server: %w", err)
	}

	c := &Client{
		cfg:       cfg,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		pending:   make(map[string]chan rpcResponse),
		closeCh:   make(chan struct{}),
		discovery: newDiscoveryState(cfg.Discovery),
		capState:  newCapabilityState(),
	}
	go c.readLoop()
	go c.captureStderr()
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    util.DefaultString(c.cfg.ClientName, "covonaut"),
			"version": util.DefaultString(c.cfg.ClientVersion, "0.1.0"),
		},
	}
	var result struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	caps, err := decodeCapabilities(result.Capabilities)
	if err != nil {
		return err
	}
	c.capState.set(ctx, caps)
	return c.notify("notifications/initialized", nil)
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var out []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result toolListResult
		if err := c.call(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Tools...)
		if result.NextCursor == "" {
			return out, nil
		}
		cursor = result.NextCursor
	}
}

func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	var result ToolResult
	if err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AgentTools(ctx context.Context) ([]*agentcore.Tool, error) {
	return agentToolsFor(ctx, c.cfg.ToolPrefix, c)
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.closeCh)
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.mu.Unlock()

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	done := make(chan error, 1)
	go func() {
		done <- c.cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "signal: killed") {
			return err
		}
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
	return nil
}

func (c *Client) qualifyToolName(name string) string {
	if c.cfg.ToolPrefix == "" {
		return name
	}
	return c.cfg.ToolPrefix + name
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.cfg.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.RequestTimeout)
		defer cancel()
	}
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errClientClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	if err := c.writeMessage(msg); err != nil {
		// Connection may be dead — attempt reconnection
		if errors.Is(err, errClientClosed) && c.tryReconnect(ctx) {
			return c.call(ctx, method, params, out)
		}
		return err
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			err := c.connectionError()
			if c.tryReconnect(ctx) {
				return c.call(ctx, method, params, out)
			}
			return err
		}
		if resp.Error != nil {
			return fmt.Errorf("mcp %s: %s", method, resp.Error.Message)
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("mcp %s decode result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closeCh:
		err := c.connectionError()
		if c.tryReconnect(ctx) {
			return c.call(ctx, method, params, out)
		}
		return err
	}
}

func (c *Client) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.writeMessage(msg)
}

func (c *Client) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp marshal request: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return errClientClosed
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("mcp write request: %w", err)
	}
	return nil
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		methodRaw, hasMethod := raw["method"]
		idRaw, ok := raw["id"]
		if !ok {
			if hasMethod {
				if err := c.handleServerMessage(c.stdoutContext(), line, methodRaw, nil); err != nil {
					c.reportAsyncError("notification", "server_message", err, true)
				}
			}
			continue
		}
		if hasMethod {
			if err := c.handleServerMessage(c.stdoutContext(), line, methodRaw, idRaw); err != nil {
				c.reportAsyncError("request", "server_message", err, true)
			}
			continue
		}
		var id any
		if err := json.Unmarshal(idRaw, &id); err != nil {
			continue
		}
		key := fmt.Sprint(id)
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[key]
		if ch != nil {
			select {
			case ch <- resp:
			default:
				// Channel full — duplicate or stale response. Clean up to avoid
				// further drops and report for observability.
				delete(c.pending, key)
				c.mu.Unlock()
				c.reportAsyncError("read_loop", "duplicate_response",
					fmt.Errorf("dropped response for id %q: pending channel full", key), true)
				continue
			}
		}
		c.mu.Unlock()
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	c.readErr = err
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		c.reportAsyncError("read_loop", "connection_closed", c.connectionError(), false)
	}
}

func (c *Client) captureStderr() {
	scanner := bufio.NewScanner(c.stderr)
	scanner.Buffer(make([]byte, 16*1024), 1024*1024)
	for scanner.Scan() {
		c.mu.Lock()
		c.appendStderrLine(scanner.Text())
		c.mu.Unlock()
	}
}

func (c *Client) appendStderrLine(line string) {
	if c.errBuf.Len() > 0 {
		c.errBuf.WriteByte('\n')
	}
	c.errBuf.WriteString(line)
	if c.errBuf.Len() <= stderrContextMaxBytes {
		return
	}
	data := append([]byte(nil), c.errBuf.Bytes()[c.errBuf.Len()-stderrContextMaxBytes:]...)
	c.errBuf.Reset()
	c.errBuf.Write(data)
}

func (c *Client) connectionError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil && c.readErr != io.EOF {
		return fmt.Errorf("mcp connection closed: %w", c.readErr)
	}
	return errClientClosed
}

func (c *Client) tryReconnect(ctx context.Context) bool {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	// If we already have a working connection (readLoop is running), don't reconnect
	if !c.isConnectionDeadLocked() {
		c.mu.Unlock()
		return true // connection is fine
	}
	c.mu.Unlock()

	backoff := c.reconnectBackoff
	if backoff == 0 {
		backoff = initialReconnectDelay
	}
	c.reconnectBackoff = backoff * 2
	if c.reconnectBackoff > maxReconnectBackoff {
		c.reconnectBackoff = maxReconnectBackoff
	}

	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}

	// Spawn new subprocess
	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	if c.cfg.Dir != "" {
		cmd.Dir = c.cfg.Dir
	}
	if len(c.cfg.Env) > 0 {
		cmd.Env = append(cmd.Environ(), c.cfg.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return false
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return false
	}
	if err := cmd.Start(); err != nil {
		return false
	}

	// Replace pipes atomically
	c.mu.Lock()
	oldStdin := c.stdin
	oldStdout := c.stdout
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr
	c.readErr = nil
	c.errBuf.Reset()
	// Reset pending map — old requests are cancelled
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.mu.Unlock()

	// Close old pipes
	if oldStdin != nil {
		_ = oldStdin.Close()
	}
	if oldStdout != nil {
		_ = oldStdout.Close()
	}

	// Start new readers
	go c.readLoop()
	go c.captureStderr()

	// Re-initialize MCP protocol
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return false
	}

	c.reconnectBackoff = initialReconnectDelay
	return true
}

func (c *Client) isConnectionDeadLocked() bool {
	return c.readErr != nil
}

func (c *Client) stdoutContext() context.Context {
	return context.Background()
}

func (c *Client) handleServerMessage(ctx context.Context, line string, methodRaw json.RawMessage, idRaw json.RawMessage) error {
	var method string
	if err := json.Unmarshal(methodRaw, &method); err != nil {
		return err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return err
	}
	params := envelope["params"]
	if len(params) == 0 {
		params = json.RawMessage(`null`)
	}
	if len(idRaw) == 0 {
		if err := c.handleDiscoveryNotification(ctx, method, params); err != nil {
			c.reportAsyncError("notification", method, err, true)
		}
		for _, hook := range c.notificationHookSnapshot() {
			if err := hook(ctx, method, params); err != nil {
				c.reportAsyncError("notification", method, err, true)
			}
		}
		if c.cfg.NotificationHandler != nil {
			if err := c.cfg.NotificationHandler(ctx, method, params); err != nil {
				c.reportAsyncError("notification", method, err, true)
			}
		}
		return nil
	}
	var reqID any
	if err := json.Unmarshal(idRaw, &reqID); err != nil {
		return err
	}
	var result any
	var handlerErr error
	if c.cfg.RequestHandler != nil {
		result, handlerErr = c.cfg.RequestHandler(ctx, method, params)
	} else {
		handlerErr = fmt.Errorf("no request handler configured")
	}
	return c.respondToServerRequest(ctx, reqID, result, handlerErr)
}

func (c *Client) respondToServerRequest(ctx context.Context, id any, result any, handlerErr error) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
	}
	if handlerErr != nil {
		msg["error"] = map[string]any{
			"code":    -32601,
			"message": handlerErr.Error(),
		}
	} else {
		msg["result"] = result
	}
	return c.writeMessage(msg)
}

func (c *Client) reportAsyncError(operation, reason string, err error, recoverable bool) {
	if err == nil {
		return
	}
	c.emitTransportError(operation, reason, err, recoverable)
	if c.cfg.ErrorHandler != nil {
		c.cfg.ErrorHandler(context.Background(), err)
	}
}

func (c *Client) AddNotificationHook(h func(context.Context, string, json.RawMessage) error) {
	if h == nil {
		return
	}
	c.hooksMu.Lock()
	defer c.hooksMu.Unlock()
	c.notificationHooks = append(c.notificationHooks, h)
}

func (c *Client) notificationHookSnapshot() []func(context.Context, string, json.RawMessage) error {
	c.hooksMu.RLock()
	defer c.hooksMu.RUnlock()
	return append([]func(context.Context, string, json.RawMessage) error(nil), c.notificationHooks...)
}

func (c *Client) SetEventSink(emit func(agentcore.Event)) {
	c.eventSink.Set(emit)
}

func (c *Client) emitRuntimeEvent(event agentcore.Event) {
	c.eventSink.Emit(event)
}

func (c *Client) emitTransportError(operation, reason string, err error, recoverable bool) {
	c.emitRuntimeEvent(TransportErrorEvent{
		At:          time.Now(),
		Extension:   c.extensionName(),
		Transport:   "stdio",
		Operation:   operation,
		Reason:      reason,
		Message:     util.ErrorString(err),
		Recoverable: recoverable,
	})
}

func (c *Client) extensionName() string {
	return util.DefaultString(c.cfg.Name, "mcp")
}

func (e *StdioExtension) emitCapabilitiesEvent(caps ServerCapabilities) {
	if e.agent == nil {
		return
	}
	e.agent.EmitEvent(CapabilitiesUpdatedEvent{
		At:           time.Now(),
		Extension:    e.name,
		Transport:    "stdio",
		Capabilities: caps,
	})
}

func decodeArguments(args json.RawMessage) (map[string]any, error) {
	if len(args) == 0 || string(args) == "null" {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(args, &decoded); err != nil {
		return nil, fmt.Errorf("mcp decode arguments: %w", err)
	}
	if decoded == nil {
		return map[string]any{}, nil
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp tool arguments must be a JSON object")
	}
	return m, nil
}

func formatToolResult(result *ToolResult) string {
	if result == nil {
		return ""
	}
	text := strings.TrimSpace(formatToolContent(result.Content))
	if text == "" && result.StructuredContent != nil {
		data, _ := json.Marshal(result.StructuredContent)
		text = string(data)
	}
	if result.IsError {
		text = strings.TrimSpace(text)
		if text == "" {
			text = "MCP tool returned an error"
		}
		if !strings.HasPrefix(strings.ToLower(text), "error:") {
			text = "Error: " + text
		}
	}
	return text
}

func formatToolContent(items []ToolResultContent) string {
	var parts []string
	for _, item := range items {
		switch item.Type {
		case "text":
			if strings.TrimSpace(item.Text) != "" {
				parts = append(parts, item.Text)
			}
		case "image":
			parts = append(parts, fmt.Sprintf("[image %s]", item.MIMEType))
		case "audio":
			parts = append(parts, fmt.Sprintf("[audio %s]", item.MIMEType))
		case "resource_link":
			if item.Name != "" {
				parts = append(parts, fmt.Sprintf("[resource] %s %s", item.Name, item.URI))
			} else {
				parts = append(parts, fmt.Sprintf("[resource] %s", item.URI))
			}
		case "resource":
			if item.Resource != nil {
				if strings.TrimSpace(item.Resource.Text) != "" {
					parts = append(parts, item.Resource.Text)
				} else {
					parts = append(parts, fmt.Sprintf("[resource] %s", item.Resource.URI))
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func agentToolsFor(ctx context.Context, prefix string, bridge toolBridge) ([]*agentcore.Tool, error) {
	tools, err := bridge.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*agentcore.Tool, 0, len(tools))
	for _, tool := range tools {
		toolName := qualifyToolName(prefix, tool.Name)
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			}
		}
		originalName := tool.Name
		out = append(out, &agentcore.Tool{
			Name:        toolName,
			Description: util.FirstNonEmpty(tool.Description, tool.Title, tool.Name),
			Parameters:  schema,
			Func: func(ctx context.Context, args json.RawMessage) (any, error) {
				decoded, err := decodeArguments(args)
				if err != nil {
					return nil, err
				}
				result, err := bridge.CallTool(ctx, originalName, decoded)
				if err != nil {
					return nil, err
				}
				return formatToolResult(result), nil
			},
		})
	}
	return out, nil
}

func qualifyToolName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + name
}
