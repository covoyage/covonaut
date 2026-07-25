// Package main implements the A2A ITK (Integration Test Kit) instruction-handling
// agent for covonaut. It is spawned by the ITK test runner as the "current" agent
// and verifies cross-SDK compatibility by parsing nested traversal instructions,
// forwarding them to remote agents, and returning the accumulated trace.
//
// The agent exposes an A2A 1.0 JSON-RPC endpoint (SendMessage / SendStreamingMessage)
// and supports all ITK behavior modes: send_message, push_notification, and resubscribe.
//
// gRPC is not supported (covonaut's A2A server is HTTP-only), so scenarios should
// use the "jsonrpc" transport protocol.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/covoyage/covonaut/a2a"
	"github.com/covoyage/covonaut/example/itk/pb"
)

// ---------------------------------------------------------------------------
// ITK Handler
// ---------------------------------------------------------------------------

// itkHandler implements the A2A AgentHandler plus V1 message/streaming interfaces.
// It parses ITK protobuf instructions from incoming messages, forwards CallAgent
// instructions to remote agents, and returns ReturnResponse / SeriesOfSteps results.
type itkHandler struct {
	mu          sync.Mutex
	card        a2a.AgentCard
	tasks       map[string]*a2a.Task
	pushConfigs map[string]*a2a.PushNotificationConfig
	publisher   a2a.TaskUpdatePublisher
	idCounter   atomic.Int64
}

func newITKHandler(httpPort int) *itkHandler {
	return &itkHandler{
		card: a2a.AgentCard{
			Name:        "Covonaut ITK Agent",
			Description: "Multi-transport covonaut agent for A2A ITK compatibility testing.",
			Version:     "1.0.0",
			URL:         fmt.Sprintf("http://127.0.0.1:%d", httpPort),
			Capabilities: a2a.AgentCapabilities{
				Streaming:         true,
				PushNotifications: true,
			},
			DefaultInputModes:  []string{"text/plain", "application/x-protobuf"},
			DefaultOutputModes: []string{"text/plain", "application/x-protobuf"},
		},
		tasks:       make(map[string]*a2a.Task),
		pushConfigs: make(map[string]*a2a.PushNotificationConfig),
	}
}

// ---------------------------------------------------------------------------
// AgentHandler interface (legacy 0.3)
// ---------------------------------------------------------------------------

func (h *itkHandler) Card() a2a.AgentCard { return h.card }

func (h *itkHandler) SendTask(ctx context.Context, req a2a.SendTaskRequest) (*a2a.Task, error) {
	instruction, err := extractInstruction(req.Message.Parts)
	if err != nil {
		return h.failTask(req.ID, req.SessionID, req.Message, err), nil
	}

	results, err := h.handleInstruction(ctx, instruction)
	if err != nil {
		return h.failTask(req.ID, req.SessionID, req.Message, err), nil
	}

	response := strings.Join(results, "\n")
	task := &a2a.Task{
		ID:        req.ID,
		SessionID: req.SessionID,
		State:     a2a.TaskStateCompleted,
		Messages:  []a2a.Message{req.Message, {Role: "agent", Parts: []a2a.Part{a2a.NewTextPart(response)}}},
		History:   []a2a.TaskStatus{{State: a2a.TaskStateCompleted, Timestamp: time.Now()}},
	}

	h.mu.Lock()
	h.tasks[req.ID] = task
	h.mu.Unlock()

	h.notify(task)
	return task, nil
}

func (h *itkHandler) GetTask(ctx context.Context, req a2a.GetTaskRequest) (*a2a.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if task, ok := h.tasks[req.ID]; ok {
		return task, nil
	}
	return nil, fmt.Errorf("task %s not found", req.ID)
}

func (h *itkHandler) CancelTask(ctx context.Context, req a2a.CancelTaskRequest) (*a2a.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if task, ok := h.tasks[req.ID]; ok {
		task.State = a2a.TaskStateCanceled
		task.History = append(task.History, a2a.TaskStatus{State: a2a.TaskStateCanceled, Timestamp: time.Now()})
		return task, nil
	}
	return nil, fmt.Errorf("task %s not found", req.ID)
}

func (h *itkHandler) QueryTasks(ctx context.Context, req a2a.QueryTasksRequest) (*a2a.QueryTasksResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var tasks []*a2a.Task
	for _, t := range h.tasks {
		tasks = append(tasks, t)
	}
	return &a2a.QueryTasksResult{Tasks: tasks}, nil
}

func (h *itkHandler) SetPushNotification(ctx context.Context, req a2a.SetPushNotificationRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pushConfigs[req.ID] = &req.Config
	return nil
}

func (h *itkHandler) GetPushNotification(ctx context.Context, taskID string) (*a2a.PushNotificationConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cfg, ok := h.pushConfigs[taskID]; ok {
		return cfg, nil
	}
	return nil, fmt.Errorf("push notification config for task %s not found", taskID)
}

func (h *itkHandler) DeletePushNotification(_ context.Context, taskID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.pushConfigs, taskID)
	return nil
}

func (h *itkHandler) SetUpdatePublisher(p a2a.TaskUpdatePublisher) {
	h.mu.Lock()
	h.publisher = p
	h.mu.Unlock()
}

// ---------------------------------------------------------------------------
// V1MessageHandler interface (A2A 1.0)
// ---------------------------------------------------------------------------

func (h *itkHandler) SendMessage(ctx context.Context, req a2a.V1SendMessageRequest) (*a2a.V1SendMessageResponse, error) {
	instruction, err := extractV1Instruction(req.Message)
	if err != nil {
		slog.Error("failed to extract instruction", "error", err)
		return &a2a.V1SendMessageResponse{
			Task: &a2a.V1Task{
				ID:     h.nextID(),
				Status: a2a.V1TaskStatus{State: "failed", Timestamp: time.Now().Format(time.RFC3339)},
			},
		}, nil
	}

	results, err := h.handleInstruction(ctx, instruction)
	if err != nil {
		slog.Error("error handling instruction", "error", err)
		return &a2a.V1SendMessageResponse{
			Task: &a2a.V1Task{
				ID:     h.nextID(),
				Status: a2a.V1TaskStatus{State: "failed", Timestamp: time.Now().Format(time.RFC3339)},
			},
		}, nil
	}

	response := strings.Join(results, "\n")
	return &a2a.V1SendMessageResponse{
		Message: &a2a.V1Message{
			MessageID: h.nextID(),
			Role:      "ROLE_AGENT",
			Parts:     []a2a.V1Part{a2a.NewV1TextPart(response)},
		},
	}, nil
}

// ---------------------------------------------------------------------------
// V1StreamingMessageHandler interface (A2A 1.0)
// ---------------------------------------------------------------------------

func (h *itkHandler) SendStreamingMessage(ctx context.Context, req a2a.V1SendMessageRequest) (<-chan a2a.V1StreamResponse, error) {
	ch := make(chan a2a.V1StreamResponse, 16)

	instruction, err := extractV1Instruction(req.Message)
	if err != nil {
		close(ch)
		return ch, err
	}

	go func() {
		defer close(ch)

		taskID := h.nextID()
		now := time.Now().Format(time.RFC3339)

		// Emit initial task.
		ch <- a2a.V1StreamResponse{
			Task: &a2a.V1Task{
				ID:     taskID,
				Status: a2a.V1TaskStatus{State: "submitted", Timestamp: now},
			},
		}

		// Emit working status.
		ch <- a2a.V1StreamResponse{
			StatusUpdate: &a2a.V1TaskStatusUpdateEvent{
				TaskID: taskID,
				Status: a2a.V1TaskStatus{State: "working", Timestamp: now},
			},
		}

		results, err := h.handleInstruction(ctx, instruction)
		if err != nil {
			slog.Error("error handling streaming instruction", "error", err)
			ch <- a2a.V1StreamResponse{
				StatusUpdate: &a2a.V1TaskStatusUpdateEvent{
					TaskID: taskID,
					Status: a2a.V1TaskStatus{State: "failed", Timestamp: time.Now().Format(time.RFC3339)},
				},
			}
			return
		}

		response := strings.Join(results, "\n")

		// If the instruction requests a hold, emit the response with a
		// "task-finished" marker, then keep emitting periodic working updates.
		if shouldHold(instruction) {
			slog.Info("holding task as requested", "taskId", taskID)
			ch <- a2a.V1StreamResponse{
				StatusUpdate: &a2a.V1TaskStatusUpdateEvent{
					TaskID: taskID,
					Status: a2a.V1TaskStatus{
						State:     "working",
						Timestamp: time.Now().Format(time.RFC3339),
						Message: &a2a.V1Message{
							MessageID: h.nextID(),
							Role:      "ROLE_AGENT",
							Parts:     []a2a.V1Part{a2a.NewV1TextPart(response + "\ntask-finished")},
						},
					},
				},
			}

			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					slog.Info("task cancelled during hold", "taskId", taskID)
					return
				case <-ticker.C:
					ch <- a2a.V1StreamResponse{
						StatusUpdate: &a2a.V1TaskStatusUpdateEvent{
							TaskID: taskID,
							Status: a2a.V1TaskStatus{State: "working", Timestamp: time.Now().Format(time.RFC3339)},
						},
					}
				}
			}
		}

		// Completed.
		ch <- a2a.V1StreamResponse{
			StatusUpdate: &a2a.V1TaskStatusUpdateEvent{
				TaskID: taskID,
				Status: a2a.V1TaskStatus{
					State:     "completed",
					Timestamp: time.Now().Format(time.RFC3339),
					Message: &a2a.V1Message{
						MessageID: h.nextID(),
						Role:      "ROLE_AGENT",
						Parts:     []a2a.V1Part{a2a.NewV1TextPart(response)},
					},
				},
			},
		}
	}()

	return ch, nil
}

// ---------------------------------------------------------------------------
// Instruction handling
// ---------------------------------------------------------------------------

func (h *itkHandler) handleInstruction(ctx context.Context, inst *pb.Instruction) ([]string, error) {
	switch {
	case inst.GetCallAgent() != nil:
		return h.handleCallAgent(ctx, inst.GetCallAgent())

	case inst.GetReturnResponse() != nil:
		return []string{inst.GetReturnResponse().GetResponse()}, nil

	case inst.GetSteps() != nil:
		var allResults []string
		for _, step := range inst.GetSteps().GetInstructions() {
			results, err := h.handleInstruction(ctx, step)
			if err != nil {
				return nil, err
			}
			allResults = append(allResults, results...)
		}
		return allResults, nil

	default:
		return nil, fmt.Errorf("unknown instruction type")
	}
}

func (h *itkHandler) handleCallAgent(ctx context.Context, call *pb.CallAgent) ([]string, error) {
	slog.Info("calling agent", "agentCardUri", call.GetAgentCardUri(), "transport", call.GetTransport())

	// Fetch the remote agent card to find the matching interface URL.
	cardClient := a2a.NewClient(call.GetAgentCardUri())
	remoteCard, err := cardClient.V1().GetAgentCard(ctx)
	var clientURL string
	useREST := false
	if err != nil {
		slog.Warn("failed to fetch agent card, falling back to JSON-RPC at base URL", "error", err)
		clientURL = call.GetAgentCardUri()
	} else {
		// Map the ITK transport name to the A2A protocol binding.
		transport := strings.ToUpper(call.GetTransport())
		switch transport {
		case "GRPC":
			return nil, fmt.Errorf("gRPC transport not supported by covonaut (HTTP-only)")
		case "REST", "HTTP_JSON", "HTTP+JSON":
			// Find the HTTP-JSON interface URL.
			for _, iface := range remoteCard.SupportedInterfaces {
				if strings.EqualFold(iface.ProtocolBinding, "HTTP_JSON") {
					clientURL = iface.URL
					useREST = true
					break
				}
			}
		default: // JSONRPC
			for _, iface := range remoteCard.SupportedInterfaces {
				if strings.EqualFold(iface.ProtocolBinding, "JSONRPC") {
					clientURL = iface.URL
					break
				}
			}
		}
		if clientURL == "" {
			// Fallback: use the first interface.
			if len(remoteCard.SupportedInterfaces) > 0 {
				clientURL = remoteCard.SupportedInterfaces[0].URL
			} else {
				clientURL = call.GetAgentCardUri()
			}
		}
	}

	slog.Info("resolved transport", "url", clientURL, "rest", useREST)

	// Create client with the appropriate transport.
	var client *a2a.Client
	if useREST {
		client = a2a.NewClient(clientURL, a2a.WithRESTTransport())
	} else {
		client = a2a.NewClient(clientURL)
	}
	v1Client := client.V1()

	wrappedMsg, err := wrapInstructionToV1Message(call.GetInstruction())
	if err != nil {
		return nil, fmt.Errorf("failed to wrap nested instruction: %w", err)
	}

	var sendConfig *a2a.V1SendConfiguration
	if call.GetPushNotification() != nil {
		url := call.GetPushNotification().GetUrl()
		if url == "" {
			return nil, fmt.Errorf("URL not specified in push_notification behavior")
		}
		sendConfig = &a2a.V1SendConfiguration{
			TaskPushNotificationConfig: &a2a.V1TaskPushNotificationConfig{
				URL:   url + "/notifications",
				Token: "itk-token",
			},
		}
	}

	req := a2a.V1SendMessageRequest{
		Message:       *wrappedMsg,
		Configuration: sendConfig,
	}

	// Resubscribe behavior: stream, extract task ID, disconnect, resubscribe, cancel.
	if call.GetResubscribe() != nil {
		return h.handleCallAgentWithResubscribe(ctx, v1Client, req, call.GetAgentCardUri())
	}

	// Streaming behavior.
	if call.GetStreaming() {
		stream, err := v1Client.SendStreamingMessage(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("streaming call failed to agent %s: %w", call.GetAgentCardUri(), err)
		}
		defer stream.Close()

		var responses []string
		for {
			event, err := stream.Recv()
			if err != nil {
				break
			}
			responses = append(responses, extractV1StreamResponses(event)...)
		}
		slog.Info("received streaming responses", "agentCardUri", call.GetAgentCardUri())
		return responses, nil
	}

	// Standard send_message behavior.
	result, err := v1Client.SendMessage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send message to agent %s: %w", call.GetAgentCardUri(), err)
	}

	responses := extractV1SendResponses(result)
	slog.Info("received responses", "agentCardUri", call.GetAgentCardUri())
	return responses, nil
}

func (h *itkHandler) handleCallAgentWithResubscribe(ctx context.Context, v1Client *a2a.V1Client, req a2a.V1SendMessageRequest, agentCardUri string) ([]string, error) {
	slog.Info("executing re-subscribe behavior", "agentCardUri", agentCardUri)

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	stream, err := v1Client.SendStreamingMessage(streamCtx, req)
	if err != nil {
		return nil, fmt.Errorf("initial streaming call failed: %w", err)
	}

	var taskID string
	for {
		event, err := stream.Recv()
		if err != nil {
			stream.Close()
			break
		}
		if event.Task != nil {
			taskID = event.Task.ID
			break
		}
		if event.StatusUpdate != nil {
			taskID = event.StatusUpdate.TaskID
			break
		}
	}

	if taskID == "" {
		return nil, fmt.Errorf("could not determine task ID for resubscribe")
	}

	// Disconnect from the stream.
	cancelStream()
	stream.Close()
	slog.Info("disconnected from task, now re-subscribing", "taskId", taskID)

	// Resubscribe.
	resubStream, err := v1Client.SubscribeToTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("resubscribe failed: %w", err)
	}
	defer resubStream.Close()

	var responses []string
	for {
		event, err := resubStream.Recv()
		if err != nil {
			break
		}
		resps := extractV1StreamResponses(event)
		for _, r := range resps {
			cleaned := strings.ReplaceAll(r, "task-finished", "")
			if cleaned != "" {
				responses = append(responses, cleaned)
			}
			if strings.Contains(r, "task-finished") {
				slog.Info("received task-finished after re-subscribe, breaking loop")
				goto done
			}
		}
	}

done:
	if len(responses) == 0 {
		slog.Info("responses empty after loop, reading from task history")
		task, err := v1Client.GetTask(ctx, a2a.V1GetTaskRequest{ID: taskID, HistoryLength: intPtr(50)})
		if err == nil && task != nil {
			for _, msg := range task.History {
				if msg.Role == "ROLE_AGENT" {
					for _, part := range msg.Parts {
						if part.Text != nil {
							t := strings.ReplaceAll(*part.Text, "task-finished", "")
							if t != "" {
								responses = append(responses, t)
							}
						}
					}
				}
			}
		}
	}

	slog.Info("canceling task after retrieval", "taskId", taskID)
	_, err = v1Client.CancelTask(ctx, taskID)
	if err != nil {
		slog.Warn("failed to cancel task after retrieval", "error", err)
	}

	return responses, nil
}

// ---------------------------------------------------------------------------
// Instruction extraction & wrapping
// ---------------------------------------------------------------------------

// extractInstruction parses a protobuf Instruction from legacy a2a.Message parts.
func extractInstruction(parts []a2a.Part) (*pb.Instruction, error) {
	for _, part := range parts {
		// File part with protobuf media type.
		if part.Type == a2a.PartTypeFile && part.File != nil {
			if part.File.MIMEType == "application/x-protobuf" || (part.File.MIMEType == "" && part.File.Name == "instruction.bin") {
				if part.File.Bytes != "" {
					raw, err := base64.StdEncoding.DecodeString(part.File.Bytes)
					if err == nil && len(raw) > 0 {
						var instruction pb.Instruction
						if err := pb.Unmarshal(raw, &instruction); err == nil {
							return &instruction, nil
						}
					}
				}
			}
		}
		// Text part containing base64-encoded protobuf.
		if part.Type == a2a.PartTypeText && part.Text != "" {
			if raw, err := base64.StdEncoding.DecodeString(part.Text); err == nil {
				var instruction pb.Instruction
				if err := pb.Unmarshal(raw, &instruction); err == nil {
					return &instruction, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no valid instruction found in request")
}

// extractV1Instruction parses a protobuf Instruction from V1 message parts.
func extractV1Instruction(msg a2a.V1Message) (*pb.Instruction, error) {
	for _, part := range msg.Parts {
		// Raw binary part with protobuf media type.
		if part.MediaType == "application/x-protobuf" || (part.MediaType == "" && part.Filename == "instruction.bin") {
			if part.Raw != nil {
				raw, err := base64.StdEncoding.DecodeString(*part.Raw)
				if err == nil && len(raw) > 0 {
					var instruction pb.Instruction
					if err := pb.Unmarshal(raw, &instruction); err == nil {
						return &instruction, nil
					}
				}
			}
		}
		// Text part containing base64-encoded protobuf.
		if part.Text != nil && *part.Text != "" {
			if raw, err := base64.StdEncoding.DecodeString(*part.Text); err == nil {
				var instruction pb.Instruction
				if err := pb.Unmarshal(raw, &instruction); err == nil {
					return &instruction, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no valid instruction found in request")
}

// wrapInstructionToV1Message marshals an Instruction into a V1 message with a
// raw protobuf part, matching the format expected by ITK agents.
func wrapInstructionToV1Message(inst *pb.Instruction) (*a2a.V1Message, error) {
	instBytes, err := pb.Marshal(inst)
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(instBytes)
	part := a2a.NewV1RawPart(encoded, "instruction.bin", "application/x-protobuf")
	return &a2a.V1Message{
		MessageID: fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Role:      "ROLE_USER",
		Parts:     []a2a.V1Part{part},
	}, nil
}

// ---------------------------------------------------------------------------
// Response extraction helpers
// ---------------------------------------------------------------------------

// extractV1SendResponses extracts text responses from a V1SendMessageResponse.
func extractV1SendResponses(result *a2a.V1SendMessageResponse) []string {
	var responses []string
	if result == nil {
		return responses
	}
	if result.Message != nil {
		for _, part := range result.Message.Parts {
			if part.Text != nil && *part.Text != "" {
				responses = append(responses, *part.Text)
			}
		}
	}
	if result.Task != nil {
		responses = append(responses, extractV1TaskResponses(result.Task)...)
	}
	return responses
}

// extractV1StreamResponses extracts text responses from a V1StreamResponse event.
func extractV1StreamResponses(event *a2a.V1StreamResponse) []string {
	if event == nil {
		return nil
	}
	var responses []string
	if event.Message != nil {
		for _, part := range event.Message.Parts {
			if part.Text != nil && *part.Text != "" {
				responses = append(responses, *part.Text)
			}
		}
	}
	if event.Task != nil {
		responses = append(responses, extractV1TaskResponses(event.Task)...)
	}
	if event.StatusUpdate != nil && event.StatusUpdate.Status.Message != nil {
		for _, part := range event.StatusUpdate.Status.Message.Parts {
			if part.Text != nil && *part.Text != "" {
				responses = append(responses, *part.Text)
			}
		}
	}
	return responses
}

// extractV1TaskResponses extracts text from a V1 task's status message and history.
func extractV1TaskResponses(task *a2a.V1Task) []string {
	var responses []string
	if task == nil {
		return responses
	}
	if task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			if part.Text != nil && *part.Text != "" {
				responses = append(responses, *part.Text)
			}
		}
	}
	for _, msg := range task.History {
		if msg.Role == "ROLE_AGENT" {
			for _, part := range msg.Parts {
				if part.Text != nil && *part.Text != "" {
					responses = append(responses, *part.Text)
				}
			}
		}
	}
	return responses
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func shouldHold(inst *pb.Instruction) bool {
	if inst.GetReturnResponse() != nil && inst.GetReturnResponse().GetHoldTask() {
		return true
	}
	if inst.GetSteps() != nil {
		for _, step := range inst.GetSteps().GetInstructions() {
			if shouldHold(step) {
				return true
			}
		}
	}
	return false
}

func (h *itkHandler) failTask(id, sessionID string, msg a2a.Message, err error) *a2a.Task {
	task := &a2a.Task{
		ID:        id,
		SessionID: sessionID,
		State:     a2a.TaskStateFailed,
		Messages:  []a2a.Message{msg},
		History:   []a2a.TaskStatus{{State: a2a.TaskStateFailed, Timestamp: time.Now()}},
	}
	h.mu.Lock()
	h.tasks[id] = task
	h.mu.Unlock()
	h.notify(task)
	return task
}

func (h *itkHandler) notify(task *a2a.Task) {
	h.mu.Lock()
	publisher := h.publisher
	h.mu.Unlock()
	if publisher == nil {
		return
	}
	publisher.PublishTaskUpdate(task.ID, &a2a.TaskUpdateEvent{Result: task, Final: task.State == a2a.TaskStateCompleted || task.State == a2a.TaskStateFailed || task.State == a2a.TaskStateCanceled})
}

func (h *itkHandler) nextID() string {
	return fmt.Sprintf("itk-%d", h.idCounter.Add(1))
}

func intPtr(v int) *int { return &v }

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	httpPort := flag.Int("httpPort", 10102, "HTTP port for JSON-RPC interface")
	grpcPort := flag.Int("grpcPort", 11002, "gRPC port (unused — covonaut is HTTP-only; accepted for ITK compatibility)")
	flag.Parse()

	// Configure logging.
	logLevelStr := os.Getenv("ITK_LOG_LEVEL")
	if logLevelStr == "" {
		logLevelStr = "INFO"
	}
	var level slog.Level
	switch strings.ToUpper(logLevelStr) {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	if *grpcPort != 11002 {
		slog.Info("gRPC port specified but covonaut is HTTP-only; gRPC will not be started", "grpcPort", *grpcPort)
	}

	handler := newITKHandler(*httpPort)
	server := a2a.NewServer(handler)

	// HTTP server.
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", *httpPort),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 3 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("starting ITK HTTP server", "address", fmt.Sprintf("127.0.0.1:%d", *httpPort))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}
