package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Client calls remote A2A agents.
// ---------------------------------------------------------------------------

// Client is an A2A client for interacting with remote agents.
type Client struct {
	baseURL     string
	httpClient  *http.Client
	apiKey      string
	bearerToken string

	mu           sync.RWMutex
	idCounter    int64
	maxRetries   int
	retryBackoff time.Duration
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithAPIKey sets an API key for authentication (sent as X-API-Key header).
func WithAPIKey(key string) ClientOption {
	return func(c *Client) {
		c.apiKey = key
	}
}

// WithBearerToken sets a Bearer token for authentication (sent as Authorization header).
func WithBearerToken(token string) ClientOption {
	return func(c *Client) {
		c.bearerToken = token
	}
}

// WithRetry sets retry policy for the client.
func WithRetry(maxRetries int, backoff time.Duration) ClientOption {
	return func(c *Client) {
		c.maxRetries = maxRetries
		c.retryBackoff = backoff
	}
}

// NewClient creates an A2A client targeting the given agent URL.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// setAuthHeaders applies authentication headers to the request.
func (c *Client) setAuthHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

// GetAgentCard fetches the agent card from /.well-known/agent.json.
func (c *Client) GetAgentCard(ctx context.Context) (*AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/.well-known/agent.json", nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("agent card: %d %s", resp.StatusCode, string(body))
	}

	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("decode agent card: %w", err)
	}
	return &card, nil
}

// ---------------------------------------------------------------------------
// Task operations
// ---------------------------------------------------------------------------

// SendTask sends a task to the remote agent (synchronous).
func (c *Client) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	resp, err := c.call(ctx, "tasks/send", req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, err
	}

	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	return &task, nil
}

// SendTaskSubscribe sends a task and subscribes to streaming updates via SSE.
func (c *Client) SendTaskSubscribe(ctx context.Context, req SendTaskRequest) (*TaskStream, error) {
	rpcReq := JSONRPCRequest{JSONRPC: "2.0", ID: c.nextID(), Method: "tasks/sendSubscribe"}
	params, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	rpcReq.Params = params

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	c.setAuthHeaders(httpReq)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		httpResp.Body.Close()
		return nil, fmt.Errorf("sse: %d", httpResp.StatusCode)
	}

	stream := &TaskStream{
		ch:       make(chan *TaskUpdateEvent, 8),
		body:     httpResp.Body,
		cancel:   ctx,
		client:   c,
		taskID:   req.ID,
		maxRetry: c.maxRetries,
	}

	go stream.readLoop()
	return stream, nil
}

// GetTask retrieves the current state of a task.
func (c *Client) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	resp, err := c.call(ctx, "tasks/get", req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, err
	}

	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	return &task, nil
}

// CancelTask cancels a running task.
func (c *Client) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	resp, err := c.call(ctx, "tasks/cancel", req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, err
	}

	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	return &task, nil
}

// QueryTasks queries tasks by session ID or state.
func (c *Client) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	resp, err := c.call(ctx, "tasks/query", req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, err
	}

	var result QueryTasksResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode query result: %w", err)
	}
	return &result, nil
}

// SetPushNotification configures push notifications for a task.
func (c *Client) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	resp, err := c.call(ctx, "tasks/pushNotification/set", req)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	return nil
}

// GetPushNotification retrieves the push notification config for a task.
func (c *Client) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	resp, err := c.call(ctx, "tasks/pushNotification/get", map[string]string{"id": taskID})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, err
	}

	var cfg PushNotificationConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode push config: %w", err)
	}
	return &cfg, nil
}

// ResubscribeTask reconnects to an existing task's SSE stream, replaying
// historical events followed by live updates.
func (c *Client) ResubscribeTask(ctx context.Context, taskID string) (*TaskStream, error) {
	return c.resubscribe(ctx, taskID, "")
}

func (c *Client) resubscribe(ctx context.Context, taskID, lastEventID string) (*TaskStream, error) {
	rpcReq := JSONRPCRequest{JSONRPC: "2.0", ID: c.nextID(), Method: "tasks/resubscribe"}
	params, err := json.Marshal(map[string]string{"id": taskID})
	if err != nil {
		return nil, err
	}
	rpcReq.Params = params

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		httpReq.Header.Set("Last-Event-ID", lastEventID)
	}
	c.setAuthHeaders(httpReq)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		httpResp.Body.Close()
		return nil, fmt.Errorf("sse: %d", httpResp.StatusCode)
	}

	stream := &TaskStream{
		ch:       make(chan *TaskUpdateEvent, 8),
		body:     httpResp.Body,
		cancel:   ctx,
		client:   c,
		taskID:   taskID,
		maxRetry: c.maxRetries,
	}

	go stream.readLoop()
	return stream, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (c *Client) call(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.retryBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		rpcReq := JSONRPCRequest{JSONRPC: "2.0", ID: c.nextID(), Method: method}
		reqID := rpcReq.ID
		if params != nil {
			data, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			rpcReq.Params = data
		}

		body, err := json.Marshal(rpcReq)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		c.setAuthHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if !isRetryableError(err) {
				return nil, err
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			lastErr = fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
			if isRetryableStatus(resp.StatusCode) {
				continue
			}
			return nil, lastErr
		}

		var rpcResp JSONRPCResponse
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode response: %w", err)
		}
		resp.Body.Close()

		if rpcResp.ID != nil {
			if wantID, ok := reqID.(int64); ok {
				if !matchID(rpcResp.ID, wantID) {
					return nil, fmt.Errorf("response ID mismatch: want %v, got %v", reqID, rpcResp.ID)
				}
			}
		}

		return &rpcResp, nil
	}
	return nil, fmt.Errorf("after %d retries: %w", c.maxRetries, lastErr)
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Timeout()
	}
	return false
}

func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (c *Client) nextID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.idCounter++
	return c.idCounter
}

func matchID(got any, want int64) bool {
	switch v := got.(type) {
	case float64:
		return v == float64(want)
	case int64:
		return v == want
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return false
		}
		return n == want
	case string:
		return v == fmt.Sprintf("%d", want)
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// TaskStream
// ---------------------------------------------------------------------------

// TaskStream receives SSE updates for a task with automatic reconnection.
type TaskStream struct {
	ch       chan *TaskUpdateEvent
	body     io.ReadCloser
	cancel   context.Context
	err      error
	mu       sync.Mutex
	lastID    string
	client    *Client
	taskID    string
	maxRetry  int
	retryNum  int
}

// Recv returns the next task update event. Returns nil, false when done.
func (s *TaskStream) Recv() (*TaskUpdateEvent, bool) {
	ev, ok := <-s.ch
	return ev, ok
}

// Err returns any error that occurred during streaming.
func (s *TaskStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close closes the stream.
func (s *TaskStream) Close() error {
	return s.body.Close()
}

func (s *TaskStream) readLoop() {
	defer close(s.ch)
	defer s.body.Close()

	for {
		decoder := NewSSEDecoder(s.body)
		for {
			select {
			case <-s.cancel.Done():
				return
			default:
			}

			ev, err := decoder.Next()
			if err != nil {
				if s.tryReconnect() {
					continue
				}
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
			if ev == nil {
				return
			}

			if ev.ID != "" {
				s.mu.Lock()
				s.lastID = ev.ID
				s.mu.Unlock()
			}

			var update TaskUpdateEvent
			if err := json.Unmarshal([]byte(ev.Data), &update); err != nil {
				s.mu.Lock()
				s.err = fmt.Errorf("decode sse event: %w", err)
				s.mu.Unlock()
				return
			}

			select {
			case s.ch <- &update:
				if update.Final {
					return
				}
			case <-s.cancel.Done():
				return
			}
		}
	}
}

func (s *TaskStream) tryReconnect() bool {
	if s.client == nil || s.taskID == "" {
		return false
	}

	s.mu.Lock()
	lastID := s.lastID
	retries := s.maxRetry
	s.mu.Unlock()

	if retries <= 0 {
		return false
	}

	s.mu.Lock()
	s.maxRetry = retries - 1
	s.retryNum++
	attempt := s.retryNum
	s.mu.Unlock()

	s.body.Close()

	backoff := time.Duration(500<<min(attempt-1, 5)) * time.Millisecond
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	select {
	case <-time.After(backoff):
	case <-s.cancel.Done():
		return false
	}

	rpcReq := JSONRPCRequest{JSONRPC: "2.0", ID: s.client.nextID(), Method: "tasks/resubscribe"}
	params, _ := json.Marshal(map[string]string{"id": s.taskID})
	rpcReq.Params = params

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return false
	}

	httpReq, err := http.NewRequestWithContext(s.cancel, http.MethodPost, s.client.baseURL+"/", bytes.NewReader(body))
	if err != nil {
		return false
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		httpReq.Header.Set("Last-Event-ID", lastID)
	}
	s.client.setAuthHeaders(httpReq)

	httpResp, err := s.client.httpClient.Do(httpReq)
	if err != nil {
		return false
	}

	if httpResp.StatusCode != http.StatusOK {
		httpResp.Body.Close()
		return false
	}

	s.body = httpResp.Body
	return true
}

// ---------------------------------------------------------------------------
// SSE Decoder
// ---------------------------------------------------------------------------

// SSEEvent represents a single Server-Sent Event.
type SSEEvent struct {
	ID    string
	Event string
	Data  string
}

// SSEDecoder decodes a stream of Server-Sent Events.
type SSEDecoder struct {
	r *bufio.Reader
}

// NewSSEDecoder creates an SSE decoder from a reader.
func NewSSEDecoder(r io.Reader) *SSEDecoder {
	return &SSEDecoder{r: bufio.NewReader(r)}
}

// Next reads the next SSE event from the stream.
func (d *SSEDecoder) Next() (*SSEEvent, error) {
	var ev SSEEvent
	var dataLines []string

	for {
		line, err := d.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(dataLines) > 0 {
				ev.Data = strings.Join(dataLines, "\n")
				return &ev, nil
			}
			return nil, err
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
			if len(dataLines) > 0 {
				ev.Data = strings.Join(dataLines, "\n")
				return &ev, nil
			}
			continue
		}

		if strings.HasPrefix(line, "id:") {
			ev.ID = strings.TrimPrefix(line, "id:")
			ev.ID = strings.TrimSpace(ev.ID)
			continue
		}
		if strings.HasPrefix(line, "event:") {
			ev.Event = strings.TrimPrefix(line, "event:")
			ev.Event = strings.TrimSpace(ev.Event)
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			if len(data) > 0 && data[0] == ' ' {
				data = data[1:]
			}
			dataLines = append(dataLines, data)
			continue
		}
	}
}
