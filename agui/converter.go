package agui

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

type Converter struct {
	threadID      string
	runID         string
	parentRunID   string
	msgSeq        atomic.Int64
	toolSeq       atomic.Int64
	thinkingSeq   atomic.Int64
	activeMsgID   atomic.Value
	activeMsgRole atomic.Value
	activeThinkingID atomic.Value
}

func NewConverter(threadID, runID string) *Converter {
	c := &Converter{
		threadID: threadID,
		runID:    runID,
	}
	c.activeMsgID.Store("")
	c.activeMsgRole.Store("")
	c.activeThinkingID.Store("")
	return c
}

func NewConverterWithParent(threadID, runID, parentRunID string) *Converter {
	c := &Converter{
		threadID:    threadID,
		runID:       runID,
		parentRunID: parentRunID,
	}
	c.activeMsgID.Store("")
	c.activeMsgRole.Store("")
	c.activeThinkingID.Store("")
	return c
}

func (c *Converter) nextMsgID() string {
	return fmt.Sprintf("msg_%d", c.msgSeq.Add(1))
}

func (c *Converter) nextToolCallID() string {
	return fmt.Sprintf("tc_%d", c.toolSeq.Add(1))
}

func (c *Converter) nextThinkingID() string {
	return fmt.Sprintf("thinking_%d", c.thinkingSeq.Add(1))
}

func tsNano(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e6
}

func baseEvent(typ EventType, t time.Time) BaseEvent {
	return BaseEvent{
		Type:      typ,
		Timestamp: tsNano(t),
	}
}

func (c *Converter) RunStarted(t time.Time) RunStartedEvent {
	return RunStartedEvent{
		BaseEvent:   baseEvent(EventRunStarted, t),
		ThreadID:    c.threadID,
		RunID:       c.runID,
		ParentRunID: c.parentRunID,
	}
}

func (c *Converter) RunFinished(t time.Time) RunFinishedEvent {
	return RunFinishedEvent{
		BaseEvent: baseEvent(EventRunFinished, t),
		ThreadID:  c.threadID,
		RunID:     c.runID,
	}
}

func (c *Converter) RunFinishedWithInterrupts(t time.Time, interrupts []Interrupt) RunFinishedEvent {
	return RunFinishedEvent{
		BaseEvent: baseEvent(EventRunFinished, t),
		ThreadID:  c.threadID,
		RunID:     c.runID,
		Outcome: &RunFinishedOutcome{
			Type:       "interrupt",
			Interrupts: interrupts,
		},
	}
}

func (c *Converter) RunFinishedWithSuccess(t time.Time, result any) RunFinishedEvent {
	return RunFinishedEvent{
		BaseEvent: baseEvent(EventRunFinished, t),
		ThreadID:  c.threadID,
		RunID:     c.runID,
		Result:    result,
		Outcome:   &RunFinishedOutcome{Type: "success"},
	}
}

func (c *Converter) RunError(t time.Time, err error) RunErrorEvent {
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	return RunErrorEvent{
		BaseEvent: baseEvent(EventRunError, t),
		ThreadID:  c.threadID,
		RunID:     c.runID,
		Message:   msg,
	}
}

func (c *Converter) CloseMessage(t time.Time) []any {
	prevID := c.activeMsgID.Load().(string)
	if prevID == "" {
		return nil
	}
	c.activeMsgID.Store("")
	c.activeMsgRole.Store("")
	return []any{TextMessageEndEvent{
		BaseEvent: baseEvent(EventTextMessageEnd, t),
		MessageID: prevID,
	}}
}

func (c *Converter) CloseThinking(t time.Time) []any {
	prevID := c.activeThinkingID.Load().(string)
	if prevID == "" {
		return nil
	}
	c.activeThinkingID.Store("")
	return []any{ThinkingEndEvent{
		BaseEvent:  baseEvent(EventThinkingEnd, t),
		ThinkingID: prevID,
	}}
}

func (c *Converter) closeAll(t time.Time) []any {
	var events []any
	if tail := c.CloseMessage(t); tail != nil {
		events = append(events, tail...)
	}
	if tail := c.CloseThinking(t); tail != nil {
		events = append(events, tail...)
	}
	return events
}

func (c *Converter) StateSnapshot(t time.Time, state any) StateSnapshotEvent {
	return StateSnapshotEvent{
		BaseEvent: baseEvent(EventStateSnapshot, t),
		Snapshot:  state,
	}
}

func (c *Converter) StateDelta(t time.Time, ops []jsonPatchOp) StateDeltaEvent {
	return StateDeltaEvent{
		BaseEvent: baseEvent(EventStateDelta, t),
		Delta:     ops,
	}
}

func (c *Converter) Convert(e agentcore.Event) []any {
	switch ev := e.(type) {
	case agentcore.AgentStartEvent:
		return []any{c.RunStarted(ev.EventTime())}
	case *agentcore.AgentStartEvent:
		return []any{c.RunStarted(ev.EventTime())}

	case agentcore.AgentEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.RunFinishedWithSuccess(ev.EventTime(), ev.Output))
		return events
	case *agentcore.AgentEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.RunFinishedWithSuccess(ev.EventTime(), ev.Output))
		return events

	case agentcore.AgentErrorEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.RunError(ev.EventTime(), ev.Err))
		return events
	case *agentcore.AgentErrorEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.RunError(ev.EventTime(), ev.Err))
		return events

	case agentcore.TurnStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, StepStartedEvent{
			BaseEvent: baseEvent(EventStepStarted, ev.EventTime()),
			StepName:  fmt.Sprintf("turn_%d", ev.Turn),
		})
		return events
	case *agentcore.TurnStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, StepStartedEvent{
			BaseEvent: baseEvent(EventStepStarted, ev.EventTime()),
			StepName:  fmt.Sprintf("turn_%d", ev.Turn),
		})
		return events

	case agentcore.TurnEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, StepFinishedEvent{
			BaseEvent: baseEvent(EventStepFinished, ev.EventTime()),
			StepName:  fmt.Sprintf("turn_%d", ev.Turn),
		})
		return events
	case *agentcore.TurnEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, StepFinishedEvent{
			BaseEvent: baseEvent(EventStepFinished, ev.EventTime()),
			StepName:  fmt.Sprintf("turn_%d", ev.Turn),
		})
		return events

	case agentcore.MessageDeltaEvent:
		return c.convertMessageDelta(ev.EventTime(), ev.Delta, ev.Kind)
	case *agentcore.MessageDeltaEvent:
		return c.convertMessageDelta(ev.EventTime(), ev.Delta, ev.Kind)

	case agentcore.ToolCallStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.convertToolCallStart(ev.EventTime(), ev.ToolCall)...)
		return events
	case *agentcore.ToolCallStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.convertToolCallStart(ev.EventTime(), ev.ToolCall)...)
		return events

	case agentcore.ToolCallEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.convertToolCallEnd(ev.EventTime(), ev.ToolCallID, ev.ToolName, ev.Result, ev.Err)...)
		return events
	case *agentcore.ToolCallEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, c.convertToolCallEnd(ev.EventTime(), ev.ToolCallID, ev.ToolName, ev.Result, ev.Err)...)
		return events

	case agentcore.HandoffStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "handoff_start",
			Value: map[string]any{
				"source_agent": ev.SourceAgent,
				"target_agent": ev.TargetAgent,
				"mode":         ev.Mode,
				"context":      ev.Context,
			},
		})
		return events
	case *agentcore.HandoffStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "handoff_start",
			Value: map[string]any{
				"source_agent": ev.SourceAgent,
				"target_agent": ev.TargetAgent,
				"mode":         ev.Mode,
				"context":      ev.Context,
			},
		})
		return events

	case agentcore.HandoffEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "handoff_end",
			Value: map[string]any{
				"target_agent": ev.TargetAgent,
				"output":       ev.Output,
				"duration_ms":  ev.Duration.Milliseconds(),
			},
		})
		return events
	case *agentcore.HandoffEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "handoff_end",
			Value: map[string]any{
				"target_agent": ev.TargetAgent,
				"output":       ev.Output,
				"duration_ms":  ev.Duration.Milliseconds(),
			},
		})
		return events

	case agentcore.CompactionStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "compaction_start",
			Value: map[string]any{
				"tokens_before":  ev.TokensBefore,
				"context_window": ev.ContextWindow,
			},
		})
		return events
	case *agentcore.CompactionStartEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "compaction_start",
			Value: map[string]any{
				"tokens_before":  ev.TokensBefore,
				"context_window": ev.ContextWindow,
			},
		})
		return events

	case agentcore.CompactionEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "compaction_end",
			Value: map[string]any{
				"tokens_before": ev.TokensBefore,
				"tokens_after":  ev.TokensAfter,
				"messages_cut":  ev.MessagesCut,
				"duration_ms":   ev.Duration.Milliseconds(),
			},
		})
		return events
	case *agentcore.CompactionEndEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "compaction_end",
			Value: map[string]any{
				"tokens_before": ev.TokensBefore,
				"tokens_after":  ev.TokensAfter,
				"messages_cut":  ev.MessagesCut,
				"duration_ms":   ev.Duration.Milliseconds(),
			},
		})
		return events

	case agentcore.AutoRetryEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "auto_retry",
			Value: map[string]any{
				"attempt":     ev.Attempt,
				"max_retries": ev.MaxRetries,
				"delay_ms":    ev.Delay.Milliseconds(),
			},
		})
		return events
	case *agentcore.AutoRetryEvent:
		var events []any
		events = append(events, c.closeAll(ev.EventTime())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, ev.EventTime()),
			Name:      "auto_retry",
			Value: map[string]any{
				"attempt":     ev.Attempt,
				"max_retries": ev.MaxRetries,
				"delay_ms":    ev.Delay.Milliseconds(),
			},
		})
		return events

	default:
		var events []any
		events = append(events, c.closeAll(time.Now())...)
		events = append(events, CustomEvent{
			BaseEvent: baseEvent(EventCustom, time.Now()),
			Name:      string(e.EventKind()),
			Value:     e,
		})
		return events
	}
}

func (c *Converter) convertMessageDelta(t time.Time, delta string, kind agentcore.BlockKind) []any {
	if kind == agentcore.BlockKindThinking {
		return c.convertThinkingDelta(t, delta)
	}
	var events []any
	if tail := c.CloseThinking(t); tail != nil {
		events = append(events, tail...)
	}
	prevID := c.activeMsgID.Load().(string)
	if prevID == "" {
		msgID := c.nextMsgID()
		c.activeMsgID.Store(msgID)
		c.activeMsgRole.Store("assistant")
		events = append(events,
			TextMessageStartEvent{
				BaseEvent: baseEvent(EventTextMessageStart, t),
				MessageID: msgID,
				Role:      "assistant",
			},
			TextMessageContentEvent{
				BaseEvent: baseEvent(EventTextMessageContent, t),
				MessageID: msgID,
				Delta:     delta,
			},
		)
		return events
	}
	events = append(events, TextMessageContentEvent{
		BaseEvent: baseEvent(EventTextMessageContent, t),
		MessageID: prevID,
		Delta:     delta,
	})
	return events
}

func (c *Converter) convertThinkingDelta(t time.Time, delta string) []any {
	prevID := c.activeThinkingID.Load().(string)
	if prevID == "" {
		thinkingID := c.nextThinkingID()
		msgID := c.nextMsgID()
		c.activeThinkingID.Store(thinkingID)
		return []any{
			ThinkingStartEvent{
				BaseEvent:  baseEvent(EventThinkingStart, t),
				ThinkingID: thinkingID,
			},
			ThinkingTextMessageStartEvent{
				BaseEvent:  baseEvent(EventThinkingTextMessageStart, t),
				ThinkingID: thinkingID,
				MessageID:  msgID,
			},
			ThinkingTextMessageContentEvent{
				BaseEvent:  baseEvent(EventThinkingTextMessageContent, t),
				ThinkingID: thinkingID,
				MessageID:  msgID,
				Delta:      delta,
			},
		}
	}
	msgID := fmt.Sprintf("thinking_msg_%d", c.msgSeq.Add(1))
	return []any{ThinkingTextMessageContentEvent{
		BaseEvent:  baseEvent(EventThinkingTextMessageContent, t),
		ThinkingID: prevID,
		MessageID:  msgID,
		Delta:      delta,
	}}
}

func (c *Converter) convertToolCallStart(t time.Time, tc agentcore.ToolCall) []any {
	return []any{
		ToolCallStartEvent{
			BaseEvent:    baseEvent(EventToolCallStart, t),
			ToolCallID:   tc.ID,
			ToolCallName: tc.Name,
		},
		ToolCallArgsEvent{
			BaseEvent:  baseEvent(EventToolCallArgs, t),
			ToolCallID: tc.ID,
			Delta:      tc.Arguments,
		},
	}
}

func (c *Converter) convertToolCallEnd(t time.Time, toolCallID, toolName, result string, err error) []any {
	events := []any{
		ToolCallEndEvent{
			BaseEvent:  baseEvent(EventToolCallEnd, t),
			ToolCallID: toolCallID,
		},
	}
	if err != nil {
		result = err.Error()
	}
	events = append(events, ToolCallResultEvent{
		BaseEvent:  baseEvent(EventToolCallResult, t),
		MessageID:  fmt.Sprintf("tool_result_%s", toolCallID),
		ToolCallID: toolCallID,
		Content:    result,
		Role:       "tool",
	})
	return events
}

func MessagesFromAgent(msgs []agentcore.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		agMsg := Message{
			ID:      m.ID,
			Role:    convertRole(m.Role),
			Content: m.Content,
			Name:    m.Name,
		}
		if m.ToolCallID != "" {
			agMsg.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			agMsg.ToolCalls = append(agMsg.ToolCalls, ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		out = append(out, agMsg)
	}
	return out
}

func convertRole(r agentcore.Role) MessageRole {
	switch r {
	case agentcore.RoleUser:
		return MessageRoleUser
	case agentcore.RoleAssistant:
		return MessageRoleAssistant
	case agentcore.RoleSystem:
		return MessageRoleSystem
	case agentcore.RoleTool:
		return MessageRoleTool
	default:
		return MessageRoleDeveloper
	}
}

func CapabilitiesFromConfig(cfg agentcore.Config) AgentCapabilities {
	caps := AgentCapabilities{
		Identity: &IdentityCapabilities{
			Name:        cfg.Name,
			Type:        "covonaut",
			Description: cfg.SystemPrompt,
		},
		Transport: &TransportCapabilities{
			Streaming: true,
		},
		Tools: &ToolsCapabilities{
			Supported:      len(cfg.Tools) > 0,
			ParallelCalls:  cfg.ExecutionConfig.Concurrency > 1,
			ClientProvided: false,
		},
		State: &StateCapabilities{
			Snapshots:       true,
			Deltas:          true,
			PersistentState: cfg.Store != nil,
		},
		MultiAgent: &MultiAgentCapabilities{
			Supported:  len(cfg.Handoffs) > 0,
			Delegation: len(cfg.Handoffs) > 0,
			Handoffs:   len(cfg.Handoffs) > 0,
		},
		Execution: &ExecutionCapabilities{
			MaxIterations: cfg.ExecutionConfig.MaxTurns,
		},
		HumanInTheLoop: &HumanInTheLoopCapabilities{
			Supported:  true,
			Approvals:  true,
			Interrupts: true,
		},
	}

	if len(cfg.Tools) > 0 {
		items := make([]ToolDef, 0, len(cfg.Tools))
		for _, t := range cfg.Tools {
			items = append(items, ToolDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		caps.Tools.Items = items
	}

	if len(cfg.Handoffs) > 0 {
		subs := make([]SubAgentDescriptor, 0, len(cfg.Handoffs))
		for _, h := range cfg.Handoffs {
			subs = append(subs, SubAgentDescriptor{
				Name:        h.Name,
				Description: h.Description,
			})
		}
		caps.MultiAgent.SubAgents = subs
	}

	if cfg.Thinking != nil {
		caps.Reasoning = &ReasoningCapabilities{
			Supported: true,
			Streaming: cfg.Streaming,
		}
	}

	return caps
}
