package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// REST path prefix. The REST transport is mounted under /rest/.
const restPrefix = "/rest"

// ---------------------------------------------------------------------------
// REST handlers — HTTP+JSON transport binding (A2A 1.0)
//
// Unlike JSON-RPC, REST sends direct JSON request/response bodies without
// the JSON-RPC envelope. Streaming uses SSE with direct JSON events.
// ---------------------------------------------------------------------------

// handleRESTSendMessage handles POST /rest/message:send
func (s *Server) handleRESTSendMessage(w http.ResponseWriter, r *http.Request) {
	body, ok := readRESTBody(w, r, s.maxRequestBody)
	if !ok {
		return
	}
	var params v1SendMessageRequest
	if err := json.Unmarshal(body, &params); err != nil {
		writeRESTError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	ctx := r.Context()
	if handler, ok := s.handler.(V1MessageHandler); ok {
		result, err := handler.SendMessage(ctx, params)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "send message failed: %v", err)
			return
		}
		writeRESTJSON(w, result)
		return
	}
	// Fallback: convert to legacy SendTask.
	legacyReq, err := params.toLegacy()
	if err != nil {
		writeRESTError(w, http.StatusBadRequest, "invalid params: %v", err)
		return
	}
	task, err := s.handler.SendTask(ctx, legacyReq)
	if err != nil {
		writeRESTError(w, http.StatusInternalServerError, "send task failed: %v", err)
		return
	}
	s.recordTask(task)
	v1T := taskToV1(task)
	writeRESTJSON(w, &V1SendMessageResponse{Task: &v1T})
}

// handleRESTStreamMessage handles POST /rest/message:stream (SSE)
func (s *Server) handleRESTStreamMessage(w http.ResponseWriter, r *http.Request) {
	body, ok := readRESTBody(w, r, s.maxRequestBody)
	if !ok {
		return
	}
	var params v1SendMessageRequest
	if err := json.Unmarshal(body, &params); err != nil {
		writeRESTError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	ctx := r.Context()
	if handler, ok := s.handler.(V1StreamingMessageHandler); ok {
		stream, err := handler.SendStreamingMessage(ctx, params)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "streaming failed: %v", err)
			return
		}
		streamRESTSSE(ctx, w, stream)
		return
	}
	// Fallback: legacy SendTask with SSE streaming.
	legacyReq, err := params.toLegacy()
	if err != nil {
		writeRESTError(w, http.StatusBadRequest, "invalid params: %v", err)
		return
	}
	flusher, ok := prepareV1SSE(w)
	if !ok {
		writeRESTError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	updates := s.subscribeToTask(legacyReq.ID)
	defer s.unsubscribeFromTask(legacyReq.ID, updates)
	type taskResult struct {
		task *Task
		err  error
	}
	resultCh := make(chan taskResult, 1)
	go func() {
		task, err := s.handler.SendTask(ctx, legacyReq)
		resultCh <- taskResult{task: task, err: err}
	}()
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	var initialTask *Task
	for initialTask == nil {
		select {
		case <-ctx.Done():
			return
		case result := <-resultCh:
			if result.err != nil {
				writeV1SSEError(w, flusher, nil, JSONRPCInternalError, result.err.Error())
				return
			}
			initialTask = result.task
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
	s.recordTask(initialTask)
	data, _ := json.Marshal(map[string]any{"task": taskToV1(initialTask)})
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
	if isTerminalState(initialTask.State) || initialTask.State == TaskStateInputRequired {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates.done:
			return
		case event := <-updates.events:
			if !writeV1SSEEvent(w, flusher, nil, streamResponseFromLegacy(legacyReq.ID, legacyReq.SessionID, event)) {
				return
			}
			if event.Final {
				return
			}
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// handleRESTGetTask handles GET /rest/tasks/{id}
func (s *Server) handleRESTGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeRESTError(w, http.StatusBadRequest, "missing task id")
		return
	}
	var historyLength *int
	if raw := r.URL.Query().Get("historyLength"); raw != "" {
		val, err := strconv.Atoi(raw)
		if err != nil {
			writeRESTError(w, http.StatusBadRequest, "invalid historyLength: %v", err)
			return
		}
		historyLength = &val
	}
	task, err := s.handler.GetTask(r.Context(), GetTaskRequest{ID: id, HistoryLength: deint(historyLength)})
	if err != nil {
		writeRESTError(w, http.StatusNotFound, "task not found: %v", err)
		return
	}
	s.recordTask(task)
	writeRESTJSON(w, taskToV1(task))
}

// handleRESTTaskAction handles POST /rest/tasks/{idAndAction}
// Actions: :cancel, :subscribe
func (s *Server) handleRESTTaskAction(w http.ResponseWriter, r *http.Request) {
	idAndAction := r.PathValue("idAndAction")
	if idAndAction == "" {
		writeRESTError(w, http.StatusBadRequest, "missing task id and action")
		return
	}
	ctx := r.Context()

	if id, ok := strings.CutSuffix(idAndAction, ":cancel"); ok {
		task, err := s.handler.CancelTask(ctx, CancelTaskRequest{ID: id})
		if err != nil {
			writeRESTError(w, http.StatusBadRequest, "cancel failed: %v", err)
			return
		}
		s.recordTask(task)
		writeRESTJSON(w, taskToV1(task))
		return
	}

	if id, ok := strings.CutSuffix(idAndAction, ":subscribe"); ok {
		if !s.handler.Card().Capabilities.Streaming {
			writeRESTError(w, http.StatusBadRequest, "streaming not supported")
			return
		}
		ts, err := s.handler.(interface {
			ResubscribeTask(ctx context.Context, taskID string) (*TaskStream, error)
		}).ResubscribeTask(ctx, id)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "subscribe failed: %v", err)
			return
		}
		flusher, ok := prepareV1SSE(w)
		if !ok {
			writeRESTError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}
		streamLegacySSE(ctx, w, flusher, ts)
		return
	}

	writeRESTError(w, http.StatusBadRequest, "unknown action in path: %s", idAndAction)
}

// handleRESTCreatePushConfig handles POST /rest/tasks/{id}/pushNotificationConfigs
func (s *Server) handleRESTCreatePushConfig(w http.ResponseWriter, r *http.Request) {
	if !s.handler.Card().Capabilities.PushNotifications {
		writeRESTError(w, http.StatusBadRequest, "push notifications not supported")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeRESTError(w, http.StatusBadRequest, "missing task id")
		return
	}
	body, ok := readRESTBody(w, r, s.maxRequestBody)
	if !ok {
		return
	}
	var config V1TaskPushNotificationConfig
	if err := json.Unmarshal(body, &config); err != nil {
		writeRESTError(w, http.StatusBadRequest, "invalid request body: %v", err)
		return
	}
	config.TaskID = id
	if err := s.handler.SetPushNotification(r.Context(), SetPushNotificationRequest{
		ID:     id,
		Config: PushNotificationConfig{URL: config.URL, Token: config.Token},
	}); err != nil {
		writeRESTError(w, http.StatusInternalServerError, "failed to set push config: %v", err)
		return
	}
	writeRESTJSON(w, config)
}

// handleRESTListTasks handles GET /rest/tasks
func (s *Server) handleRESTListTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	result, err := s.handler.QueryTasks(r.Context(), QueryTasksRequest{
		SessionID: query.Get("sessionId"),
		Limit:     limit,
	})
	if err != nil {
		writeRESTError(w, http.StatusInternalServerError, "query failed: %v", err)
		return
	}
	tasks := make([]V1Task, 0, len(result.Tasks))
	for _, t := range result.Tasks {
		vt := taskToV1(t)
		tasks = append(tasks, vt)
	}
	writeRESTJSON(w, V1ListTasksResponse{Tasks: tasks, PageSize: limit, TotalSize: len(tasks)})
}

// ---------------------------------------------------------------------------
// REST helpers
// ---------------------------------------------------------------------------

func readRESTBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, bool) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxRequestBody
	}
	lr := io.LimitReader(r.Body, maxBytes)
	body, err := io.ReadAll(lr)
	if err != nil {
		writeRESTError(w, http.StatusBadRequest, "failed to read body: %v", err)
		return nil, false
	}
	if len(body) == 0 {
		writeRESTError(w, http.StatusBadRequest, "empty request body")
		return nil, false
	}
	return body, true
}

func writeRESTJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeRESTError(w http.ResponseWriter, status int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": fmt.Sprintf(format, args...),
		},
	})
}

// streamRESTSSE streams V1StreamResponse events as SSE for REST transport.
func streamRESTSSE(ctx context.Context, w http.ResponseWriter, stream <-chan V1StreamResponse) {
	flusher, ok := prepareV1SSE(w)
	if !ok {
		writeRESTError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	first := true
	taskStream := false
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-stream:
			if !open {
				if first {
					writeV1SSEError(w, flusher, nil, A2AErrorInvalidAgentResponse, "stream closed before its initial task or message")
				}
				return
			}
			if err := validateNativeV1StreamEvent(event, first, taskStream); err != nil {
				writeV1SSEError(w, flusher, nil, A2AErrorInvalidAgentResponse, err.Error())
				return
			}
			if first {
				taskStream = event.Task != nil
				first = false
			}
			if !writeV1SSEEvent(w, flusher, nil, &event) {
				return
			}
			if event.Message != nil {
				return
			}
			if event.StatusUpdate != nil && isTerminalOrInterruptedV1State(event.StatusUpdate.Status.State) {
				return
			}
		}
	}
}

// streamLegacySSE streams legacy TaskStream events as SSE for REST transport.
func streamLegacySSE(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, ts *TaskStream) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		ev, ok := ts.Recv()
		if !ok {
			return
		}
		if ev != nil && ev.Result != nil {
			v1Task := taskToV1(ev.Result)
			data, _ := json.Marshal(V1StreamResponse{Task: &v1Task})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		if ev != nil && ev.Final {
			return
		}
	}
}

func deint(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
