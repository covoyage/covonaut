package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/pkg/util"
)

const (
	headerSessionID       = "Mcp-Session-Id"
	headerProtocolVersion = "Mcp-Protocol-Version"
	headerLastEventID     = "Last-Event-ID"
)

var errSessionExpired = errors.New("mcp session expired")
var errServerStreamUnsupported = errors.New("mcp server stream unsupported")

type sessionExpiredError struct {
	sessionID string
}

func (e sessionExpiredError) Error() string { return errSessionExpired.Error() }
func (e sessionExpiredError) Unwrap() error { return errSessionExpired }

type HTTPConfig struct {
	Name                string
	Endpoint            string
	ToolPrefix          string
	ClientName          string
	ClientVersion       string
	RequestTimeout      time.Duration
	Headers             map[string]string
	Client              *http.Client
	EnableServerStream  bool
	NotificationHandler func(context.Context, string, json.RawMessage) error
	RequestHandler      func(context.Context, string, json.RawMessage) (any, error)
	ErrorHandler        func(context.Context, error)
	Discovery           DiscoveryConfig
}

type HTTPClient struct {
	cfg               HTTPConfig
	httpClient        *http.Client
	sessionID         string
	negotiatedProto   string
	nextID            atomic.Int64
	closed            atomic.Bool
	initMu            sync.Mutex
	stateMu           sync.RWMutex
	bgCtx             context.Context
	bgCancel          context.CancelFunc
	streamDone        chan struct{}
	streamStarted     bool
	discovery         *discoveryState
	capState          *capabilityState
	eventSink         runtimeEventSink
	hooksMu           sync.RWMutex
	notificationHooks []func(context.Context, string, json.RawMessage) error
}

type HTTPExtension struct {
	name              string
	cfg               HTTPConfig
	client            *HTTPClient
	tools             []*agentcore.Tool
	agent             *agentcore.Agent
	toolNames         []string
	refreshMu         sync.Mutex
	refreshScheduleMu sync.Mutex
	refreshInFlight   bool
	refreshPending    bool
}

func NewHTTPClient(ctx context.Context, cfg HTTPConfig) (*HTTPClient, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("mcp: endpoint is required")
	}
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	c := &HTTPClient{
		cfg:             cfg,
		httpClient:      httpClient,
		negotiatedProto: protocolVersion,
		bgCtx:           bgCtx,
		bgCancel:        bgCancel,
		streamDone:      make(chan struct{}),
		discovery:       newDiscoveryState(cfg.Discovery),
		capState:        newCapabilityState(),
	}
	if err := c.initializeSession(ctx); err != nil {
		return nil, err
	}
	if cfg.EnableServerStream {
		c.ensureServerStream()
	} else {
		close(c.streamDone)
	}
	return c, nil
}

func NewHTTPExtension(ctx context.Context, cfg HTTPConfig) (*HTTPExtension, error) {
	client, err := NewHTTPClient(ctx, cfg)
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
		name = "mcp-http"
	}
	return &HTTPExtension{
		name:   name,
		cfg:    cfg,
		client: client,
		tools:  tools,
	}, nil
}

func (e *HTTPExtension) Name() string { return e.name }
func (e *HTTPExtension) Init(ctx context.Context, agent *agentcore.Agent) error {
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
func (e *HTTPExtension) Client() *HTTPClient { return e.client }
func (e *HTTPExtension) Dispose() error {
	if e.client == nil {
		return nil
	}
	return e.client.Close()
}
func (e *HTTPExtension) Tools() []*agentcore.Tool {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()
	return append([]*agentcore.Tool(nil), e.tools...)
}
func (e *HTTPExtension) SnapshotEvents() []agentcore.Event {
	if e.client == nil {
		return nil
	}
	return []agentcore.Event{CapabilitiesUpdatedEvent{
		At:           time.Now(),
		Extension:    e.name,
		Transport:    "http",
		Capabilities: e.client.Capabilities(),
	}}
}

func (c *HTTPClient) initialize(ctx context.Context) error {
	return c.initializeSession(ctx)
}

func (c *HTTPClient) initializeSession(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    util.DefaultString(c.cfg.ClientName, "covonaut"),
			"version": util.DefaultString(c.cfg.ClientVersion, "0.1.0"),
		},
	}
	var result struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
	}
	headers, err := c.callInitialize(ctx, params, &result)
	if err != nil {
		return err
	}
	proto := protocolVersion
	if result.ProtocolVersion != "" {
		proto = result.ProtocolVersion
	}
	c.setSessionState(headers.Get(headerSessionID), proto)
	caps, err := decodeCapabilities(result.Capabilities)
	if err != nil {
		return err
	}
	c.capState.set(ctx, caps)
	c.ensureServerStream()
	return c.notifyInitialize(ctx)
}

func (c *HTTPClient) ListTools(ctx context.Context) ([]Tool, error) {
	var out []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result toolListResult
		if _, err := c.call(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Tools...)
		if result.NextCursor == "" {
			return out, nil
		}
		cursor = result.NextCursor
	}
}

func (c *HTTPClient) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	var result ToolResult
	if _, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *HTTPClient) AgentTools(ctx context.Context) ([]*agentcore.Tool, error) {
	return agentToolsFor(ctx, c.cfg.ToolPrefix, c)
}

func (c *HTTPClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.bgCancel()
	c.initMu.Lock()
	defer c.initMu.Unlock()
	c.stateMu.Lock()
	if !c.streamStarted {
		select {
		case <-c.streamDone:
		default:
			close(c.streamDone)
		}
	}
	c.stateMu.Unlock()
	select {
	case <-c.streamDone:
	case <-time.After(2 * time.Second):
	}
	sessionID, _ := c.sessionState()
	if sessionID == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, c.cfg.Endpoint, nil)
	if err != nil {
		return nil
	}
	c.applyHeaders(req, true, true)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	return nil
}

func (c *HTTPClient) emitReconnectEvent(phase, reason string, attempt int, staleSessionID, sessionID, lastEventID string, err error) {
	c.emitRuntimeEvent(ReconnectEvent{
		At:             time.Now(),
		Extension:      c.extensionName(),
		Transport:      "http",
		Phase:          phase,
		Reason:         reason,
		Attempt:        attempt,
		StaleSessionID: staleSessionID,
		SessionID:      sessionID,
		LastEventID:    lastEventID,
		Error:          util.ErrorString(err),
	})
}

func (c *HTTPClient) emitTransportError(operation, reason string, err error, statusCode int, sessionID, lastEventID string, recoverable bool) {
	c.emitRuntimeEvent(TransportErrorEvent{
		At:          time.Now(),
		Extension:   c.extensionName(),
		Transport:   "http",
		Operation:   operation,
		Reason:      reason,
		Message:     util.ErrorString(err),
		StatusCode:  statusCode,
		SessionID:   sessionID,
		LastEventID: lastEventID,
		Recoverable: recoverable,
	})
}

func (c *HTTPClient) call(ctx context.Context, method string, params any, out any) (http.Header, error) {
	headers, err := c.callOnce(ctx, method, params, out)
	if err == nil || !errors.Is(err, errSessionExpired) || isInitializeMethod(method) {
		return headers, err
	}
	staleSession := expiredSessionID(err)
	c.emitReconnectEvent(ReconnectPhaseStarted, ReconnectReasonSessionExpired, 1, staleSession, "", "", nil)
	if err := c.reinitializeSession(ctx, expiredSessionID(err)); err != nil {
		c.emitReconnectEvent(ReconnectPhaseFailed, ReconnectReasonSessionExpired, 1, staleSession, "", "", err)
		return nil, err
	}
	sessionID, _ := c.sessionState()
	c.emitReconnectEvent(ReconnectPhaseSucceeded, ReconnectReasonSessionExpired, 1, staleSession, sessionID, "", nil)
	return c.callOnce(ctx, method, params, out)
}

func (c *HTTPClient) callOnce(ctx context.Context, method string, params any, out any) (http.Header, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.cfg.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.RequestTimeout)
		defer cancel()
	}
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	resp, err := c.doJSONRPC(ctx, msg, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rpcResp, err := c.decodeHTTPRPCResponse(ctx, resp.Body, resp.Header.Get("Content-Type"), id)
	if err != nil {
		return resp.Header, fmt.Errorf("mcp %s decode response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return resp.Header, fmt.Errorf("mcp %s: %s", method, rpcResp.Error.Message)
	}
	if out != nil && len(rpcResp.Result) > 0 {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			return resp.Header, fmt.Errorf("mcp %s decode result: %w", method, err)
		}
	}
	return resp.Header, nil
}

func (c *HTTPClient) notify(ctx context.Context, method string, params any) error {
	err := c.notifyOnce(ctx, method, params)
	if err == nil || !errors.Is(err, errSessionExpired) || isInitializeMethod(method) {
		return err
	}
	staleSession := expiredSessionID(err)
	c.emitReconnectEvent(ReconnectPhaseStarted, ReconnectReasonSessionExpired, 1, staleSession, "", "", nil)
	if err := c.reinitializeSession(ctx, expiredSessionID(err)); err != nil {
		c.emitReconnectEvent(ReconnectPhaseFailed, ReconnectReasonSessionExpired, 1, staleSession, "", "", err)
		return err
	}
	sessionID, _ := c.sessionState()
	c.emitReconnectEvent(ReconnectPhaseSucceeded, ReconnectReasonSessionExpired, 1, staleSession, sessionID, "", nil)
	return c.notifyOnce(ctx, method, params)
}

func (c *HTTPClient) notifyOnce(ctx context.Context, method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	resp, err := c.doJSONRPC(ctx, msg, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *HTTPClient) doJSONRPC(ctx context.Context, msg any, expectResponse bool) (*http.Response, error) {
	if c.closed.Load() {
		return nil, errClientClosed
	}
	var cancel context.CancelFunc
	ctx, cancel = mergeContext(ctx, c.bgCtx)
	defer cancel()
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("mcp marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mcp create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if expectResponse {
		req.Header.Set("Accept", "application/json, text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	sessionID, negotiatedProto := c.sessionState()
	c.applyHeadersWithState(req, !isInitializeRequest(msg), !isInitializeRequest(msg), sessionID, negotiatedProto)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp http request: %w", err)
	}
	if expectResponse && resp.StatusCode == http.StatusNotFound && requestIncludesSession(msg) {
		resp.Body.Close()
		return nil, sessionExpiredError{sessionID: sessionID}
	}
	if expectResponse {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("mcp http status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return resp, nil
	}
	if resp.StatusCode == http.StatusNotFound && requestIncludesSession(msg) {
		resp.Body.Close()
		return nil, sessionExpiredError{sessionID: sessionID}
	}
	if resp.StatusCode != http.StatusAccepted && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("mcp notify status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func (c *HTTPClient) applyHeaders(req *http.Request, includeProtocol bool, includeSession bool) {
	sessionID, negotiatedProto := c.sessionState()
	c.applyHeadersWithState(req, includeProtocol, includeSession, sessionID, negotiatedProto)
}

func (c *HTTPClient) applyHeadersWithState(req *http.Request, includeProtocol bool, includeSession bool, sessionID, negotiatedProto string) {
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	if includeSession && sessionID != "" {
		req.Header.Set(headerSessionID, sessionID)
	}
	if includeProtocol && negotiatedProto != "" {
		req.Header.Set(headerProtocolVersion, negotiatedProto)
	}
}

func isInitializeRequest(msg any) bool {
	m, ok := msg.(map[string]any)
	if !ok {
		return false
	}
	method, _ := m["method"].(string)
	return method == "initialize"
}

func isInitializeMethod(method string) bool {
	return method == "initialize" || method == "notifications/initialized"
}

func requestIncludesSession(msg any) bool {
	return !isInitializeRequest(msg)
}

func (c *HTTPClient) decodeHTTPRPCResponse(ctx context.Context, body io.Reader, contentType string, expectedID string) (*rpcResponse, error) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return c.decodeSSERPCResponse(ctx, body, expectedID)
	}
	var rpcResp rpcResponse
	if err := json.NewDecoder(body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	return &rpcResp, nil
}

func (c *HTTPClient) decodeSSERPCResponse(ctx context.Context, body io.Reader, expectedID string) (*rpcResponse, error) {
	state := sseStreamState{}
	resp, nextState, err := readSSERPCResponse(body, expectedID)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return resp, nil
	}
	state.merge(nextState)
	for {
		if state.lastEventID == "" {
			return nil, fmt.Errorf("mcp sse stream ended before response %q", expectedID)
		}
		if err := sleepContext(ctx, state.retry); err != nil {
			return nil, err
		}
		resumeResp, err := c.resumeSSE(ctx, state.lastEventID)
		if err != nil {
			return nil, err
		}
		resp, nextState, readErr := readSSERPCResponse(resumeResp.Body, expectedID)
		_ = resumeResp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp != nil {
			return resp, nil
		}
		state.merge(nextState)
	}
}

func readSSERPCResponse(body io.Reader, expectedID string) (*rpcResponse, sseStreamState, error) {
	var matchedResp *rpcResponse
	state, err := consumeSSEStream(body, func(evt sseEvent) (bool, error) {
		resp, matched, err := handleSSEEvent(evt, expectedID)
		if err != nil {
			return false, err
		}
		if matched {
			matchedResp = resp
			return true, nil
		}
		return false, nil
	})
	return matchedResp, state, err
}

func consumeSSEStream(body io.Reader, handler func(sseEvent) (bool, error)) (sseStreamState, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var evt sseEvent
	var state sseStreamState
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			state.merge(eventState(evt))
			stop, err := handler(evt)
			if err != nil {
				return state, err
			}
			if stop {
				return state, nil
			}
			evt = sseEvent{}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value := splitSSEField(line)
		switch field {
		case "data":
			evt.data = append(evt.data, value)
		case "id":
			evt.id = value
		case "event":
			evt.event = value
		case "retry":
			evt.retry = value
		}
	}
	if err := scanner.Err(); err != nil {
		return state, err
	}
	if len(evt.data) > 0 || evt.id != "" || evt.event != "" || evt.retry != "" {
		state.merge(eventState(evt))
		stop, err := handler(evt)
		if err != nil {
			return state, err
		}
		if stop {
			return state, nil
		}
	}
	return state, nil
}

type sseEvent struct {
	id    string
	event string
	retry string
	data  []string
}

type sseStreamState struct {
	lastEventID string
	retry       time.Duration
}

func eventState(evt sseEvent) sseStreamState {
	state := sseStreamState{lastEventID: strings.TrimSpace(evt.id)}
	if evt.retry != "" {
		if ms, err := strconv.Atoi(strings.TrimSpace(evt.retry)); err == nil && ms >= 0 {
			state.retry = time.Duration(ms) * time.Millisecond
		}
	}
	return state
}

func (s *sseStreamState) merge(other sseStreamState) {
	if other.lastEventID != "" {
		s.lastEventID = other.lastEventID
	}
	if other.retry > 0 {
		s.retry = other.retry
	}
}

func handleSSEEvent(evt sseEvent, expectedID string) (*rpcResponse, bool, error) {
	payload := strings.TrimSpace(strings.Join(evt.data, "\n"))
	if payload == "" {
		return nil, false, nil
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return nil, false, fmt.Errorf("invalid sse payload: %w", err)
	}

	idRaw, ok := envelope["id"]
	if !ok {
		return nil, false, nil
	}
	var msgID any
	if err := json.Unmarshal(idRaw, &msgID); err != nil {
		return nil, false, fmt.Errorf("decode sse response id: %w", err)
	}
	if fmt.Sprint(msgID) != expectedID {
		return nil, false, nil
	}

	var resp rpcResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return nil, false, fmt.Errorf("decode sse response: %w", err)
	}
	return &resp, true, nil
}

func splitSSEField(line string) (string, string) {
	field, value, ok := strings.Cut(line, ":")
	if !ok {
		return line, ""
	}
	value = strings.TrimPrefix(value, " ")
	return field, value
}

func (c *HTTPClient) resumeSSE(ctx context.Context, lastEventID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp create resume request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(headerLastEventID, lastEventID)
	c.applyHeaders(req, true, true)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp resume request: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		sessionID, _ := c.sessionState()
		return nil, sessionExpiredError{sessionID: sessionID}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("mcp resume status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("mcp resume expected text/event-stream, got %q: %s", resp.Header.Get("Content-Type"), strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *HTTPClient) sessionState() (string, string) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.sessionID, c.negotiatedProto
}

func (c *HTTPClient) setSessionState(sessionID, negotiatedProto string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.sessionID = sessionID
	c.negotiatedProto = negotiatedProto
}

func (c *HTTPClient) reinitializeSession(ctx context.Context, staleSession string) error {
	if c.closed.Load() {
		return errClientClosed
	}
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.closed.Load() {
		return errClientClosed
	}
	currentSession, _ := c.sessionState()
	if staleSession != "" && currentSession != "" && currentSession != staleSession {
		c.emitReconnectEvent(ReconnectPhaseSkipped, ReconnectReasonSessionExpired, 0, staleSession, currentSession, "", nil)
		return nil
	}
	return c.initializeSession(ctx)
}

func expiredSessionID(err error) string {
	var sessionErr sessionExpiredError
	if errors.As(err, &sessionErr) {
		return sessionErr.sessionID
	}
	return ""
}

func (c *HTTPClient) callInitialize(ctx context.Context, params any, out any) (http.Header, error) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      strconv.FormatInt(c.nextID.Add(1), 10),
		"method":  "initialize",
		"params":  params,
	}
	resp, err := c.doJSONRPC(ctx, msg, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return resp.Header, fmt.Errorf("mcp initialize decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return resp.Header, fmt.Errorf("mcp initialize: %s", rpcResp.Error.Message)
	}
	if out != nil && len(rpcResp.Result) > 0 {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			return resp.Header, fmt.Errorf("mcp initialize decode result: %w", err)
		}
	}
	return resp.Header, nil
}

func (c *HTTPClient) notifyInitialize(ctx context.Context) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	resp, err := c.doJSONRPC(ctx, msg, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *HTTPClient) runServerStream() {
	defer close(c.streamDone)
	state := sseStreamState{}
	reconnectAttempts := 0
	for {
		select {
		case <-c.bgCtx.Done():
			return
		default:
		}

		nextState, err := c.listenServerStreamOnce(c.bgCtx, state.lastEventID)
		if err == nil {
			if reconnectAttempts > 0 {
				c.emitReconnectEvent(ReconnectPhaseSucceeded, ReconnectReasonServerStreamWake, reconnectAttempts, "", "", state.lastEventID, nil)
				reconnectAttempts = 0
			}
			state.merge(nextState)
			if state.retry <= 0 {
				state.retry = time.Second
			}
			if err := sleepContext(c.bgCtx, state.retry); err != nil {
				return
			}
			continue
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, errServerStreamUnsupported) {
			c.emitTransportError("server_stream", "server_stream_unsupported", err, 0, "", state.lastEventID, false)
			return
		}
		if errors.Is(err, errSessionExpired) {
			reconnectAttempts++
			staleSession := expiredSessionID(err)
			c.emitReconnectEvent(ReconnectPhaseStarted, ReconnectReasonServerStream404, reconnectAttempts, staleSession, "", state.lastEventID, nil)
			if reinitErr := c.reinitializeSession(c.bgCtx, staleSession); reinitErr != nil {
				c.emitReconnectEvent(ReconnectPhaseFailed, ReconnectReasonServerStream404, reconnectAttempts, staleSession, "", state.lastEventID, reinitErr)
				c.reportAsyncError(reinitErr)
				if err := sleepContext(c.bgCtx, time.Second); err != nil {
					return
				}
				continue
			}
			sessionID, _ := c.sessionState()
			c.emitReconnectEvent(ReconnectPhaseSucceeded, ReconnectReasonServerStream404, reconnectAttempts, staleSession, sessionID, state.lastEventID, nil)
			state = sseStreamState{}
			continue
		}
		reconnectAttempts++
		c.emitTransportError("server_stream", ReconnectReasonServerStreamEOF, err, 0, "", state.lastEventID, true)
		c.reportAsyncError(err)
		if state.retry <= 0 {
			state.retry = time.Second
		}
		if err := sleepContext(c.bgCtx, state.retry); err != nil {
			return
		}
	}
}

func (c *HTTPClient) listenServerStreamOnce(ctx context.Context, lastEventID string) (sseStreamState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Endpoint, nil)
	if err != nil {
		return sseStreamState{}, fmt.Errorf("mcp create server stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set(headerLastEventID, lastEventID)
	}
	c.applyHeaders(req, true, true)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return sseStreamState{}, fmt.Errorf("mcp server stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		sessionID, _ := c.sessionState()
		return sseStreamState{}, sessionExpiredError{sessionID: sessionID}
	}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return sseStreamState{}, errServerStreamUnsupported
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return sseStreamState{}, fmt.Errorf("mcp server stream status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		body, _ := io.ReadAll(resp.Body)
		return sseStreamState{}, fmt.Errorf("mcp server stream expected text/event-stream, got %q: %s", resp.Header.Get("Content-Type"), strings.TrimSpace(string(body)))
	}

	return consumeSSEStream(resp.Body, func(evt sseEvent) (bool, error) {
		return false, c.handleServerSSEEvent(ctx, evt)
	})
}

func (c *HTTPClient) handleServerSSEEvent(ctx context.Context, evt sseEvent) error {
	payload := strings.TrimSpace(strings.Join(evt.data, "\n"))
	if payload == "" {
		return nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return fmt.Errorf("invalid server sse payload: %w", err)
	}

	methodRaw, hasMethod := envelope["method"]
	if !hasMethod {
		return nil
	}
	var method string
	if err := json.Unmarshal(methodRaw, &method); err != nil {
		return fmt.Errorf("decode server message method: %w", err)
	}
	params := envelope["params"]
	if len(params) == 0 {
		params = json.RawMessage(`null`)
	}

	idRaw, hasID := envelope["id"]
	if !hasID {
		if err := c.handleDiscoveryNotification(ctx, method, params); err != nil {
			return err
		}
		for _, hook := range c.notificationHookSnapshot() {
			if err := hook(ctx, method, params); err != nil {
				return err
			}
		}
		if c.cfg.NotificationHandler != nil {
			if err := c.cfg.NotificationHandler(ctx, method, params); err != nil {
				return fmt.Errorf("handle server notification %s: %w", method, err)
			}
		}
		return nil
	}

	var reqID any
	if err := json.Unmarshal(idRaw, &reqID); err != nil {
		return fmt.Errorf("decode server request id: %w", err)
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

func (c *HTTPClient) respondToServerRequest(ctx context.Context, id any, result any, handlerErr error) error {
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
	resp, err := c.doJSONRPC(ctx, msg, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *HTTPClient) reportAsyncError(err error) {
	if err == nil {
		return
	}
	if c.cfg.ErrorHandler != nil {
		c.cfg.ErrorHandler(c.bgCtx, err)
	}
}

func (c *HTTPClient) AddNotificationHook(h func(context.Context, string, json.RawMessage) error) {
	if h == nil {
		return
	}
	c.hooksMu.Lock()
	defer c.hooksMu.Unlock()
	c.notificationHooks = append(c.notificationHooks, h)
}

func (c *HTTPClient) notificationHookSnapshot() []func(context.Context, string, json.RawMessage) error {
	c.hooksMu.RLock()
	defer c.hooksMu.RUnlock()
	return append([]func(context.Context, string, json.RawMessage) error(nil), c.notificationHooks...)
}

func (c *HTTPClient) SetEventSink(emit func(agentcore.Event)) {
	c.eventSink.Set(emit)
}

func (c *HTTPClient) emitRuntimeEvent(event agentcore.Event) {
	c.eventSink.Emit(event)
}

func (e *HTTPExtension) emitCapabilitiesEvent(caps ServerCapabilities) {
	if e.agent == nil {
		return
	}
	e.agent.EmitEvent(CapabilitiesUpdatedEvent{
		At:           time.Now(),
		Extension:    e.name,
		Transport:    "http",
		Capabilities: caps,
	})
}

func (c *HTTPClient) shouldRunServerStream() bool {
	if c.closed.Load() {
		return false
	}
	if !c.cfg.EnableServerStream {
		return false
	}
	caps := c.Capabilities()
	if c.cfg.RequestHandler != nil || c.cfg.NotificationHandler != nil {
		return true
	}
	if caps.Tools.ListChanged || caps.Resources.Subscribe || caps.Resources.ListChanged || caps.Prompts.ListChanged {
		return true
	}
	return false
}

func (c *HTTPClient) ensureServerStream() {
	if !c.shouldRunServerStream() {
		return
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.streamStarted {
		return
	}
	c.streamStarted = true
	go c.runServerStream()
}

func (c *HTTPClient) extensionName() string {
	return util.DefaultString(c.cfg.Name, "mcp-http")
}

var _ agentcore.Extension = (*HTTPExtension)(nil)
var _ agentcore.ToolProvider = (*HTTPExtension)(nil)
