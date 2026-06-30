package a2a

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
)

// ---------------------------------------------------------------------------
// A2A Handoff Integration
//
// This file bridges the existing agentcore Handoff mechanism with the A2A
// protocol, allowing agents to hand off tasks to remote A2A agents.
// ---------------------------------------------------------------------------

// RemoteHandoffConfig describes a remote A2A agent that can receive handoffs.
type RemoteHandoffConfig struct {
	Name        string // local name used for the handoff tool
	Description string // shown to the LLM
	URL         string // remote A2A agent URL
}

// RemoteHandoffExtension creates an agentcore.Extension that registers
// handoff tools for remote A2A agents.
type RemoteHandoffExtension struct {
	agents []RemoteHandoffConfig
}

// NewRemoteHandoffExtension creates a new remote handoff extension.
func NewRemoteHandoffExtension(agents []RemoteHandoffConfig) *RemoteHandoffExtension {
	return &RemoteHandoffExtension{agents: agents}
}

// Name implements agentcore.Extension.
func (e *RemoteHandoffExtension) Name() string { return "remote-handoff" }

// Init implements agentcore.Extension.
func (e *RemoteHandoffExtension) Init(ctx context.Context, agent *agentcore.Agent) error {
	for _, cfg := range e.agents {
		tool := e.createRemoteHandoffTool(cfg)
		agent.RegisterTools(tool)
	}
	return nil
}

// Dispose implements agentcore.Extension.
func (e *RemoteHandoffExtension) Dispose() error { return nil }

func (e *RemoteHandoffExtension) createRemoteHandoffTool(cfg RemoteHandoffConfig) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "transfer_to_" + cfg.Name,
		Description: fmt.Sprintf("Hand off to remote A2A agent %s. %s", cfg.Name, cfg.Description),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Context or instructions for the remote agent",
				},
			},
			"required":             []string{"message"},
			"additionalProperties": false,
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}

			client := NewClient(cfg.URL)

			card, err := client.GetAgentCard(ctx)
			if err != nil {
				return nil, fmt.Errorf("discover remote agent %q: %w", cfg.Name, err)
			}

			task, err := client.SendTask(ctx, SendTaskRequest{
				ID:      generateTaskID(),
				Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart(p.Message)}},
			})
			if err != nil {
				return nil, fmt.Errorf("send task to %q: %w", cfg.Name, err)
			}

			result := extractTaskResult(task, card)
			return map[string]string{
				"agent":   card.Name,
				"result":  result,
				"task_id": task.ID,
			}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Streaming Remote Handoff
// ---------------------------------------------------------------------------

// RemoteHandoffStreamConfig enables streaming handoff to remote A2A agents.
type RemoteHandoffStreamConfig struct {
	Name        string
	Description string
	URL         string
}

// RemoteHandoffStreamExtension creates streaming handoff tools.
type RemoteHandoffStreamExtension struct {
	agents []RemoteHandoffStreamConfig
}

// NewRemoteHandoffStreamExtension creates a streaming remote handoff extension.
func NewRemoteHandoffStreamExtension(agents []RemoteHandoffStreamConfig) *RemoteHandoffStreamExtension {
	return &RemoteHandoffStreamExtension{agents: agents}
}

// Name implements agentcore.Extension.
func (e *RemoteHandoffStreamExtension) Name() string { return "remote-handoff-stream" }

// Init implements agentcore.Extension.
func (e *RemoteHandoffStreamExtension) Init(ctx context.Context, agent *agentcore.Agent) error {
	for _, cfg := range e.agents {
		tool := e.createStreamHandoffTool(cfg)
		agent.RegisterTools(tool)
	}
	return nil
}

// Dispose implements agentcore.Extension.
func (e *RemoteHandoffStreamExtension) Dispose() error { return nil }

func (e *RemoteHandoffStreamExtension) createStreamHandoffTool(cfg RemoteHandoffStreamConfig) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "transfer_to_" + cfg.Name,
		Description: fmt.Sprintf("Hand off to remote A2A agent %s (streaming). %s", cfg.Name, cfg.Description),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Context or instructions for the remote agent",
				},
			},
			"required":             []string{"message"},
			"additionalProperties": false,
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}

			client := NewClient(cfg.URL)

			stream, err := client.SendTaskSubscribe(ctx, SendTaskRequest{
				ID:      generateTaskID(),
				Message: Message{Role: string(RoleUser), Parts: []Part{NewTextPart(p.Message)}},
			})
			if err != nil {
				return nil, fmt.Errorf("stream task to %q: %w", cfg.Name, err)
			}
			defer stream.Close()

			var finalResult string
			for {
				ev, ok := stream.Recv()
				if !ok {
					break
				}
				if ev.Error != nil {
					return nil, fmt.Errorf("stream error from %q: %s", cfg.Name, ev.Error.Message)
				}
				if ev.Result != nil {
					finalResult = extractTaskResult(ev.Result, nil)
					if ev.Final {
						break
					}
				}
			}

			return map[string]string{
				"agent":  cfg.Name,
				"result": finalResult,
			}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func generateTaskID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "task_" + hex.EncodeToString(b) // best-effort; b is still zero-filled
	}
	return "task_" + hex.EncodeToString(b)
}

func extractTaskResult(task *Task, card *AgentCard) string {
	if task == nil {
		return ""
	}

	for _, art := range task.Artifacts {
		for _, part := range art.Parts {
			if part.Type == PartTypeText {
				return part.Text
			}
		}
	}

	for i := len(task.Messages) - 1; i >= 0; i-- {
		msg := task.Messages[i]
		if msg.Role == string(RoleAgent) {
			for _, part := range msg.Parts {
				if part.Type == PartTypeText {
					return part.Text
				}
			}
		}
	}

	if card != nil {
		return fmt.Sprintf("Task completed by %s", card.Name)
	}

	return "Task completed"
}

// ---------------------------------------------------------------------------
// A2A Server Integration with agentcore.Agent
// ---------------------------------------------------------------------------

// AgentAdapter wraps an agentcore.Agent to implement the AgentHandler interface.
// It provides a higher-level integration than DefaultAgentHandler.
type AgentAdapter struct {
	card      AgentCard
	agent     *agentcore.Agent
	config    agentcore.Config
	handler   *DefaultAgentHandler
	callbacks *AdapterCallbacks
}

// AdapterCallbacks allows customizing the adapter behavior.
type AdapterCallbacks struct {
	// BeforeRun is called before running the agent. Can modify input or context.
	BeforeRun func(ctx context.Context, taskID, input string) (string, error)
	// AfterRun is called after the agent completes. Can modify the task result.
	AfterRun func(ctx context.Context, taskID, output string, err error) (*Task, error)
	// OnStatusChange is called when a task status changes.
	OnStatusChange func(taskID string, state TaskState)
}

// NewAgentAdapter creates an A2A AgentAdapter from an agentcore.Agent.
func NewAgentAdapter(card AgentCard, agent *agentcore.Agent, cfg agentcore.Config, callbacks *AdapterCallbacks) *AgentAdapter {
	h := NewDefaultAgentHandler(card, agent, cfg)
	return &AgentAdapter{
		card:      card,
		agent:     agent,
		config:    cfg,
		handler:   h,
		callbacks: callbacks,
	}
}

// Card returns the agent card.
func (a *AgentAdapter) Card() AgentCard { return a.card }

// SendTask implements AgentHandler with callback support.
func (a *AgentAdapter) SendTask(ctx context.Context, req SendTaskRequest) (*Task, error) {
	if a.callbacks != nil && a.callbacks.BeforeRun != nil {
		input := extractMessageText(req.Message)
		modified, err := a.callbacks.BeforeRun(ctx, req.ID, input)
		if err != nil {
			return nil, err
		}
		if modified != input {
			req.inputOverride = modified
		}
	}

	task, err := a.handler.SendTask(ctx, req)

	if a.callbacks != nil && a.callbacks.AfterRun != nil {
		var output string
		if task != nil && len(task.Artifacts) > 0 {
			output = extractTaskResult(task, nil)
		}
		customTask, cbErr := a.callbacks.AfterRun(ctx, req.ID, output, err)
		if cbErr == nil && customTask != nil {
			return customTask, nil
		}
	}

	if a.callbacks != nil && a.callbacks.OnStatusChange != nil && task != nil {
		a.callbacks.OnStatusChange(task.ID, task.State)
	}

	return task, err
}

// GetTask implements AgentHandler.
func (a *AgentAdapter) GetTask(ctx context.Context, req GetTaskRequest) (*Task, error) {
	return a.handler.GetTask(ctx, req)
}

// CancelTask implements AgentHandler.
func (a *AgentAdapter) CancelTask(ctx context.Context, req CancelTaskRequest) (*Task, error) {
	return a.handler.CancelTask(ctx, req)
}

func (a *AgentAdapter) QueryTasks(ctx context.Context, req QueryTasksRequest) (*QueryTasksResult, error) {
	return a.handler.QueryTasks(ctx, req)
}

// SetPushNotification implements AgentHandler.
func (a *AgentAdapter) SetPushNotification(ctx context.Context, req SetPushNotificationRequest) error {
	return a.handler.SetPushNotification(ctx, req)
}

// GetPushNotification implements AgentHandler.
func (a *AgentAdapter) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	return a.handler.GetPushNotification(ctx, taskID)
}

func (a *AgentAdapter) SetUpdatePublisher(p TaskUpdatePublisher) {
	a.handler.SetUpdatePublisher(p)
}

func (a *AgentAdapter) SetInputRequiredPredicate(fn func(output string) bool) {
	a.handler.SetInputRequiredPredicate(fn)
}

func extractMessageText(msg Message) string {
	var text string
	for _, p := range msg.Parts {
		if p.Type == PartTypeText {
			text += p.Text
		}
	}
	return text
}
