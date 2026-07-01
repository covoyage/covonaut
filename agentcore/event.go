package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/pkg/util"
	"github.com/covoyage/covonaut/skill"
)

type EventType string

const (
	EventAgentStart      EventType = "agent_start"
	EventAgentEnd        EventType = "agent_end"
	EventAgentError      EventType = "agent_error"
	EventSkillLoaded     EventType = "skill_loaded"
	EventSkillsReloaded  EventType = "skills_reloaded"
	EventTurnStart       EventType = "turn_start"
	EventTurnEnd         EventType = "turn_end"
	EventMessageDelta    EventType = "message_delta"
	EventToolCallStart   EventType = "tool_call_start"
	EventToolCallEnd     EventType = "tool_call_end"
	EventHandoffStart    EventType = "handoff_start"
	EventHandoffEnd      EventType = "handoff_end"
	EventCompactionStart EventType = "compaction_start"
	EventCompactionEnd   EventType = "compaction_end"
	EventAutoRetry       EventType = "auto_retry"
	EventAgentInterrupt  EventType = "agent_interrupt"
)

// Event is the common interface for all agent lifecycle events.
type Event interface {
	EventKind() EventType
	EventTime() time.Time
}

type baseEvent struct {
	Kind EventType `json:"type"`
	At   time.Time `json:"timestamp"`
}

func (e baseEvent) EventKind() EventType { return e.Kind }
func (e baseEvent) EventTime() time.Time { return e.At }

func newBase(t EventType) baseEvent {
	return baseEvent{Kind: t, At: time.Now()}
}

type AgentStartEvent struct {
	baseEvent
	AgentName string `json:"agent_name,omitempty"`
	Input     string `json:"input,omitempty"`
}

type AgentEndEvent struct {
	baseEvent
	AgentName string `json:"agent_name,omitempty"`
	Output    string `json:"output"`
}

type AgentErrorEvent struct {
	baseEvent
	Err error `json:"error"`
}

type AgentInterruptEvent struct {
	baseEvent
	AgentName string          `json:"agent_name,omitempty"`
	Reason    *InterruptReason `json:"reason,omitempty"`
}

type SkillLoadedEvent struct {
	baseEvent
	SkillName string `json:"skill_name"`
	Path      string `json:"path,omitempty"`
	Source    string `json:"source"`
	Arguments string `json:"arguments,omitempty"`
}

type SkillsReloadedEvent struct {
	baseEvent
	SkillPaths         []string           `json:"skill_paths,omitempty"`
	TotalSkills        int                `json:"total_skills"`
	VisibleSkills      int                `json:"visible_skills"`
	HiddenSkills       int                `json:"hidden_skills"`
	DiagnosticsCount   int                `json:"diagnostics_count"`
	AddedSkills        []string           `json:"added_skills,omitempty"`
	RemovedSkills      []string           `json:"removed_skills,omitempty"`
	UpdatedSkills      []string           `json:"updated_skills,omitempty"`
	AddedDiagnostics   []skill.Diagnostic `json:"added_diagnostics,omitempty"`
	RemovedDiagnostics []skill.Diagnostic `json:"removed_diagnostics,omitempty"`
}

func NewSkillsReloadedEvent(
	skillPaths []string,
	totalSkills, visibleSkills, hiddenSkills, diagnosticsCount int,
	addedSkills, removedSkills, updatedSkills []string,
	addedDiagnostics, removedDiagnostics []skill.Diagnostic,
) SkillsReloadedEvent {
	return SkillsReloadedEvent{
		baseEvent:          newBase(EventSkillsReloaded),
		SkillPaths:         append([]string(nil), skillPaths...),
		TotalSkills:        totalSkills,
		VisibleSkills:      visibleSkills,
		HiddenSkills:       hiddenSkills,
		DiagnosticsCount:   diagnosticsCount,
		AddedSkills:        append([]string(nil), addedSkills...),
		RemovedSkills:      append([]string(nil), removedSkills...),
		UpdatedSkills:      append([]string(nil), updatedSkills...),
		AddedDiagnostics:   append([]skill.Diagnostic(nil), addedDiagnostics...),
		RemovedDiagnostics: append([]skill.Diagnostic(nil), removedDiagnostics...),
	}
}

type TurnStartEvent struct {
	baseEvent
	Turn int64 `json:"turn"`
}

type TurnEndEvent struct {
	baseEvent
	Turn  int64      `json:"turn"`
	Usage TokenUsage `json:"usage"`
}

type MessageDeltaEvent struct {
	baseEvent
	Delta string    `json:"delta"`
	Kind  BlockKind `json:"kind,omitempty"`
}

type ToolCallStartEvent struct {
	baseEvent
	ToolCall ToolCall `json:"tool_call"`
}

type ToolCallEndEvent struct {
	baseEvent
	ToolCallID string        `json:"tool_call_id"`
	ToolName   string        `json:"tool_name"`
	Result     string        `json:"result"`
	Err        error         `json:"error,omitempty"`
	Duration   time.Duration `json:"duration"`
}

type HandoffStartEvent struct {
	baseEvent
	SourceAgent string `json:"source_agent"`
	TargetAgent string `json:"target_agent"`
	Mode        string `json:"mode"`
	Context     string `json:"context"`
}

type HandoffEndEvent struct {
	baseEvent
	TargetAgent string        `json:"target_agent"`
	Output      string        `json:"output"`
	Duration    time.Duration `json:"duration"`
	Err         error         `json:"error,omitempty"`
}

type CompactionStartEvent struct {
	baseEvent
	TokensBefore  int64 `json:"tokens_before"`
	ContextWindow int64 `json:"context_window"`
}

type CompactionEndEvent struct {
	baseEvent
	TokensBefore int64         `json:"tokens_before"`
	TokensAfter  int64         `json:"tokens_after"`
	MessagesCut  int64         `json:"messages_cut"`
	Duration     time.Duration `json:"duration"`
}

type AutoRetryEvent struct {
	baseEvent
	Attempt    int64         `json:"attempt"`
	MaxRetries int64         `json:"max_retries"`
	Delay      time.Duration `json:"delay"`
	Err        error         `json:"error"`
}

// --- JSON serialization for events with error fields ---

func (e AgentErrorEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type      EventType `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Error     string    `json:"error"`
		ErrorType string    `json:"error_type,omitempty"`
	}{e.Kind, e.At, util.ErrorString(e.Err), errorType(e.Err)})
}

func (e *AgentErrorEvent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type      EventType `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Error     string    `json:"error"`
		ErrorType string    `json:"error_type,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Kind = raw.Type
	e.At = raw.Timestamp
	if raw.Error != "" {
		e.Err = reconstructError(raw.Error, raw.ErrorType)
	}
	return nil
}

func (e ToolCallEndEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       EventType     `json:"type"`
		Timestamp  time.Time     `json:"timestamp"`
		ToolCallID string        `json:"tool_call_id"`
		ToolName   string        `json:"tool_name"`
		Result     string        `json:"result"`
		Duration   time.Duration `json:"duration"`
		Error      string        `json:"error,omitempty"`
		ErrorType  string        `json:"error_type,omitempty"`
	}{e.Kind, e.At, e.ToolCallID, e.ToolName, e.Result, e.Duration, util.ErrorString(e.Err), errorType(e.Err)})
}

func (e *ToolCallEndEvent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type       EventType     `json:"type"`
		Timestamp  time.Time     `json:"timestamp"`
		ToolCallID string        `json:"tool_call_id"`
		ToolName   string        `json:"tool_name"`
		Result     string        `json:"result"`
		Duration   time.Duration `json:"duration"`
		Error      string        `json:"error,omitempty"`
		ErrorType  string        `json:"error_type,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Kind = raw.Type
	e.At = raw.Timestamp
	e.ToolCallID = raw.ToolCallID
	e.ToolName = raw.ToolName
	e.Result = raw.Result
	e.Duration = raw.Duration
	if raw.Error != "" {
		e.Err = reconstructError(raw.Error, raw.ErrorType)
	}
	return nil
}

func (e HandoffEndEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type        EventType     `json:"type"`
		Timestamp   time.Time     `json:"timestamp"`
		TargetAgent string        `json:"target_agent"`
		Output      string        `json:"output"`
		Duration    time.Duration `json:"duration"`
		Error       string        `json:"error,omitempty"`
		ErrorType   string        `json:"error_type,omitempty"`
	}{e.Kind, e.At, e.TargetAgent, e.Output, e.Duration, util.ErrorString(e.Err), errorType(e.Err)})
}

func (e *HandoffEndEvent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type        EventType     `json:"type"`
		Timestamp   time.Time     `json:"timestamp"`
		TargetAgent string        `json:"target_agent"`
		Output      string        `json:"output"`
		Duration    time.Duration `json:"duration"`
		Error       string        `json:"error,omitempty"`
		ErrorType   string        `json:"error_type,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Kind = raw.Type
	e.At = raw.Timestamp
	e.TargetAgent = raw.TargetAgent
	e.Output = raw.Output
	e.Duration = raw.Duration
	if raw.Error != "" {
		e.Err = reconstructError(raw.Error, raw.ErrorType)
	}
	return nil
}

func (e AutoRetryEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       EventType     `json:"type"`
		Timestamp  time.Time     `json:"timestamp"`
		Attempt    int64         `json:"attempt"`
		MaxRetries int64         `json:"max_retries"`
		Delay      time.Duration `json:"delay"`
		Error      string        `json:"error"`
		ErrorType  string        `json:"error_type,omitempty"`
	}{e.Kind, e.At, e.Attempt, e.MaxRetries, e.Delay, util.ErrorString(e.Err), errorType(e.Err)})
}

func (e *AutoRetryEvent) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type       EventType     `json:"type"`
		Timestamp  time.Time     `json:"timestamp"`
		Attempt    int64         `json:"attempt"`
		MaxRetries int64         `json:"max_retries"`
		Delay      time.Duration `json:"delay"`
		Error      string        `json:"error"`
		ErrorType  string        `json:"error_type,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Kind = raw.Type
	e.At = raw.Timestamp
	e.Attempt = raw.Attempt
	e.MaxRetries = raw.MaxRetries
	e.Delay = raw.Delay
	if raw.Error != "" {
		e.Err = reconstructError(raw.Error, raw.ErrorType)
	}
	return nil
}

// errorType returns a stable type identifier for an error.
func errorType(err error) string {
	if err == nil {
		return ""
	}
	switch err {
	case context.Canceled:
		return "context.Canceled"
	case context.DeadlineExceeded:
		return "context.DeadlineExceeded"
	}
	return ""
}

// reconstructError rebuilds an error from its string representation and type hint.
func reconstructError(msg, errType string) error {
	switch errType {
	case "context.Canceled":
		return context.Canceled
	case "context.DeadlineExceeded":
		return context.DeadlineExceeded
	}
	return errors.New(msg)
}

// EventHandler is a callback invoked when an event is emitted.
type EventHandler func(Event)

// EventBus provides async pub/sub for agent lifecycle events.
// Events are dispatched via a buffered channel to avoid blocking the agent loop.
// Event ordering is preserved — a single goroutine processes events sequentially.
//
// Handlers are keyed by a monotonic ID so they can be removed individually
// (Go func values can't be compared with ==). This matters for long-lived
// agents whose EventBus is reused across requests: a per-request handler
// (e.g. an SSE writer closure) must be unregistered when the request ends,
// otherwise it leaks and keeps writing to a stale sink on subsequent requests.
type EventBus struct {
	mu       sync.RWMutex
	handlers map[EventType]map[uint64]EventHandler
	global   map[uint64]EventHandler
	nextID   atomic.Uint64
	ch       chan Event
	done     chan struct{}
	closed   bool
}

const defaultEventBufSize = 256

func NewEventBus() *EventBus {
	eb := &EventBus{
		handlers: make(map[EventType]map[uint64]EventHandler),
		global:   make(map[uint64]EventHandler),
		ch:       make(chan Event, defaultEventBufSize),
		done:     make(chan struct{}),
	}
	go eb.dispatch()
	return eb
}

func (eb *EventBus) dispatch() {
	defer close(eb.done)
	for e := range eb.ch {
		if ds, ok := e.(*drainSentinel); ok {
			close(ds.ack)
			continue
		}

		eb.mu.RLock()
		globals := make([]EventHandler, 0, len(eb.global))
		for _, h := range eb.global {
			globals = append(globals, h)
		}
		typedMap := eb.handlers[e.EventKind()]
		typed := make([]EventHandler, 0, len(typedMap))
		for _, h := range typedMap {
			typed = append(typed, h)
		}
		eb.mu.RUnlock()

		for _, h := range globals {
			eb.safeCall(h, e)
		}
		for _, h := range typed {
			eb.safeCall(h, e)
		}
	}
}

// safeCall invokes a handler with panic recovery. A panicking handler is
// logged to stderr but does NOT kill the dispatch goroutine — without this
// guard, a single buggy handler would permanently take down the event bus
// (dispatch exits, close(done) fires, all subsequent events are silently
// dropped once the channel fills). Other handlers and subsequent events
// must continue to flow.
func (eb *EventBus) safeCall(h EventHandler, e Event) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "agentcore: event handler panicked (event=%s): %v\n%s\n", e.EventKind(), r, debugStack())
		}
	}()
	h(e)
}

// debugStack returns a short stack trace for panic diagnostics.
func debugStack() string {
	buf := make([]byte, 2048)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

// On registers a handler for a specific event type and returns a function
// that removes the handler when called. Callers that attach scoped handlers
// (e.g. per-request SSE writers on a long-lived agent) MUST invoke the
// returned function when their scope ends — otherwise the handler stays
// registered on a reused agent and leaks, writing to a dead/stale sink.
func (eb *EventBus) On(t EventType, h EventHandler) func() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	id := eb.nextID.Add(1)
	if eb.handlers[t] == nil {
		eb.handlers[t] = make(map[uint64]EventHandler)
	}
	eb.handlers[t][id] = h
	return func() { eb.offID(t, id) }
}

// OnAll registers a handler that receives every event and returns a function
// that removes the handler when called. See On for the scoping contract.
func (eb *EventBus) OnAll(h EventHandler) func() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	id := eb.nextID.Add(1)
	eb.global[id] = h
	return func() { eb.offAllID(id) }
}

// offID removes a typed handler by its registration ID. Idempotent.
func (eb *EventBus) offID(t EventType, id uint64) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.handlers[t], id)
}

// offAllID removes a global handler by its registration ID. Idempotent.
func (eb *EventBus) offAllID(id uint64) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.global, id)
}

// Emit dispatches an event to the async processing goroutine.
// Non-blocking: if the buffer is full the event is dropped to avoid blocking
// the caller (typically the agent main loop). Dropped events are preferable to
// a stalled agent — consumers that need guaranteed delivery should use a
// persistent store or checkpoint mechanism instead.
func (eb *EventBus) Emit(e Event) {
	eb.mu.RLock()
	closed := eb.closed
	eb.mu.RUnlock()
	if closed {
		return
	}
	select {
	case eb.ch <- e:
	default:
		// Buffer full — drop event rather than block the agent loop.
	}
}

// Close shuts down the event bus. All queued events are processed before Close returns.
func (eb *EventBus) Close() {
	eb.mu.Lock()
	if eb.closed {
		eb.mu.Unlock()
		return
	}
	eb.closed = true
	eb.mu.Unlock()
	close(eb.ch)
	<-eb.done
}

// Drain blocks until all currently queued events have been processed.
// If the channel buffer is full, it spins briefly to ensure the sentinel
// is eventually enqueued rather than blocking the caller indefinitely.
// If the EventBus has been closed, Drain returns immediately without panic.
func (eb *EventBus) Drain() {
	ack := make(chan struct{})
	ds := &drainSentinel{baseEvent: newBase("drain"), ack: ack}
	for {
		eb.mu.RLock()
		closed := eb.closed
		eb.mu.RUnlock()
		if closed {
			return
		}
		select {
		case eb.ch <- ds:
			<-ack
			return
		default:
			// Yield to allow the dispatch goroutine to process events.
			runtime.Gosched()
		}
	}
}

// drainSentinel is a special event used by Drain to synchronize.
type drainSentinel struct {
	baseEvent
	ack chan struct{}
}
