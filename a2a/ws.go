package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type wsConn struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	closed bool
}

func (c *wsConn) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("websocket closed")
	}
	return c.conn.WriteJSON(v)
}

func (c *wsConn) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.auth.APIKey != "" || s.auth.BearerToken != "" {
		key := r.URL.Query().Get("apiKey")
		token := r.URL.Query().Get("token")
		if !s.checkWSAuth(key, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	wc := &wsConn{conn: conn}
	go s.wsPingLoop(wc)
	go s.wsReadLoop(wc, r)
}

func (s *Server) wsPingLoop(wc *wsConn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			wc.mu.Lock()
			if wc.closed {
				wc.mu.Unlock()
				return
			}
			err := wc.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second))
			wc.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) checkWSAuth(key, token string) bool {
	if s.auth.APIKey != "" && key == s.auth.APIKey {
		return true
	}
	if s.auth.BearerToken != "" && token == s.auth.BearerToken {
		return true
	}
	return false
}

func (s *Server) wsReadLoop(wc *wsConn, r *http.Request) {
	defer wc.close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	for {
		wc.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, message, err := wc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				s.logger.Debug("websocket read error", "error", err)
			}
			return
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(message, &req); err != nil {
			wc.writeJSON(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &JSONRPCError{Code: JSONRPCParseError, Message: err.Error()},
			})
			continue
		}

		switch req.Method {
		case "tasks/send":
			s.handleWSSendTask(ctx, wc, req)
		case "tasks/get":
			s.handleWSGetTask(ctx, wc, req)
		case "tasks/cancel":
			s.handleWSCancelTask(ctx, wc, req)
		case "tasks/query":
			s.handleWSQueryTasks(ctx, wc, req)
		case "tasks/subscribe":
			if !s.handler.Card().Capabilities.Streaming {
				wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorUnsupportedOperation, Message: "streaming not supported"}})
				continue
			}
			s.handleWSSubscribe(ctx, wc, req, cancel)
		case "tasks/resubscribe":
			if !s.handler.Card().Capabilities.Streaming {
				wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorUnsupportedOperation, Message: "streaming not supported"}})
				continue
			}
			s.handleWSResubscribe(ctx, wc, req, cancel)
		case "tasks/pushNotification/set":
			if !s.handler.Card().Capabilities.PushNotifications {
				wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorPushNotSupported, Message: "push notifications not supported"}})
				continue
			}
			s.handleWSSetPushNotification(ctx, wc, req)
		case "tasks/pushNotification/get":
			if !s.handler.Card().Capabilities.PushNotifications {
				wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorPushNotSupported, Message: "push notifications not supported"}})
				continue
			}
			s.handleWSGetPushNotification(ctx, wc, req)
		default:
			wc.writeJSON(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: JSONRPCMethodNotFound, Message: "method not found: " + req.Method},
			})
		}
	}
}

func (s *Server) handleWSSendTask(ctx context.Context, wc *wsConn, req JSONRPCRequest) {
	var params SendTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	card := s.handler.Card()
	if len(card.DefaultInputModes) > 0 {
		requestedModes := ExtractInputModes(params.Message)
		if err := ValidateInputModes(requestedModes, card.DefaultInputModes); err != nil {
			wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorContentTypeNotSupported, Message: err.Error()}})
			return
		}
	}

	task, err := s.handler.SendTask(ctx, params)
	if err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInternalError, Message: err.Error()}})
		return
	}

	s.recordTask(task)
	if s.sessionMgr != nil && task.SessionID != "" {
		s.sessionMgr.AddTask(task.SessionID, task.ID)
	}
	wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: task})
}

func (s *Server) handleWSGetTask(ctx context.Context, wc *wsConn, req JSONRPCRequest) {
	var params GetTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	task, err := s.handler.GetTask(ctx, params)
	if err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorTaskNotFound, Message: err.Error()}})
		return
	}

	wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: task})
}

func (s *Server) handleWSCancelTask(ctx context.Context, wc *wsConn, req JSONRPCRequest) {
	var params CancelTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	task, err := s.handler.CancelTask(ctx, params)
	if err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorTaskNotCancelable, Message: err.Error()}})
		return
	}

	s.recordTask(task)
	wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: task})
}

func (s *Server) handleWSQueryTasks(ctx context.Context, wc *wsConn, req JSONRPCRequest) {
	var params QueryTasksRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	result, err := s.handler.QueryTasks(ctx, params)
	if err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInternalError, Message: err.Error()}})
		return
	}

	wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (s *Server) handleWSSetPushNotification(ctx context.Context, wc *wsConn, req JSONRPCRequest) {
	var params SetPushNotificationRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	if err := s.handler.SetPushNotification(ctx, params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInternalError, Message: err.Error()}})
		return
	}

	wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: nil})
}

func (s *Server) handleWSGetPushNotification(ctx context.Context, wc *wsConn, req JSONRPCRequest) {
	var params GetTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	cfg, err := s.handler.GetPushNotification(ctx, params.ID)
	if err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInternalError, Message: err.Error()}})
		return
	}

	wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: cfg})
}

func (s *Server) handleWSSubscribe(ctx context.Context, wc *wsConn, req JSONRPCRequest, cancel context.CancelFunc) {
	var params SendTaskRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	card := s.handler.Card()
	if len(card.DefaultInputModes) > 0 {
		requestedModes := ExtractInputModes(params.Message)
		if err := ValidateInputModes(requestedModes, card.DefaultInputModes); err != nil {
			wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: A2AErrorContentTypeNotSupported, Message: err.Error()}})
			return
		}
	}

	taskID := params.ID
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", time.Now().UnixNano())
		params.ID = taskID
	}

	ch := s.subscribeToTask(taskID)
	defer s.unsubscribeFromTask(taskID, ch)

	type taskResult struct {
		task *Task
		err  error
	}
	resultCh := make(chan taskResult, 1)
	go func() {
		task, err := s.handler.SendTask(ctx, params)
		resultCh <- taskResult{task, err}
	}()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case r := <-resultCh:
			if r.err != nil {
				wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInternalError, Message: r.err.Error()}})
				return
			}
			s.recordTask(r.task)
			if s.sessionMgr != nil && r.task.SessionID != "" {
				s.sessionMgr.AddTask(r.task.SessionID, r.task.ID)
			}
			final := isTerminalState(r.task.State) || r.task.State == TaskStateInputRequired
			wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: r.task})
			if final {
				return
			}
		case <-heartbeat.C:
			wc.mu.Lock()
			if wc.closed {
				wc.mu.Unlock()
				return
			}
			err := wc.conn.WriteMessage(websocket.TextMessage, []byte(": heartbeat\n\n"))
			wc.mu.Unlock()
			if err != nil {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := wc.writeJSON(ev); err != nil {
				return
			}
			if ev.Final {
				return
			}
		}
	}
}

func (s *Server) handleWSResubscribe(ctx context.Context, wc *wsConn, req JSONRPCRequest, cancel context.CancelFunc) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: err.Error()}})
		return
	}

	if _, err := s.handler.GetTask(ctx, GetTaskRequest{ID: params.ID}); err != nil {
		wc.writeJSON(JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &JSONRPCError{Code: JSONRPCInvalidParams, Message: fmt.Sprintf("task %q not found", params.ID)}})
		return
	}

	ch := s.subscribeToTask(params.ID)
	go s.wsForwardEvents(ctx, wc, ch, params.ID, cancel)
}

func (s *Server) subscribeToTask(taskID string) chan *TaskUpdateEvent {
	ts := s.getTaskState(taskID)
	ts.mu.Lock()
	ch := make(chan *TaskUpdateEvent, 16)
	ts.subs = append(ts.subs, ch)
	ts.mu.Unlock()
	return ch
}

func (s *Server) unsubscribeFromTask(taskID string, ch chan *TaskUpdateEvent) {
	ts := s.getTaskState(taskID)
	ts.mu.Lock()
	for i, c := range ts.subs {
		if c == ch {
			ts.subs = append(ts.subs[:i], ts.subs[i+1:]...)
			break
		}
	}
	close(ch)
	ts.mu.Unlock()
}

func (s *Server) wsForwardEvents(ctx context.Context, wc *wsConn, ch chan *TaskUpdateEvent, taskID string, cancel context.CancelFunc) {
	defer cancel()
	defer s.unsubscribeFromTask(taskID, ch)

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			wc.mu.Lock()
			if wc.closed {
				wc.mu.Unlock()
				return
			}
			err := wc.conn.WriteMessage(websocket.TextMessage, []byte(": heartbeat\n\n"))
			wc.mu.Unlock()
			if err != nil {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := wc.writeJSON(ev); err != nil {
				return
			}
			if ev.Final {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// WebSocket Client
// ---------------------------------------------------------------------------

type WSClient struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
	bearer     string
	logger     *slog.Logger
	maxRetries int
}

func NewWSClient(baseURL string) *WSClient {
	return &WSClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     slog.Default(),
		maxRetries: 3,
	}
}

func (c *WSClient) WithAPIKey(key string) *WSClient {
	c.apiKey = key
	return c
}

func (c *WSClient) WithBearer(token string) *WSClient {
	c.bearer = token
	return c
}

func (c *WSClient) WithMaxRetries(n int) *WSClient {
	c.maxRetries = n
	return c
}

type WSConnection struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	closed    bool
	ch        chan *TaskUpdateEvent
	err       error
	ctx       context.Context
	cancel    context.CancelFunc

	client    *WSClient
	maxRetry  int
	retryNum  int
}

func (c *WSClient) Connect(ctx context.Context) (*WSConnection, error) {
	u := c.baseURL
	if strings.HasPrefix(u, "http") {
		u = "ws" + u[4:]
	}
	if !strings.HasSuffix(u, "/ws") {
		u += "/ws"
	}

	reqHeader := http.Header{}
	if c.apiKey != "" {
		reqHeader.Set("X-API-Key", c.apiKey)
	}
	if c.bearer != "" {
		reqHeader.Set("Authorization", "Bearer "+c.bearer)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u, reqHeader)
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}

	ctx2, cancel := context.WithCancel(ctx)
	wsc := &WSConnection{
		conn:      conn,
		ch:        make(chan *TaskUpdateEvent, 16),
		ctx:       ctx2,
		cancel:    cancel,
		client:    c,
		maxRetry:  c.maxRetries,
	}

	go wsc.readLoop()
	return wsc, nil
}

func (c *WSConnection) readLoop() {
	defer close(c.ch)
	defer c.cancel()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPingHandler(func(appData string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return c.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if c.tryReconnect() {
				continue
			}
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.mu.Lock()
				c.err = err
				c.mu.Unlock()
			}
			return
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(message, &raw); err != nil {
			continue
		}

		if _, hasResult := raw["result"]; hasResult {
			var resp JSONRPCResponse
			if err := json.Unmarshal(message, &resp); err != nil {
				continue
			}
			if resp.Result != nil {
				data, _ := json.Marshal(resp.Result)
				var task Task
				if err := json.Unmarshal(data, &task); err == nil && task.ID != "" {
					ev := &TaskUpdateEvent{
						ID:     resp.ID,
						Result: &task,
						Final:  isTerminalState(task.State),
					}
					select {
					case c.ch <- ev:
					default:
					}
				}
			}
			continue
		}

		var ev TaskUpdateEvent
		if err := json.Unmarshal(message, &ev); err == nil && ev.Result != nil {
			select {
			case c.ch <- &ev:
			default:
			}
		}
	}
}

func (c *WSConnection) tryReconnect() bool {
	c.mu.Lock()
	if c.closed || c.client == nil || c.maxRetry <= 0 {
		c.mu.Unlock()
		return false
	}
	c.retryNum++
	attempt := c.retryNum
	c.maxRetry--
	c.mu.Unlock()

	backoff := time.Duration(500<<min(attempt-1, 5)) * time.Millisecond
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	select {
	case <-time.After(backoff):
	case <-c.ctx.Done():
		return false
	}

	u := c.client.baseURL
	if strings.HasPrefix(u, "http") {
		u = "ws" + u[4:]
	}
	if !strings.HasSuffix(u, "/ws") {
		u += "/ws"
	}

	reqHeader := http.Header{}
	if c.client.apiKey != "" {
		reqHeader.Set("X-API-Key", c.client.apiKey)
	}
	if c.client.bearer != "" {
		reqHeader.Set("Authorization", "Bearer "+c.client.bearer)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(c.ctx, u, reqHeader)
	if err != nil {
		return false
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		conn.Close()
		return false
	}
	oldConn := c.conn
	c.conn = conn
	c.mu.Unlock()

	oldConn.Close()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	return true
}

func (c *WSConnection) SendRequest(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Method:  method,
	}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = p
	}

	return c.conn.WriteJSON(req)
}

func (c *WSConnection) Recv() (*TaskUpdateEvent, bool) {
	ev, ok := <-c.ch
	return ev, ok
}

func (c *WSConnection) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *WSConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(5*time.Second))
	return c.conn.Close()
}
