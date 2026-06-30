package agui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func makeEvent[T any](eventType agentcore.EventType, extra map[string]any) T {
	m := map[string]any{
		"type":      string(eventType),
		"timestamp": time.Now().Format(time.RFC3339Nano),
	}
	for k, v := range extra {
		m[k] = v
	}
	data, _ := json.Marshal(m)
	var ev T
	json.Unmarshal(data, &ev)
	return ev
}

func TestConverterRunStarted(t *testing.T) {
	c := NewConverter("thread1", "run1")
	ev := c.RunStarted(time.Now())
	if ev.Type != EventRunStarted {
		t.Errorf("expected %s, got %s", EventRunStarted, ev.Type)
	}
	if ev.ThreadID != "thread1" {
		t.Errorf("expected thread1, got %s", ev.ThreadID)
	}
	if ev.RunID != "run1" {
		t.Errorf("expected run1, got %s", ev.RunID)
	}
	if ev.ParentRunID != "" {
		t.Errorf("expected empty parentRunID, got %s", ev.ParentRunID)
	}
}

func TestConverterRunStartedWithParent(t *testing.T) {
	c := NewConverterWithParent("thread1", "run2", "run1")
	ev := c.RunStarted(time.Now())
	if ev.ParentRunID != "run1" {
		t.Errorf("expected 'run1' parentRunID, got %s", ev.ParentRunID)
	}
	if ev.RunID != "run2" {
		t.Errorf("expected 'run2' runID, got %s", ev.RunID)
	}
}

func TestConverterRunFinished(t *testing.T) {
	c := NewConverter("thread1", "run1")
	ev := c.RunFinished(time.Now())
	if ev.Type != EventRunFinished {
		t.Errorf("expected %s, got %s", EventRunFinished, ev.Type)
	}
}

func TestConverterRunError(t *testing.T) {
	c := NewConverter("thread1", "run1")
	ev := c.RunError(time.Now(), nil)
	if ev.Message != "unknown error" {
		t.Errorf("expected 'unknown error', got %s", ev.Message)
	}
}

func TestConvertAgentStartEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.AgentStartEvent](agentcore.EventAgentStart, nil)
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	rse, ok := events[0].(RunStartedEvent)
	if !ok {
		t.Fatal("expected RunStartedEvent")
	}
	if rse.Type != EventRunStarted {
		t.Errorf("expected %s, got %s", EventRunStarted, rse.Type)
	}
}

func TestConvertAgentEndEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.AgentEndEvent](agentcore.EventAgentEnd, map[string]any{
		"output": "done",
	})
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	_, ok := events[0].(RunFinishedEvent)
	if !ok {
		t.Fatal("expected RunFinishedEvent")
	}
}

func TestConvertAgentErrorEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.AgentErrorEvent](agentcore.EventAgentError, map[string]any{
		"error": "fail",
	})
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ree, ok := events[0].(RunErrorEvent)
	if !ok {
		t.Fatal("expected RunErrorEvent")
	}
	if ree.Message != "fail" {
		t.Errorf("expected 'fail', got %s", ree.Message)
	}
}

func TestConvertTurnStartEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.TurnStartEvent](agentcore.EventTurnStart, map[string]any{
		"turn": int64(3),
	})
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	sse, ok := events[0].(StepStartedEvent)
	if !ok {
		t.Fatal("expected StepStartedEvent")
	}
	if sse.StepName != "turn_3" {
		t.Errorf("expected 'turn_3', got %s", sse.StepName)
	}
}

func TestConvertTurnEndEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.TurnEndEvent](agentcore.EventTurnEnd, map[string]any{
		"turn": int64(1),
	})
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	_, ok := events[0].(StepFinishedEvent)
	if !ok {
		t.Fatal("expected StepFinishedEvent")
	}
}

func TestConvertMessageDeltaEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "hello",
	})
	events := c.Convert(ev)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (START + CONTENT), got %d", len(events))
	}
	start, ok := events[0].(TextMessageStartEvent)
	if !ok {
		t.Fatal("expected TextMessageStartEvent")
	}
	if start.Role != "assistant" {
		t.Errorf("expected 'assistant', got %s", start.Role)
	}
	tmce, ok := events[1].(TextMessageContentEvent)
	if !ok {
		t.Fatal("expected TextMessageContentEvent")
	}
	if tmce.Delta != "hello" {
		t.Errorf("expected 'hello', got %s", tmce.Delta)
	}
	if tmce.MessageID != start.MessageID {
		t.Errorf("message IDs should match: start=%s content=%s", start.MessageID, tmce.MessageID)
	}
}

func TestConvertMessageDeltaStreamWithEnd(t *testing.T) {
	c := NewConverter("t1", "r1")

	delta1 := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "hello",
	})
	events1 := c.Convert(delta1)
	if len(events1) != 2 {
		t.Fatalf("first delta: expected 2 events, got %d", len(events1))
	}
	start, ok := events1[0].(TextMessageStartEvent)
	if !ok {
		t.Fatal("expected TextMessageStartEvent")
	}

	delta2 := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": " world",
	})
	events2 := c.Convert(delta2)
	if len(events2) != 1 {
		t.Fatalf("second delta: expected 1 event, got %d", len(events2))
	}
	content, ok := events2[0].(TextMessageContentEvent)
	if !ok {
		t.Fatal("expected TextMessageContentEvent")
	}
	if content.MessageID != start.MessageID {
		t.Errorf("message IDs should match across deltas")
	}

	endEv := makeEvent[agentcore.AgentEndEvent](agentcore.EventAgentEnd, nil)
	events3 := c.Convert(endEv)
	var endFound bool
	for _, e := range events3 {
		if tmEnd, ok := e.(TextMessageEndEvent); ok {
			endFound = true
			if tmEnd.MessageID != start.MessageID {
				t.Errorf("end message ID should match start: start=%s end=%s", start.MessageID, tmEnd.MessageID)
			}
		}
	}
	if !endFound {
		t.Fatal("expected TextMessageEndEvent when AgentEnd closes the message")
	}
}

func TestConvertMessageDeltaAutoCloseOnToolCall(t *testing.T) {
	c := NewConverter("t1", "r1")

	delta := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "thinking",
	})
	events1 := c.Convert(delta)
	start, _ := events1[0].(TextMessageStartEvent)

	toolStart := makeEvent[agentcore.ToolCallStartEvent](agentcore.EventToolCallStart, map[string]any{
		"tool_call": map[string]any{
			"id":        "tc_1",
			"name":      "search",
			"arguments": `{}`,
		},
	})
	events2 := c.Convert(toolStart)

	var endFound bool
	for _, e := range events2 {
		if tmEnd, ok := e.(TextMessageEndEvent); ok {
			endFound = true
			if tmEnd.MessageID != start.MessageID {
				t.Errorf("end message ID should match start")
			}
		}
	}
	if !endFound {
		t.Fatal("expected TextMessageEndEvent when ToolCallStart closes the message")
	}
}

func TestConvertToolCallStartEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.ToolCallStartEvent](agentcore.EventToolCallStart, map[string]any{
		"tool_call": map[string]any{
			"id":        "tc_1",
			"name":      "search",
			"arguments": `{"q":"test"}`,
		},
	})
	events := c.Convert(ev)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	start, ok := events[0].(ToolCallStartEvent)
	if !ok {
		t.Fatal("expected ToolCallStartEvent")
	}
	if start.ToolCallID != "tc_1" {
		t.Errorf("expected 'tc_1', got %s", start.ToolCallID)
	}
	if start.ToolCallName != "search" {
		t.Errorf("expected 'search', got %s", start.ToolCallName)
	}
	args, ok := events[1].(ToolCallArgsEvent)
	if !ok {
		t.Fatal("expected ToolCallArgsEvent")
	}
	if args.Delta != `{"q":"test"}` {
		t.Errorf("expected arguments, got %s", args.Delta)
	}
}

func TestConvertToolCallEndEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.ToolCallEndEvent](agentcore.EventToolCallEnd, map[string]any{
		"tool_call_id": "tc_1",
		"tool_name":    "search",
		"result":       "found it",
	})
	events := c.Convert(ev)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	end, ok := events[0].(ToolCallEndEvent)
	if !ok {
		t.Fatal("expected ToolCallEndEvent")
	}
	if end.ToolCallID != "tc_1" {
		t.Errorf("expected 'tc_1', got %s", end.ToolCallID)
	}
	result, ok := events[1].(ToolCallResultEvent)
	if !ok {
		t.Fatal("expected ToolCallResultEvent")
	}
	if result.Content != "found it" {
		t.Errorf("expected 'found it', got %s", result.Content)
	}
}

func TestConvertToolCallEndEventWithError(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.ToolCallEndEvent](agentcore.EventToolCallEnd, map[string]any{
		"tool_call_id": "tc_1",
		"tool_name":    "search",
		"result":       "",
		"error":        "timeout",
	})
	events := c.Convert(ev)
	result, ok := events[1].(ToolCallResultEvent)
	if !ok {
		t.Fatal("expected ToolCallResultEvent")
	}
	if result.Content != "timeout" {
		t.Errorf("expected 'timeout', got %s", result.Content)
	}
}

func TestConvertHandoffStartEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.HandoffStartEvent](agentcore.EventHandoffStart, map[string]any{
		"source_agent": "agent_a",
		"target_agent": "agent_b",
		"mode":         "transfer",
	})
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ce, ok := events[0].(CustomEvent)
	if !ok {
		t.Fatal("expected CustomEvent")
	}
	if ce.Name != "handoff_start" {
		t.Errorf("expected 'handoff_start', got %s", ce.Name)
	}
}

func TestConvertCompactionEvents(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.CompactionStartEvent](agentcore.EventCompactionStart, map[string]any{
		"tokens_before":  int64(1000),
		"context_window": int64(8000),
	})
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ce, ok := events[0].(CustomEvent)
	if !ok {
		t.Fatal("expected CustomEvent")
	}
	if ce.Name != "compaction_start" {
		t.Errorf("expected 'compaction_start', got %s", ce.Name)
	}
}

func TestConvertAutoRetryEvent(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.AutoRetryEvent](agentcore.EventAutoRetry, map[string]any{
		"attempt":     int64(2),
		"max_retries": int64(3),
	})
	events := c.Convert(ev)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ce, ok := events[0].(CustomEvent)
	if !ok {
		t.Fatal("expected CustomEvent")
	}
	if ce.Name != "auto_retry" {
		t.Errorf("expected 'auto_retry', got %s", ce.Name)
	}
}

func TestMessagesFromAgent(t *testing.T) {
	msgs := []agentcore.Message{
		{ID: "m1", Role: agentcore.RoleUser, Content: "hello"},
		{ID: "m2", Role: agentcore.RoleAssistant, Content: "hi", ToolCalls: []agentcore.ToolCall{
			{ID: "tc1", Name: "search", Arguments: `{"q":"go"}`},
		}},
		{ID: "m3", Role: agentcore.RoleTool, Content: "result", ToolCallID: "tc1", Name: "search"},
	}
	agMsgs := MessagesFromAgent(msgs)
	if len(agMsgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(agMsgs))
	}
	if agMsgs[0].Role != MessageRoleUser {
		t.Errorf("expected user, got %s", agMsgs[0].Role)
	}
	if agMsgs[1].Role != MessageRoleAssistant {
		t.Errorf("expected assistant, got %s", agMsgs[1].Role)
	}
	if len(agMsgs[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(agMsgs[1].ToolCalls))
	}
	if agMsgs[1].ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected 'search', got %s", agMsgs[1].ToolCalls[0].Function.Name)
	}
	if agMsgs[2].Role != MessageRoleTool {
		t.Errorf("expected tool, got %s", agMsgs[2].Role)
	}
	if agMsgs[2].ToolCallID != "tc1" {
		t.Errorf("expected 'tc1', got %s", agMsgs[2].ToolCallID)
	}
}

func TestCapabilitiesFromConfig(t *testing.T) {
	cfg := agentcore.Config{}
	cfg.Name = "test-agent"
	cfg.SystemPrompt = "You are a test agent"
	cfg.Streaming = true
	cfg.Tools = []*agentcore.Tool{
		{Name: "search", Description: "Search the web"},
	}
	cfg.Handoffs = []agentcore.HandoffConfig{
		{Name: "coder", Description: "Code assistant"},
	}

	caps := CapabilitiesFromConfig(cfg)
	if caps.Identity.Name != "test-agent" {
		t.Errorf("expected 'test-agent', got %s", caps.Identity.Name)
	}
	if !caps.Transport.Streaming {
		t.Error("expected streaming to be true")
	}
	if !caps.Tools.Supported {
		t.Error("expected tools to be supported")
	}
	if len(caps.Tools.Items) != 1 {
		t.Errorf("expected 1 tool, got %d", len(caps.Tools.Items))
	}
	if !caps.MultiAgent.Supported {
		t.Error("expected multi-agent to be supported")
	}
}

func TestBaseEventGetType(t *testing.T) {
	b := BaseEvent{Type: EventRunStarted}
	if b.GetType() != EventRunStarted {
		t.Errorf("expected %s, got %s", EventRunStarted, b.GetType())
	}
}

func TestEventTypeJSONRoundTrip(t *testing.T) {
	ev := RunStartedEvent{
		BaseEvent: BaseEvent{Type: EventRunStarted, Timestamp: 1000.5},
		ThreadID:  "thread1",
		RunID:     "run1",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["type"] != "RUN_STARTED" {
		t.Errorf("expected RUN_STARTED, got %v", parsed["type"])
	}
	if parsed["threadId"] != "thread1" {
		t.Errorf("expected thread1, got %v", parsed["threadId"])
	}
}

func TestExtractEventType(t *testing.T) {
	ev := RunStartedEvent{
		BaseEvent: BaseEvent{Type: EventRunStarted},
		ThreadID:  "t1",
		RunID:     "r1",
	}
	if extractEventType(ev) != "RUN_STARTED" {
		t.Errorf("expected RUN_STARTED, got %s", extractEventType(ev))
	}
}

func TestGenerateID(t *testing.T) {
	id1 := generateID("thread")
	id2 := generateID("thread")
	if id1 == id2 {
		t.Error("expected different IDs")
	}
	if len(id1) == 0 {
		t.Error("expected non-empty ID")
	}
}

func TestRunFinishedWithSuccess(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := c.RunFinishedWithSuccess(time.Now(), "result text")
	if ev.Type != EventRunFinished {
		t.Errorf("expected %s, got %s", EventRunFinished, ev.Type)
	}
	if ev.Result != "result text" {
		t.Errorf("expected 'result text', got %v", ev.Result)
	}
	if ev.Outcome == nil {
		t.Fatal("expected outcome")
	}
	if ev.Outcome.Type != "success" {
		t.Errorf("expected 'success', got %s", ev.Outcome.Type)
	}
}

func TestRunFinishedWithInterrupts(t *testing.T) {
	c := NewConverter("t1", "r1")
	interrupts := []Interrupt{
		{
			ID:         "int-1",
			Reason:     "tool_call",
			Message:    "Approve sendEmail?",
			ToolCallID: "tc-001",
		},
	}
	ev := c.RunFinishedWithInterrupts(time.Now(), interrupts)
	if ev.Outcome == nil {
		t.Fatal("expected outcome")
	}
	if ev.Outcome.Type != "interrupt" {
		t.Errorf("expected 'interrupt', got %s", ev.Outcome.Type)
	}
	if len(ev.Outcome.Interrupts) != 1 {
		t.Fatalf("expected 1 interrupt, got %d", len(ev.Outcome.Interrupts))
	}
	if ev.Outcome.Interrupts[0].ID != "int-1" {
		t.Errorf("expected 'int-1', got %s", ev.Outcome.Interrupts[0].ID)
	}
}

func TestStateSnapshot(t *testing.T) {
	c := NewConverter("t1", "r1")
	state := map[string]any{"key": "value", "count": 42}
	ev := c.StateSnapshot(time.Now(), state)
	if ev.Type != EventStateSnapshot {
		t.Errorf("expected %s, got %s", EventStateSnapshot, ev.Type)
	}
	if ev.Snapshot == nil {
		t.Fatal("expected snapshot")
	}
}

func TestStateDelta(t *testing.T) {
	c := NewConverter("t1", "r1")
	ops := []jsonPatchOp{
		{Op: "replace", Path: "/count", Value: 43},
		{Op: "add", Path: "/new_field", Value: "hello"},
	}
	ev := c.StateDelta(time.Now(), ops)
	if ev.Type != EventStateDelta {
		t.Errorf("expected %s, got %s", EventStateDelta, ev.Type)
	}
	if len(ev.Delta) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ev.Delta))
	}
}

func TestConvertThinkingDelta(t *testing.T) {
	c := NewConverter("t1", "r1")
	ev := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "let me think",
		"kind":  string(agentcore.BlockKindThinking),
	})
	events := c.Convert(ev)
	if len(events) != 3 {
		t.Fatalf("expected 3 events (THINKING_START + TEXT_MESSAGE_START + CONTENT), got %d", len(events))
	}
	ts, ok := events[0].(ThinkingStartEvent)
	if !ok {
		t.Fatal("expected ThinkingStartEvent")
	}
	tms, ok := events[1].(ThinkingTextMessageStartEvent)
	if !ok {
		t.Fatal("expected ThinkingTextMessageStartEvent")
	}
	tmc, ok := events[2].(ThinkingTextMessageContentEvent)
	if !ok {
		t.Fatal("expected ThinkingTextMessageContentEvent")
	}
	if tmc.Delta != "let me think" {
		t.Errorf("expected 'let me think', got %s", tmc.Delta)
	}
	if ts.ThinkingID != tms.ThinkingID || ts.ThinkingID != tmc.ThinkingID {
		t.Error("thinking IDs should match across events")
	}
}

func TestConvertThinkingThenText(t *testing.T) {
	c := NewConverter("t1", "r1")

	thinkingEv := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "reasoning",
		"kind":  string(agentcore.BlockKindThinking),
	})
	events1 := c.Convert(thinkingEv)

	textEv := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "answer",
		"kind":  "",
	})
	events2 := c.Convert(textEv)

	var hasThinkingEnd, hasTextStart bool
	for _, e := range events2 {
		if _, ok := e.(ThinkingEndEvent); ok {
			hasThinkingEnd = true
		}
		if _, ok := e.(TextMessageStartEvent); ok {
			hasTextStart = true
		}
	}
	if !hasThinkingEnd {
		t.Error("expected ThinkingEndEvent when transitioning from thinking to text")
	}
	if !hasTextStart {
		t.Error("expected TextMessageStartEvent when transitioning from thinking to text")
	}
	_ = events1
}

func TestCloseAllClosesBothMessageAndThinking(t *testing.T) {
	c := NewConverter("t1", "r1")

	thinkingEv := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "hmm",
		"kind":  string(agentcore.BlockKindThinking),
	})
	c.Convert(thinkingEv)

	events := c.closeAll(time.Now())
	var hasThinkingEnd bool
	for _, e := range events {
		if _, ok := e.(ThinkingEndEvent); ok {
			hasThinkingEnd = true
		}
	}
	if !hasThinkingEnd {
		t.Error("expected ThinkingEndEvent in closeAll when only thinking is active")
	}
}

func TestCloseAllClosesMessageAfterThinkingTransition(t *testing.T) {
	c := NewConverter("t1", "r1")

	thinkingEv := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "hmm",
		"kind":  string(agentcore.BlockKindThinking),
	})
	c.Convert(thinkingEv)

	textEv := makeEvent[agentcore.MessageDeltaEvent](agentcore.EventMessageDelta, map[string]any{
		"delta": "answer",
		"kind":  "",
	})
	textEvents := c.Convert(textEv)

	var thinkingClosedInTransition bool
	for _, e := range textEvents {
		if _, ok := e.(ThinkingEndEvent); ok {
			thinkingClosedInTransition = true
		}
	}
	if !thinkingClosedInTransition {
		t.Error("expected ThinkingEndEvent during thinking→text transition")
	}

	events := c.closeAll(time.Now())
	var hasMsgEnd bool
	for _, e := range events {
		if _, ok := e.(TextMessageEndEvent); ok {
			hasMsgEnd = true
		}
	}
	if !hasMsgEnd {
		t.Error("expected TextMessageEndEvent in closeAll after thinking→text transition")
	}
}

func TestInterruptJSONRoundTrip(t *testing.T) {
	ev := RunFinishedEvent{
		BaseEvent: BaseEvent{Type: EventRunFinished, Timestamp: 1000},
		ThreadID:  "thread-1",
		RunID:     "run-1",
		Outcome: &RunFinishedOutcome{
			Type: "interrupt",
			Interrupts: []Interrupt{
				{
					ID:             "int-1",
					Reason:         "tool_call",
					Message:        "Approve sendEmail?",
					ToolCallID:     "tc-001",
					ResponseSchema: map[string]any{"type": "object"},
				},
			},
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	outcome, ok := parsed["outcome"].(map[string]any)
	if !ok {
		t.Fatal("expected outcome object")
	}
	if outcome["type"] != "interrupt" {
		t.Errorf("expected 'interrupt', got %v", outcome["type"])
	}
	interrupts, ok := outcome["interrupts"].([]any)
	if !ok || len(interrupts) != 1 {
		t.Fatalf("expected 1 interrupt, got %v", outcome["interrupts"])
	}
}

func TestResumeEntryInInput(t *testing.T) {
	input := RunAgentInput{
		ThreadID: "thread-1",
		RunID:    "run-2",
		Resume: []ResumeEntry{
			{InterruptID: "int-1", Status: "resolved", Payload: map[string]any{"approved": true}},
			{InterruptID: "int-2", Status: "cancelled"},
		},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var parsed RunAgentInput
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Resume) != 2 {
		t.Fatalf("expected 2 resume entries, got %d", len(parsed.Resume))
	}
	if parsed.Resume[0].InterruptID != "int-1" {
		t.Errorf("expected 'int-1', got %s", parsed.Resume[0].InterruptID)
	}
	if parsed.Resume[0].Status != "resolved" {
		t.Errorf("expected 'resolved', got %s", parsed.Resume[0].Status)
	}
	if parsed.Resume[1].Status != "cancelled" {
		t.Errorf("expected 'cancelled', got %s", parsed.Resume[1].Status)
	}
}

func TestCapabilitiesStateAndHITL(t *testing.T) {
	cfg := agentcore.Config{}
	cfg.Name = "test"
	caps := CapabilitiesFromConfig(cfg)
	if !caps.State.Snapshots {
		t.Error("expected state snapshots to be true")
	}
	if !caps.State.Deltas {
		t.Error("expected state deltas to be true")
	}
	if !caps.HumanInTheLoop.Supported {
		t.Error("expected HITL supported")
	}
	if !caps.HumanInTheLoop.Approvals {
		t.Error("expected HITL approvals")
	}
	if !caps.HumanInTheLoop.Interrupts {
		t.Error("expected HITL interrupts")
	}
}
