package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultMaxTurns = 20

// ToolCallOverride controls how a loop-level BeforeToolCall can block or replace
// the execution of a tool call.
type ToolCallOverride struct {
	Block   bool   // if true, skip execution
	Result  string // result to use when blocked (empty = default error message)
	IsError bool   // whether the override result should be treated as an error
}

// Config defines the parameters for constructing an Agent.
//
// Config is composed of embedded sub-configs that group related fields:
//   - ModelConfig:      LLM model selection and generation parameters
//   - SkillConfig:      skill loading, selection, and API control
//   - ExecutionConfig:  execution mode, concurrency, middleware, and hooks
//   - CompactionConfig: context window management and compaction behavior
//
// Because sub-configs are embedded, fields are promoted to the top level:
// you can access c.Model or c.ModelConfig.Model interchangeably.
// Both struct literal construction and functional options (NewConfig) are supported.
type Config struct {
	ModelConfig
	SkillConfig
	ExecutionConfig
	CompactionConfig

	// Top-level agent configuration not belonging to a specific sub-config.
	Tools        []*Tool
	SystemPrompt string

	Store Store // optional: enables SaveState / LoadState
	// Checkpoint optional durable snapshots per thread (see CheckpointSettings).
	Checkpoint *CheckpointSettings

	Handoffs []HandoffConfig // optional: sub-agents reachable via handoff
	Tracer   Tracer          // optional: distributed tracing

	// LLM-level retry with exponential backoff.
	// Context overflow errors trigger compaction instead of retry.
	RetryConfig *RetryConfig

	// TransformContext is called before ConvertToLLM to filter/modify/inject messages.
	TransformContext func(ctx context.Context, msgs []Message) []Message

	// ConvertToLLM converts internal message types to standard LLM messages.
	// If nil, DefaultConvertToLLM is used which strips custom types.
	ConvertToLLM ConvertToLLMFunc

	// BeforeToolCall is invoked before each tool at the agent loop level.
	// Return non-nil ToolCallOverride with Block=true to skip the tool.
	BeforeToolCall func(ctx context.Context, tc ToolCall) *ToolCallOverride

	// AfterToolCall is invoked after each tool at the agent loop level.
	// It can modify the ToolResult before it's added to conversation messages.
	AfterToolCall func(ctx context.Context, tc ToolCall, result *ToolResult) *ToolResult

	// PostProcessResults is invoked after all tools in a turn have executed,
	// before results are persisted to conversation messages. It receives the
	// full batch of calls and results and can modify results in-place.
	// Use this for turn-level processing (e.g., output budget enforcement).
	PostProcessResults func(ctx context.Context, calls []ToolCall, results []ToolResult) []ToolResult

	// Extensions are registered during New() and contribute tools, hooks, etc.
	Extensions []Extension

	// Lifecycle hooks intercept every stage of agent execution.
	// Multiple hooks are composed via LifecycleChain.
	Lifecycle LifecycleHook
}

// Agent is the core runtime that orchestrates LLM calls and tool execution.
type Agent struct {
	config        Config
	configMu      sync.RWMutex
	state         *AgentState
	registry      *Registry
	executor      *Executor
	eventBus      *EventBus
	ownsEventBus  bool
	steering      *messageQueue
	followUp      *messageQueue
	extensions    *ExtensionRegistry
	contextEngine ContextEngine
	engineReg     *EngineRegistry
	interrupted   *InterruptReason
}

func New(cfg Config) *Agent {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = defaultMaxTurns
	}

	reg := NewRegistry()
	reg.Register(cfg.Tools...)

	unknownHandler := cfg.UnknownToolHandler
	if unknownHandler == nil {
		unknownHandler = DynamicUnknownToolHandler(reg)
	}

	execCfg := ExecutorConfig{
		Mode:               cfg.ExecutionMode,
		Concurrency:        cfg.Concurrency,
		Middleware:         cfg.Middleware,
		Before:             cfg.GlobalBefore,
		After:              cfg.GlobalAfter,
		ValidateArguments:  cfg.ValidateArguments,
		UnknownToolHandler: unknownHandler,
	}

	engineReg := NewEngineRegistry()

	var ctxEngine ContextEngine
	if cfg.CustomEngine != nil {
		ctxEngine = cfg.CustomEngine
	} else if cfg.ContextWindow > 0 {
		engineName := cfg.Engine
		if engineName == "" {
			engineName = engineReg.Default()
		}
		engineCfg := ContextEngineConfig{
			Model:               cfg.Model,
			BaseURL:             "",
			APIKey:              "",
			Provider:            cfg.Provider,
			ContextWindow:       cfg.ContextWindow,
			ReserveTokens:       cfg.ReserveTokens,
			KeepRecentTokens:    cfg.KeepRecentTokens,
			ProtectFirstN:       cfg.ProtectFirstN,
			CompressionThreshold: cfg.CompressionThreshold,
			AutoCompactLimit:    cfg.AutoCompactTokenLimit,
			StructuredCompaction: cfg.StructuredCompaction,
			CompressionModel:    cfg.CompressionModel,
			CompressionProvider: cfg.CompressionProvider,
			CompressionBaseURL:  cfg.CompressionBaseURL,
			CompressionAPIKey:   cfg.CompressionAPIKey,
		}
		var err error
		ctxEngine, err = engineReg.Create(engineName, engineCfg)
		if err != nil {
			ctxEngine = engineReg.factories[engineReg.Default()](engineCfg)
		}
	}

	a := &Agent{
		config:        cfg,
		state:         NewState(),
		registry:      reg,
		executor:      NewExecutor(reg, execCfg),
		eventBus:      NewEventBus(),
		ownsEventBus:  true,
		steering:      newMessageQueue(cfg.SteeringMode),
		followUp:      newMessageQueue(cfg.FollowUpMode),
		extensions:    NewExtensionRegistry(),
		contextEngine: ctxEngine,
		engineReg:     engineReg,
	}

	a.registerHandoffs()

	if len(cfg.AvailableSkills) > 0 {
		cfg.Extensions = append(cfg.Extensions, NewSkillExtension(cfg.AvailableSkills, cfg.SelectedSkills))
		a.config = cfg
	}

	if len(cfg.Extensions) > 0 {
		_ = a.extensions.Register(context.Background(), a, cfg.Extensions...)
	}

	return a
}

// --- event subscriptions ---

func (a *Agent) On(t EventType, h EventHandler) func() { return a.eventBus.On(t, h) }
func (a *Agent) OnAll(h EventHandler) func()           { return a.eventBus.OnAll(h) }
func (a *Agent) EmitEvent(e Event)              { a.eventBus.Emit(e) }
func (a *Agent) EmitExtensionSnapshots() {
	for _, e := range a.extensions.SnapshotEvents() {
		a.eventBus.Emit(e)
	}
}

// SetEventBus replaces the agent's event bus (used by sub-agents to forward
// events to a parent's bus). The agent will not close a bus it did not create.
func (a *Agent) SetEventBus(bus *EventBus) {
	a.eventBus = bus
	a.ownsEventBus = false
}

// --- state access ---

func (a *Agent) Config() Config {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.config
}

// SetFastMode enables or disables priority/low-latency API processing.
func (a *Agent) SetFastMode(enabled bool) {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	a.config.FastMode = enabled
}

// ApplyCallConfig updates the agent's Model, Thinking, ResponseFormat, and
// SelectedSkills from the given CallConfig. This is used by the server pool
// to apply thread-level or request-level overrides before reusing a cached agent.
func (a *Agent) ApplyCallConfig(cc *CallConfig) {
	if cc == nil {
		return
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if cc.Model != "" {
		a.config.Model = cc.Model
	}
	if cc.ResponseFormat != nil {
		a.config.ResponseFormat = CloneResponseFormat(cc.ResponseFormat)
	}
	if cc.Thinking != nil {
		a.config.Thinking = CloneThinkingConfig(cc.Thinking)
	}
	if len(cc.Skills) > 0 {
		a.config.SelectedSkills = CloneStringSlice(cc.Skills)
		a.extensions.Visit("skills", func(ext Extension) {
			if s, ok := ext.(interface{ SetSelected([]string) }); ok {
				s.SetSelected(CloneStringSlice(cc.Skills))
			}
		})
	}
}

// SetThinkingConfig updates thinking/reasoning configuration at runtime.
func (a *Agent) SetThinkingConfig(tc *ThinkingConfig) {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	a.config.Thinking = tc
}

func (a *Agent) State() *AgentState { return a.state }

func (a *Agent) lifecycle() LifecycleHook {
	a.configMu.RLock()
	lc := a.config.Lifecycle
	a.configMu.RUnlock()
	return lc
}

func (a *Agent) transformContext() func(ctx context.Context, msgs []Message) []Message {
	a.configMu.RLock()
	fn := a.config.TransformContext
	a.configMu.RUnlock()
	return fn
}

func (a *Agent) systemPrompt() string {
	a.configMu.RLock()
	s := a.config.SystemPrompt
	a.configMu.RUnlock()
	return s
}

// --- tool hot-reload ---

func (a *Agent) RegisterTools(tools ...*Tool)    { a.registry.Register(tools...) }
func (a *Agent) UnregisterTools(names ...string) { a.registry.Unregister(names...) }
func (a *Agent) ToolNames() []string             { return a.registry.Names() }
func (a *Agent) GetTool(name string) (*Tool, bool) { return a.registry.Get(name) }

// --- steering & follow-up ---

// Steer injects a message that will be picked up before the next LLM call.
// Use this to redirect or interrupt the agent mid-conversation.
func (a *Agent) Steer(msg Message) { a.steering.Push(msg) }

// FollowUp queues a message that will be processed after the current
// conversation finishes (no more tool calls). The agent loop restarts
// with the follow-up as new input.
func (a *Agent) FollowUp(msg Message) { a.followUp.Push(msg) }

// --- extensions ---

func (a *Agent) ExtensionNames() []string { return a.extensions.Names() }

// --- context engine ---

// ContextEngine returns the active context engine (nil if compaction is disabled).
func (a *Agent) ContextEngine() ContextEngine {
	return a.contextEngine
}

// RegisterContextEngine registers a custom context engine factory.
func (a *Agent) RegisterContextEngine(name string, factory ContextEngineFactory) {
	a.engineReg.Register(name, factory)
}

// SetContextEngine replaces the active context engine at runtime.
func (a *Agent) SetContextEngine(engine ContextEngine) {
	a.contextEngine = engine
}

// ResetContextEngine clears per-session state for the active engine.
func (a *Agent) ResetContextEngine() {
	if a.contextEngine != nil {
		a.contextEngine.OnSessionReset()
	}
}

// ContextEngineStats returns diagnostics from the active engine.
func (a *Agent) ContextEngineStats() map[string]any {
	if a.contextEngine == nil {
		return nil
	}
	stats := map[string]any{
		"name":             a.contextEngine.Name(),
		"context_length":   a.contextEngine.ContextLength(),
		"threshold_tokens": a.contextEngine.ThresholdTokens(),
		"compression_count": a.contextEngine.CompressionCount(),
		"last_savings_pct": a.contextEngine.LastSavingsPct(),
	}
	if ce, ok := a.contextEngine.(*CompressorEngine); ok {
		stats["details"] = ce.SummaryStats()
	}
	return stats
}

// Close releases all resources held by the agent, including extensions and the
// event bus. Call this when the agent is no longer needed. It is safe to call
// multiple times. After Close, the agent should not be used for further Run
// calls — create a new Agent instead.
func (a *Agent) Close() {
	if a.contextEngine != nil {
		a.contextEngine.OnSessionEnd()
	}
	_ = a.extensions.Dispose()
	if a.ownsEventBus {
		a.eventBus.Close()
	}
}

// --- persistence ---

func (a *Agent) SaveState(ctx context.Context, key string) error {
	if a.config.Store == nil {
		return fmt.Errorf("no store configured")
	}
	return a.config.Store.Save(ctx, key, a.state.Snapshot())
}

func (a *Agent) LoadState(ctx context.Context, key string) error {
	if a.config.Store == nil {
		return fmt.Errorf("no store configured")
	}
	snap, err := a.config.Store.Load(ctx, key)
	if err != nil {
		return err
	}
	a.state.Restore(snap)
	return nil
}

func (a *Agent) checkpointThreadID() string {
	if a.config.Checkpoint == nil || a.config.Checkpoint.ThreadID == "" {
		return "default"
	}
	return a.config.Checkpoint.ThreadID
}

// SaveCheckpoint persists the current StateSnapshot to the configured CheckpointSaver.
func (a *Agent) SaveCheckpoint(ctx context.Context) (int64, error) {
	if a.config.Checkpoint == nil || a.config.Checkpoint.Saver == nil {
		return 0, fmt.Errorf("checkpoint: no saver configured")
	}
	return a.config.Checkpoint.Saver.Append(ctx, a.checkpointThreadID(), a.state.Snapshot())
}

// RestoreLatestCheckpoint loads the latest snapshot for threadID into this agent.
// If threadID is empty, Config.Checkpoint.ThreadID (or "default") is used.
// The restored Status is whatever was saved (e.g. StatusFinished after a completed
// reply, StatusInterrupted after an interrupt); call Resume() to continue from
// an interrupt, or SetStatus(StatusRunning) before Continue for a normal follow-up.
func (a *Agent) RestoreLatestCheckpoint(ctx context.Context, threadID string) error {
	if a.config.Checkpoint == nil || a.config.Checkpoint.Saver == nil {
		return fmt.Errorf("checkpoint: no saver configured")
	}
	tid := threadID
	if tid == "" {
		tid = a.checkpointThreadID()
	}
	snap, _, err := a.config.Checkpoint.Saver.Latest(ctx, tid)
	if err != nil {
		return err
	}
	a.state.Restore(snap)
	a.interrupted = a.state.GetInterruptReason()
	return nil
}

func (a *Agent) appendCheckpoint(ctx context.Context) error {
	if a.config.Checkpoint == nil || a.config.Checkpoint.Saver == nil {
		return nil
	}
	_, err := a.config.Checkpoint.Saver.Append(ctx, a.checkpointThreadID(), a.state.Snapshot())
	return err
}

// --- run ---

// Run starts the agent loop with a new user input.
// The Agent can be reused across multiple Run calls — conversation state is
// preserved between calls and system prompt is only persisted once.
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	ctx, span := a.tracer().Start(ctx, "agent.run",
		Attr("agent.name", a.config.Name),
		Attr("agent.model", a.config.Model),
	)
	defer span.End()
	defer a.eventBus.Drain()

	a.state.SetStatus(StatusRunning)
	a.emit(&AgentStartEvent{
		baseEvent: newBase(EventAgentStart),
		AgentName: a.config.Name,
		Input:     input,
	})

	// Only persist system prompt if not already present in conversation history.
	if sp := a.systemPrompt(); sp != "" && !a.state.HasSystemPrompt() {
		if err := a.persistMessage(ctx, Message{Role: RoleSystem, Content: sp}); err != nil {
			span.RecordError(err)
			return "", WrapNodeError(err, "lifecycle:persist_system")
		}
	}
	if err := a.persistMessage(ctx, Message{Role: RoleUser, Content: input}); err != nil {
		span.RecordError(err)
		return "", WrapNodeError(err, "lifecycle:persist_user")
	}

	// Lifecycle: BeforeAgentRun
	if lc := a.lifecycle(); lc != nil {
		arc := &AgentRunContext{Agent: a, Input: input, Messages: a.state.Messages()}
		if err := lc.BeforeAgentRun(ctx, arc); err != nil {
			span.RecordError(err)
			return "", WrapNodeError(err, "lifecycle:before_agent_run")
		}
	}

	if a.contextEngine != nil {
		a.contextEngine.OnSessionStart(ctx, a.config.Model, a.config.ContextWindow)
	}

	output, err := a.runLoop(ctx)

	// Lifecycle: AfterAgentRun
	if lc := a.lifecycle(); lc != nil {
		arc := &AgentRunContext{Agent: a, Input: input, Messages: a.state.Messages()}
		lc.AfterAgentRun(ctx, arc, output, err)
	}

	if err != nil {
		span.RecordError(err)
	}
	return output, err
}

// Continue resumes the agent loop from the current state without adding new input.
func (a *Agent) Continue(ctx context.Context) (string, error) {
	ctx, span := a.tracer().Start(ctx, "agent.continue",
		Attr("agent.name", a.config.Name),
	)
	defer span.End()
	defer a.eventBus.Drain()

	a.state.SetStatus(StatusRunning)
	a.emit(&AgentStartEvent{
		baseEvent: newBase(EventAgentStart),
		AgentName: a.config.Name,
	})

	output, err := a.runLoop(ctx)
	if err != nil {
		span.RecordError(err)
	}
	return output, err
}

// Interrupted returns the interrupt reason if the agent was interrupted,
// or nil if it completed normally or hasn't run yet.
func (a *Agent) Interrupted() *InterruptReason {
	return a.interrupted
}

// Resume continues execution after an interrupt. The agent must have
// StatusInterrupted (check Interrupted() != nil). It replays the
// conversation from the tool result that triggered the interrupt,
// allowing the LLM to continue naturally.
func (a *Agent) Resume(ctx context.Context) (string, error) {
	ir := a.Interrupted()
	if ir == nil {
		return "", fmt.Errorf("agent is not interrupted (status: %s)", a.state.Status())
	}
	a.interrupted = nil
	a.state.ClearInterruptReason()
	a.state.SetStatus(StatusRunning)
	a.emit(&AgentStartEvent{
		baseEvent: newBase(EventAgentStart),
		AgentName: a.config.Name,
	})
	defer a.eventBus.Drain()
	output, err := a.runLoop(ctx)
	if err != nil {
		return "", WrapNodeError(err, "resume")
	}
	return output, nil
}

// runLoop is the core turn loop shared by Run, Continue, and Resume.
// Outer loop handles follow-up messages; inner loop handles tool call turns.
// MaxTurns is enforced per runLoop invocation (not cumulative across the session).
func (a *Agent) runLoop(ctx context.Context) (string, error) {
	var finalOutput string
	loopStartTurn := a.state.Turn()

	var lastContent string
	var repeatCount int
	var lastToolSignature string
	var toolRepeatCount int

	for {
		// Inner loop: process turns until the model stops calling tools
		for a.state.Status() == StatusRunning {
			turn := a.state.NextTurn()

			if turn-loopStartTurn > a.config.MaxTurns {
				err := NewNodeError("exceeded max turns", ErrExceedMaxSteps, a.config.Name, fmt.Sprintf("turn:%d", turn))
				a.state.SetStatus(StatusError)
				a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: err})
				return "", err
			}

			// Context compaction
			if err := a.maybeCompact(ctx); err != nil {
				ne := NewNodeError("compaction failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn), "compaction")
				a.state.SetStatus(StatusError)
				a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
				return "", ne
			}

			// Inject steering messages before LLM call
			if steered := a.steering.Drain(); len(steered) > 0 {
				for _, msg := range steered {
					if err := a.persistMessage(ctx, msg); err != nil {
						ne := NewNodeError("lifecycle persist steering failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn))
						a.state.SetStatus(StatusError)
						a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
						return "", ne
					}
				}
			}

			if lc := a.lifecycle(); lc != nil {
				arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: turn}
				if err := lc.BeforeTurn(ctx, arc); err != nil {
					ne := NewNodeError("lifecycle before_turn failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn))
					a.state.SetStatus(StatusError)
					a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
					return "", ne
				}
			}

			if err := a.checkpointTurnStart(ctx, turn); err != nil {
				a.state.SetStatus(StatusError)
				a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: err})
				return "", err
			}

			a.emit(&TurnStartEvent{baseEvent: newBase(EventTurnStart), Turn: turn})

			// Build request: TransformContext → ConvertToLLM
			msgs := a.state.Messages()
			if tc := a.transformContext(); tc != nil {
				msgs = tc(ctx, msgs)
			}
			converter := a.config.ConvertToLLM
			if converter == nil {
				converter = DefaultConvertToLLM
			}
			msgs = converter(msgs)

			req := &ProviderRequest{
				Model:          a.config.Model,
				Messages:       msgs,
				Tools:          a.registry.Definitions(),
				Temperature:    a.config.Temperature,
				MaxTokens:      a.config.MaxTokens,
				ResponseFormat: a.config.ResponseFormat,
				Thinking:       a.config.Thinking,
				FastMode:       a.config.FastMode,
			}

			// Lifecycle: BeforeModelCall
			if lc := a.lifecycle(); lc != nil {
				arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: turn}
				mcc := &ModelCallContext{Request: req}
				if lcErr := lc.BeforeModelCall(ctx, arc, mcc); lcErr != nil {
					ne := NewNodeError("lifecycle before_model_call failed", lcErr, a.config.Name, fmt.Sprintf("turn:%d", turn))
					a.state.SetStatus(StatusError)
					a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
					return "", ne
				}
			}

			resp, err := a.callProviderWithRetry(ctx, req)
			if err != nil {
				// Context overflow: attempt compaction then retry once
				if IsContextOverflowError(err) && a.config.ContextWindow > 0 {
					if compErr := a.ForceCompact(ctx); compErr == nil {
						msgs = a.state.Messages()
						if tc := a.transformContext(); tc != nil {
							msgs = tc(ctx, msgs)
						}
						msgs = converter(msgs)
						req.Messages = msgs
						resp, err = a.callProviderWithRetry(ctx, req)
					}
				}
				if err != nil {
					if errors.Is(err, context.Canceled) {
						// User interrupted — emit clean end event instead of cryptic error
						a.state.SetStatus(StatusFinished)
						a.emit(&AgentEndEvent{
							baseEvent: newBase(EventAgentEnd),
							AgentName: a.config.Name,
						})
						return "", nil
					}
					ne := NewNodeError("provider call failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn), "provider")
					a.state.SetStatus(StatusError)
					a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
					return "", ne
				}
			}

			// Lifecycle: AfterModelCall
			if lc := a.lifecycle(); lc != nil {
				arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: turn}
				mcc := &ModelCallContext{Request: req, Response: resp, Err: err}
				lc.AfterModelCall(ctx, arc, mcc)
				if mcc.Err != nil && err == nil {
					if !resp.SuppressPersist {
						if pErr := a.persistMessage(ctx, Message{
							Role:      RoleAssistant,
							Content:   resp.Content,
							Blocks:    resp.Blocks,
							ToolCalls: resp.ToolCalls,
						}); pErr != nil {
							ne := NewNodeError("lifecycle persist assistant failed", pErr, a.config.Name, fmt.Sprintf("turn:%d", turn))
							a.state.SetStatus(StatusError)
							a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
							return "", ne
						}
					}
					if err := a.persistMessage(ctx, Message{
						Role:    RoleSystem,
						Content: fmt.Sprintf("Error: %s", mcc.Err.Error()),
					}); err != nil {
						ne := NewNodeError("lifecycle persist guardrail error failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn))
						a.state.SetStatus(StatusError)
						a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
						return "", ne
					}
					continue
				}
			}

			// Accumulate usage
			if resp.Usage.TotalTokens > 0 {
				a.state.AddUsage(resp.Usage)
				if a.contextEngine != nil {
					a.contextEngine.UpdateFromResponse(resp.Usage)
				}
			}

			if !resp.SuppressPersist {
				if err := a.persistMessage(ctx, Message{
					Role:      RoleAssistant,
					Content:   resp.Content,
					Blocks:    resp.Blocks,
					ToolCalls: resp.ToolCalls,
				}); err != nil {
					ne := NewNodeError("lifecycle persist assistant failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn))
					a.state.SetStatus(StatusError)
					a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
					return "", ne
				}
			}

			if len(resp.ToolCalls) == 0 {
				finalOutput = resp.Content
				a.state.SetStatus(StatusFinished)
				a.emit(&TurnEndEvent{
					baseEvent: newBase(EventTurnEnd),
					Turn:      turn,
					Usage:     resp.Usage,
				})
				if lc := a.lifecycle(); lc != nil {
					arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: turn}
					lc.AfterTurn(ctx, arc, TurnInfo{HadToolCalls: false})
				}
				if err := a.checkpointTurnEnd(ctx, turn); err != nil {
					a.state.SetStatus(StatusError)
					a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: err})
					return "", err
				}
				break
			}

			// Truncation guard: when the provider reports finish_reason="length" the
			// model hit max_tokens and any tool-call arguments may be cut mid-JSON.
			// The executor validates JSON per-call, but a partial batch (some calls
			// valid, some not) leaves the conversation in an inconsistent state.
			// Refuse the entire batch up front and persist error results so the
			// model regenerates with complete output.
			if resp.FinishReason == "length" && hasInvalidToolCallArgs(resp.ToolCalls) {
				for _, tc := range resp.ToolCalls {
					if perr := a.persistMessage(ctx, Message{
						Role:       RoleTool,
						Content:    "Error: this tool call was not executed because the model output was truncated by max_tokens, producing invalid JSON arguments. Regenerate the tool call with complete arguments; if the call is large, split it or reduce output length.",
						ToolCallID: tc.ID,
						Name:       tc.Name,
					}); perr != nil {
						ne := NewNodeError("truncation guard persist failed", perr, a.config.Name, fmt.Sprintf("turn:%d", turn))
						a.state.SetStatus(StatusError)
						a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
						return "", ne
					}
				}
				a.emit(&TurnEndEvent{
					baseEvent: newBase(EventTurnEnd),
					Turn:      turn,
					Usage:     resp.Usage,
				})
				if lc := a.lifecycle(); lc != nil {
					arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: turn}
					lc.AfterTurn(ctx, arc, TurnInfo{HadToolCalls: true})
				}
				if err := a.checkpointTurnEnd(ctx, turn); err != nil {
					a.state.SetStatus(StatusError)
					a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: err})
					return "", err
				}
				continue
			}

			if err := a.executeToolCalls(ctx, resp.ToolCalls); err != nil {
				if IsInterrupt(err) {
					a.state.SetStatus(StatusInterrupted)
					a.state.SetInterruptReason(a.interrupted)
					a.emit(&AgentInterruptEvent{
						baseEvent: newBase(EventAgentInterrupt),
						AgentName: a.config.Name,
						Reason:    a.interrupted,
					})
					return "", nil
				}
				ne := NewNodeError("tool execution persist failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn))
				a.state.SetStatus(StatusError)
				a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
				return "", ne
			}

			// Context cancellation during tool execution — exit cleanly.
			if errors.Is(ctx.Err(), context.Canceled) {
				a.state.SetStatus(StatusFinished)
				a.emit(&AgentEndEvent{
					baseEvent: newBase(EventAgentEnd),
					AgentName: a.config.Name,
				})
				return "", nil
			}
			a.emit(&TurnEndEvent{
				baseEvent: newBase(EventTurnEnd),
				Turn:      turn,
				Usage:     resp.Usage,
			})
			if lc := a.lifecycle(); lc != nil {
				arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: turn}
				lc.AfterTurn(ctx, arc, TurnInfo{HadToolCalls: true})
			}
			if err := a.checkpointTurnEnd(ctx, turn); err != nil {
				a.state.SetStatus(StatusError)
				a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: err})
				return "", err
			}

			// Transfer handoff
			if handoff := a.state.PendingHandoff(); handoff != nil {
				a.state.ClearPendingHandoff()
				return a.handleTransfer(ctx, handoff)
			}

			// Repetition detection: if the model emits the same text 3+ turns in a
			// row it is stuck in a loop. Inject a steering message to break out.
			if turn-loopStartTurn >= 2 && resp.Content != "" && resp.Content == lastContent {
				repeatCount++
				if repeatCount >= 2 {
					a.steering.Push(Message{
						Role:    RoleSystem,
						Content: "You have been repeating the same response. Stop this loop immediately. Do not call any more tools. Give a final answer based on what you have so far, or clearly state that you cannot complete the request and ask the user for guidance.",
					})
					lastContent = ""
					repeatCount = 0
				}
			} else if resp.Content != "" {
				lastContent = resp.Content
				repeatCount = 0
			}

			// Tool-call repetition detection: if the model makes the same set of
			// tool calls (by name) 3+ turns in a row, it is stuck in a retry loop
			// even though the text content differs each turn.
			if len(resp.ToolCalls) > 0 {
				sig := toolCallSignature(resp.ToolCalls)
				if sig == lastToolSignature {
					toolRepeatCount++
					if toolRepeatCount >= 2 {
						a.steering.Push(Message{
							Role:    RoleSystem,
							Content: "You have been calling the same tools repeatedly without progress. Stop this loop immediately. Do not call any more tools. Report to the user what you attempted and why it failed, and ask for guidance.",
						})
						lastToolSignature = ""
						toolRepeatCount = 0
					}
				} else {
					lastToolSignature = sig
					toolRepeatCount = 0
				}
			}
		}

		// Outer loop: check for follow-up messages
		followUps := a.followUp.Drain()
		if len(followUps) == 0 {
			break
		}

		// Restart the loop with follow-up messages
		a.state.SetStatus(StatusRunning)
		for _, msg := range followUps {
			if err := a.persistMessage(ctx, msg); err != nil {
				ne := NewNodeError("lifecycle persist follow-up failed", err, a.config.Name, "follow_up")
				a.state.SetStatus(StatusError)
				a.emit(&AgentErrorEvent{baseEvent: newBase(EventAgentError), Err: ne})
				return "", ne
			}
		}
	}

	a.emit(&AgentEndEvent{
		baseEvent: newBase(EventAgentEnd),
		AgentName: a.config.Name,
		Output:    finalOutput,
	})
	return finalOutput, nil
}

// toolCallSignature returns a stable string key for a set of tool calls,
// used by the repetition detector to catch retry loops where the model
// calls the same tools each turn but with varying text content.
func toolCallSignature(calls []ToolCall) string {
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.Name
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// persistMessage appends a message after lifecycle BeforeMessagePersist /
// AfterMessagePersist hooks. ReplaceMessages (compaction) bypasses this.
func (a *Agent) persistMessage(ctx context.Context, m Message) error {
	if lc := a.lifecycle(); lc != nil {
		arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: a.state.Turn()}
		cp := m
		if err := lc.BeforeMessagePersist(ctx, arc, &cp); err != nil {
			return err
		}
		m = cp
	}
	a.state.AddMessage(m)
	if lc := a.lifecycle(); lc != nil {
		arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: a.state.Turn()}
		lc.AfterMessagePersist(ctx, arc, m)
	}
	return nil
}

func (a *Agent) checkpointTurnStart(ctx context.Context, turn int64) error {
	c := a.config.Checkpoint
	if c == nil || c.Saver == nil || !c.SaveOnTurnStart {
		return nil
	}
	if err := a.appendCheckpoint(ctx); err != nil {
		return NewNodeError("checkpoint failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn), "checkpoint_turn_start")
	}
	return nil
}

func (a *Agent) checkpointTurnEnd(ctx context.Context, turn int64) error {
	c := a.config.Checkpoint
	if c == nil || c.Saver == nil || c.SkipSaveOnTurnEnd {
		return nil
	}
	if err := a.appendCheckpoint(ctx); err != nil {
		return NewNodeError("checkpoint failed", err, a.config.Name, fmt.Sprintf("turn:%d", turn), "checkpoint_turn_end")
	}
	return nil
}

// --- context compaction ---

func (a *Agent) maybeCompact(ctx context.Context) error {
	if a.contextEngine == nil {
		return nil
	}
	msgs := a.state.Messages()
	toolDefs := a.registry.Definitions()
	if !a.contextEngine.ShouldCompact(msgs, toolDefs, a.config.ContextWindow) {
		return nil
	}
	return a.ForceCompact(ctx)
}

func (a *Agent) ForceCompact(ctx context.Context) error {
	return a.ForceCompactWithTopic(ctx, "")
}

func (a *Agent) ForceCompactWithTopic(ctx context.Context, focusTopic string) error {
	if a.contextEngine == nil {
		return nil
	}
	msgs := a.state.Messages()
	toolDefs := a.registry.Definitions()
	tokensBefore := EstimateMessagesTokens(msgs) + EstimateToolDefinitionsTokens(toolDefs)

	a.emit(&CompactionStartEvent{
		baseEvent:     newBase(EventCompactionStart),
		TokensBefore:  tokensBefore,
		ContextWindow: a.config.ContextWindow,
	})

	start := time.Now()
	newMsgs, messagesCut, err := a.contextEngine.Compress(ctx, msgs, focusTopic)
	if err != nil {
		return err
	}
	if messagesCut > 0 {
		a.state.ReplaceMessages(newMsgs)
	}

	tokensAfter := EstimateMessagesTokens(a.state.Messages()) + EstimateToolDefinitionsTokens(toolDefs)

	a.emit(&CompactionEndEvent{
		baseEvent:    newBase(EventCompactionEnd),
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
		MessagesCut:  messagesCut,
		Duration:     time.Since(start),
	})
	return nil
}

// --- provider call with retry ---

func (a *Agent) callProviderWithRetry(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	resp, err := a.callProvider(ctx, req)
	if err == nil {
		return resp, nil
	}

	cfg := a.config.RetryConfig
	if cfg == nil || !IsRetryableError(err) {
		return nil, err
	}

	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	for attempt := int64(1); attempt <= maxRetries; attempt++ {
		delay := retryDelay(attempt, cfg)
		a.emit(&AutoRetryEvent{
			baseEvent:  newBase(EventAutoRetry),
			Attempt:    attempt,
			MaxRetries: maxRetries,
			Delay:      delay,
			Err:        err,
		})

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		resp, err = a.callProvider(ctx, req)
		if err == nil {
			return resp, nil
		}
		if !IsRetryableError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("after %d retries: %w", maxRetries, err)
}

// --- internal helpers ---

func (a *Agent) callProvider(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	ctx, span := a.tracer().Start(ctx, "agent.llm",
		Attr("model", req.Model),
		Attr("streaming", a.config.Streaming),
		Attr("tool_count", len(req.Tools)),
	)
	defer span.End()

	var resp *ProviderResponse
	var err error
	if a.config.Streaming {
		resp, err = a.runStreaming(ctx, req)
	} else {
		resp, err = a.config.Provider.Complete(ctx, req)
	}
	if err != nil {
		span.RecordError(err)
	}
	return resp, err
}

func (a *Agent) runStreaming(ctx context.Context, req *ProviderRequest) (*ProviderResponse, error) {
	ch, err := a.config.Provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	var content strings.Builder
	var blocks []ContentBlock
	toolCallMap := make(map[int64]*ToolCall)
	var usage TokenUsage
	var finishReason string

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case delta, ok := <-ch:
			if !ok {
				goto buildResponse
			}
			if delta.Content != "" {
				content.WriteString(delta.Content)
				kind := BlockKindText
				for _, bl := range delta.Blocks {
					if bl.Kind == BlockKindThinking {
						kind = BlockKindThinking
						break
					}
				}
				a.emit(&MessageDeltaEvent{
					baseEvent: newBase(EventMessageDelta),
					Delta:     delta.Content,
					Kind:      kind,
				})
			}
			if len(delta.Blocks) > 0 {
				blocks = MergeContentBlocks(blocks, delta.Blocks...)
			} else if delta.Content != "" {
				blocks = MergeContentBlocks(blocks, ContentBlock{
					Kind: BlockKindText,
					Text: delta.Content,
				})
			}

			for _, tcd := range delta.ToolCalls {
				tc, ok := toolCallMap[tcd.Index]
				if !ok {
					tc = &ToolCall{}
					toolCallMap[tcd.Index] = tc
				}
				if tcd.ID != "" {
					tc.ID = tcd.ID
				}
				if tcd.Name != "" {
					tc.Name = tcd.Name
				}
				tc.Arguments += tcd.Arguments
			}

			if delta.Usage != nil {
				usage = *delta.Usage
			}
			if delta.FinishReason != "" {
				finishReason = delta.FinishReason
			}
		}
	}

buildResponse:
	var indices []int64
	for idx := range toolCallMap {
		indices = append(indices, idx)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

	toolCalls := make([]ToolCall, 0, len(indices))
	for _, idx := range indices {
		toolCalls = append(toolCalls, *toolCallMap[idx])
	}

	return &ProviderResponse{
		Content:      content.String(),
		Blocks:       blocks,
		ToolCalls:    toolCalls,
		Usage:        usage,
		FinishReason: finishReason,
	}, nil
}

// hasInvalidToolCallArgs checks if any tool call in the batch has arguments
// that are not valid JSON. Empty arguments are considered valid (some tools
// take no arguments).
func hasInvalidToolCallArgs(calls []ToolCall) bool {
	for _, tc := range calls {
		if tc.Arguments != "" && !json.Valid([]byte(tc.Arguments)) {
			return true
		}
	}
	return false
}

func (a *Agent) executeToolCalls(ctx context.Context, calls []ToolCall) error {
	// Lifecycle: BeforeToolExecution
	if lc := a.lifecycle(); lc != nil {
		arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: a.state.Turn()}
		tec := &ToolExecutionContext{ToolCalls: calls}
		if err := lc.BeforeToolExecution(ctx, arc, tec); err != nil {
			for _, tc := range calls {
				if perr := a.persistMessage(ctx, Message{
					Role:       RoleTool,
					Content:    fmt.Sprintf("Error: tool execution blocked by lifecycle hook: %v", err),
					ToolCallID: tc.ID,
					Name:       tc.Name,
				}); perr != nil {
					return perr
				}
			}
			return nil
		}
	}

	cb := &ExecuteCallbacks{
		OnStart: func(tc ToolCall) {
			a.emit(&ToolCallStartEvent{
				baseEvent: newBase(EventToolCallStart),
				ToolCall:  tc,
			})
		},
		OnEnd: func(r ToolResult) {
			a.emit(&ToolCallEndEvent{
				baseEvent:  newBase(EventToolCallEnd),
				ToolCallID: r.ToolCallID,
				ToolName:   r.ToolName,
				Result:     r.Result,
				Err:        r.Err,
				Duration:   r.Duration,
			})
		},
	}

	var results []ToolResult
	if a.config.BeforeToolCall != nil || a.config.AfterToolCall != nil {
		results = a.executeWithLoopHooks(ctx, calls, cb)
	} else {
		results = a.executor.ExecuteAll(ctx, calls, a.state, cb)
	}

	// Lifecycle: AfterToolExecution
	if lc := a.lifecycle(); lc != nil {
		arc := &AgentRunContext{Agent: a, Messages: a.state.Messages(), Turn: a.state.Turn()}
		tec := &ToolExecutionContext{ToolCalls: calls, Results: results}
		lc.AfterToolExecution(ctx, arc, tec)
	}

	// Turn-level result post-processing (e.g. output budget enforcement)
	if a.config.PostProcessResults != nil {
		results = a.config.PostProcessResults(ctx, calls, results)
	}

	for i, tc := range calls {
		r := results[i]

		// Skip unexecuted tools (serial mode stopped early after interrupt).
		if r.ToolCallID == "" && r.ToolName == "" && r.Result == "" && r.Err == nil {
			continue
		}

		content := r.Result
		if r.Err != nil {
			if errors.Is(r.Err, context.Canceled) {
				content = "Tool execution was interrupted"
			} else if IsInterrupt(r.Err) {
				content = r.Result
				if content == "" {
					content = r.Err.Error()
				}
			} else {
				content = fmt.Sprintf("Error: %s", r.Err.Error())
			}
		}
		if err := a.persistMessage(ctx, Message{
			Role:       RoleTool,
			Content:    content,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		}); err != nil {
			return err
		}

		if IsInterrupt(r.Err) {
			a.interrupted = &InterruptReason{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Reason:     InterruptMessage(r.Err),
				Data:       InterruptData(r.Err),
			}
			a.state.SetInterruptReason(a.interrupted)
			if err := a.appendCheckpoint(ctx); err != nil {
				return err
			}
			return ErrInterrupt
		}
	}
	return nil
}

func (a *Agent) executeWithLoopHooks(ctx context.Context, calls []ToolCall, cb *ExecuteCallbacks) []ToolResult {
	results := make([]ToolResult, len(calls))

	for i, tc := range calls {
		if a.config.BeforeToolCall != nil {
			if override := a.config.BeforeToolCall(ctx, tc); override != nil && override.Block {
				result := override.Result
				if result == "" {
					result = fmt.Sprintf("tool call blocked: %s", tc.Name)
				}
				var toolErr error
				if override.IsError {
					toolErr = fmt.Errorf("%s", result)
				}
				results[i] = ToolResult{
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Result:     result,
					Err:        toolErr,
				}
				if cb != nil && cb.OnStart != nil {
					cb.OnStart(tc)
				}
				if cb != nil && cb.OnEnd != nil {
					cb.OnEnd(results[i])
				}
				continue
			}
		}

		if cb != nil && cb.OnStart != nil {
			cb.OnStart(tc)
		}
		results[i] = a.executor.Execute(ctx, tc, a.state)
		if cb != nil && cb.OnEnd != nil {
			cb.OnEnd(results[i])
		}

		if a.config.AfterToolCall != nil {
			if modified := a.config.AfterToolCall(ctx, tc, &results[i]); modified != nil {
				results[i] = *modified
			}
		}

		// Stop executing subsequent tools on interrupt so unexecuted
		// results are skipped by executeToolCalls.
		if IsInterrupt(results[i].Err) {
			break
		}
	}

	return results
}

func (a *Agent) emit(e Event) { a.eventBus.Emit(e) }

func (a *Agent) tracer() Tracer {
	if a.config.Tracer != nil {
		return a.config.Tracer
	}
	return noopTracer{}
}
