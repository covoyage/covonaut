package agentcore

import (
	"context"
	"fmt"
	"time"
)

// ContextEngine controls how conversation context is managed when
// approaching the model's token limit. The built-in CompressorEngine
// is the default implementation. Third-party engines can replace it
// via the plugin registry.
//
// Lifecycle:
//  1. Engine is registered (plugin or default)
//  2. OnSessionStart called when a conversation begins
//  3. UpdateFromResponse called after each API response with usage data
//  4. ShouldCompact checked after each turn
//  5. Compress called when ShouldCompact returns true
//  6. OnSessionEnd called at real session boundaries (CLI exit, /reset,
//     gateway session expiry) — NOT per-turn
type ContextEngine interface {
	// Name returns the engine identifier (e.g. "compressor", "lcm").
	Name() string

	// OnSessionStart initializes per-session state.
	OnSessionStart(ctx context.Context, model string, contextLength int64)

	// OnSessionReset clears all per-session state for /new or /reset.
	OnSessionReset()

	// OnSessionEnd is called at session termination.
	OnSessionEnd()

	// UpdateFromResponse updates tracked token usage from an API response.
	UpdateFromResponse(usage TokenUsage)

	// ShouldCompact returns true if compaction should fire this turn.
	ShouldCompact(msgs []Message, toolDefs []ToolDefinition, contextWindow int64) bool

	// Compress compacts the message list and returns the new message list.
	// focusTopic is optional for guided compression (like Claude Code's /compact <topic>).
	Compress(ctx context.Context, msgs []Message, focusTopic string) ([]Message, int64, error)

	// GetToolSchemas returns optional tool definitions the engine exposes
	// (e.g. lcm_grep for LCM engines). Most engines return nil.
	GetToolSchemas() []ToolDefinition

	// ContextLength returns the model's context window size.
	ContextLength() int64

	// ThresholdTokens returns the token count at which compression triggers.
	ThresholdTokens() int64

	// CompressionCount returns the number of successful compressions.
	CompressionCount() int64

	// LastSavingsPct returns the savings percentage of the last compression.
	LastSavingsPct() float64

	// CheckFeasibility validates that the compression model can handle
	// summarization. Returns a warning message if issues found, empty string otherwise.
	CheckFeasibility(mainModelContextLength int64) string
}

// ContextEngineFactory creates a ContextEngine from configuration.
type ContextEngineFactory func(cfg ContextEngineConfig) ContextEngine

// ContextEngineConfig holds configuration for context engine initialization.
type ContextEngineConfig struct {
	Model                string
	BaseURL              string
	APIKey               string
	Provider             Provider
	ContextWindow        int64
	ReserveTokens        int64
	KeepRecentTokens     int64
	ProtectFirstN        int
	CompressionThreshold float64
	AutoCompactLimit     int64
	StructuredCompaction bool
	CompressionModel     string
	CompressionProvider  Provider
	CompressionBaseURL   string
	CompressionAPIKey    string
}

// EngineRegistry manages registered context engine factories.
type EngineRegistry struct {
	factories map[string]ContextEngineFactory
	defaults  string
}

// NewEngineRegistry creates a new registry with the built-in compressor as default.
func NewEngineRegistry() *EngineRegistry {
	r := &EngineRegistry{
		factories: make(map[string]ContextEngineFactory),
		defaults:  "compressor",
	}
	r.Register("compressor", NewCompressorEngine)
	r.Register("truncate", NewTruncateEngine)
	return r
}

// Register adds a context engine factory.
func (r *EngineRegistry) Register(name string, factory ContextEngineFactory) {
	r.factories[name] = factory
}

// Create instantiates a context engine by name.
func (r *EngineRegistry) Create(name string, cfg ContextEngineConfig) (ContextEngine, error) {
	factory, ok := r.factories[name]
	if !ok {
		return nil, &EngineNotFoundError{Name: name}
	}
	return factory(cfg), nil
}

// Default returns the default engine name.
func (r *EngineRegistry) Default() string {
	return r.defaults
}

// List returns all registered engine names.
func (r *EngineRegistry) List() []string {
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// EngineNotFoundError is returned when a requested engine is not registered.
type EngineNotFoundError struct {
	Name string
}

func (e *EngineNotFoundError) Error() string {
	return "context engine '" + e.Name + "' not found"
}

// CompressorEngine is the built-in context engine — compresses conversation
// context via lossy LLM summarization.
type CompressorEngine struct {
	model               string
	baseURL             string
	apiKey              string
	provider            Provider
	contextLength       int64
	thresholdTokens     int64
	thresholdPercent    float64
	protectFirstN       int
	keepRecentTokens    int64
	structured          bool
	autoCompactLimit    int64
	compressionModel    string
	compressionProvider Provider
	compressionBaseURL  string
	compressionAPIKey   string

	state          *compactionState
	compressionCnt int64
}

// NewCompressorEngine creates the built-in compressor engine.
func NewCompressorEngine(cfg ContextEngineConfig) ContextEngine {
	return &CompressorEngine{
		model:               cfg.Model,
		baseURL:             cfg.BaseURL,
		apiKey:              cfg.APIKey,
		provider:            cfg.Provider,
		contextLength:       cfg.ContextWindow,
		thresholdPercent:    cfg.CompressionThreshold,
		protectFirstN:       cfg.ProtectFirstN,
		keepRecentTokens:    cfg.KeepRecentTokens,
		structured:          cfg.StructuredCompaction,
		autoCompactLimit:    cfg.AutoCompactLimit,
		compressionModel:    cfg.CompressionModel,
		compressionProvider: cfg.CompressionProvider,
		compressionBaseURL:  cfg.CompressionBaseURL,
		compressionAPIKey:   cfg.CompressionAPIKey,
		state:               newCompactionState(),
	}
}

func (e *CompressorEngine) Name() string {
	return "compressor"
}

func (e *CompressorEngine) OnSessionStart(ctx context.Context, model string, contextLength int64) {
	e.model = model
	e.contextLength = contextLength
	if e.thresholdPercent > 0 {
		e.thresholdTokens = int64(float64(contextLength) * e.thresholdPercent)
	}
}

func (e *CompressorEngine) OnSessionReset() {
	e.state = newCompactionState()
	e.compressionCnt = 0
}

func (e *CompressorEngine) OnSessionEnd() {
}

func (e *CompressorEngine) UpdateFromResponse(usage TokenUsage) {
}

func (e *CompressorEngine) ShouldCompact(msgs []Message, toolDefs []ToolDefinition, contextWindow int64) bool {
	return shouldCompact(msgs, toolDefs, contextWindow, 0, e.thresholdPercent, e.autoCompactLimit, e.state)
}

func (e *CompressorEngine) Compress(ctx context.Context, msgs []Message, focusTopic string) ([]Message, int64, error) {
	displayTokens := EstimateMessagesTokens(msgs)

	tmpState := NewState()
	tmpState.ReplaceMessages(msgs)

	cut, err := runCompaction(
		ctx,
		e.provider,
		e.model,
		tmpState,
		e.keepRecentTokens,
		e.structured,
		e.protectFirstN,
		focusTopic,
		e.state,
		e.compressionModel,
		e.compressionProvider,
		e.compressionBaseURL,
		e.compressionAPIKey,
	)
	if err != nil {
		return msgs, 0, err
	}
	if cut == 0 {
		return msgs, 0, nil
	}

	e.compressionCnt++
	result := tmpState.Messages()
	newTokens := EstimateMessagesTokens(result)
	saved := displayTokens - newTokens

	if displayTokens > 0 {
		e.state.lastSavingsPct = float64(saved) / float64(displayTokens) * 100
		if e.state.lastSavingsPct < 10 {
			e.state.ineffectiveCompactions++
		} else {
			e.state.ineffectiveCompactions = 0
		}
	}

	return result, cut, nil
}

func (e *CompressorEngine) GetToolSchemas() []ToolDefinition {
	return nil
}

func (e *CompressorEngine) ContextLength() int64 {
	return e.contextLength
}

func (e *CompressorEngine) ThresholdTokens() int64 {
	return e.thresholdTokens
}

func (e *CompressorEngine) CompressionCount() int64 {
	return e.compressionCnt
}

func (e *CompressorEngine) LastSavingsPct() float64 {
	return e.state.lastSavingsPct
}

// SummaryStats returns detailed compression statistics for diagnostics.
func (e *CompressorEngine) SummaryStats() map[string]any {
	return map[string]any{
		"previous_summary":         e.state.previousSummary != "",
		"last_savings_pct":         e.state.lastSavingsPct,
		"ineffective_compactions":  e.state.ineffectiveCompactions,
		"last_summary_error":       e.state.lastSummaryError,
		"summary_failure_cooldown": e.state.summaryFailureCooldown.After(time.Now()),
	}
}

// CheckFeasibility validates that the compression model's context window
// is sufficient for summarization. Returns a warning message if issues found.
func (e *CompressorEngine) CheckFeasibility(mainModelContextLength int64) string {
	if e.compressionModel == "" || e.compressionProvider == nil {
		return ""
	}
	if e.contextLength > 0 && e.contextLength < mainModelContextLength/2 {
		return "Warning: compression model context window (" +
			fmt.Sprintf("%d", e.contextLength) +
			") is smaller than half the main model's context. Summaries may be truncated."
	}
	return ""
}
