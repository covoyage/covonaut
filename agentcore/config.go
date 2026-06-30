package agentcore

import (
	"context"

	"github.com/covoyage/covonaut/skill"
)

// ModelConfig groups LLM model selection and generation parameters.
type ModelConfig struct {
	Name           string         // optional: identifies this agent in events and handoff logs
	Model          string         // model identifier (e.g. "gpt-4o-mini")
	Provider       Provider       // LLM provider implementation
	Temperature    float64        // sampling temperature; 0 = deterministic
	MaxTokens      int64          // max tokens in response; 0 = provider default
	ResponseFormat *ResponseFormat // optional: force JSON mode etc.
	Thinking       *ThinkingConfig // optional: extended thinking / reasoning
	Streaming      bool           // enable streaming responses

	// FastMode requests priority/low-latency processing where supported
	// (e.g. OpenAI Priority Processing, Anthropic Fast Mode).
	FastMode bool
}

// SkillConfig groups skill loading, selection, and API control.
type SkillConfig struct {
	AvailableSkills         []skill.Skill
	SelectedSkills          []string
	SkillPaths              []string
	SkillDiagnostics        []skill.Diagnostic
	SkillAPIAuthToken       string
	DisableSkillRegistryAPI bool
	DisableSkillReloadAPI   bool
}

// ExecutionConfig groups execution mode, concurrency, middleware, and hooks.
type ExecutionConfig struct {
	ExecutionMode      ExecutionMode
	Concurrency        int64
	MaxTurns           int64
	Middleware         []Middleware
	GlobalBefore       []BeforeHook
	GlobalAfter        []AfterHook
	ValidateArguments  bool
	UnknownToolHandler UnknownToolHandler
	SteeringMode       SteeringMode // default: SteeringAll
	FollowUpMode       SteeringMode // default: SteeringAll
}

// CompactionConfig groups context window management and compaction behavior.
type CompactionConfig struct {
	ContextWindow         int64    // model context window size in tokens (e.g. 128000); 0 = no compaction
	ReserveTokens         int64    // tokens reserved for response generation; default = ContextWindow/4
	KeepRecentTokens      int64    // min recent tokens preserved during compaction; default = 2000
	StructuredCompaction  bool     // emit JSON summaries instead of free-form paragraphs
	ProtectFirstN         int      // number of non-system head messages to preserve verbatim; default = 3
	CompressionThreshold  float64  // compress when usage exceeds this fraction of contextWindow; default = 0.75
	AutoCompactTokenLimit int64    // absolute token threshold (overrides CompressionThreshold when > 0); default = 0
	AntiThrashEnabled     bool     // skip compaction if recent savings < 10%; default = true
	CompressionModel      string   // optional: separate model for summarization (cheaper/faster)
	CompressionProvider   Provider // optional: provider for compression model
	CompressionBaseURL    string   // optional: base URL for compression model
	CompressionAPIKey     string   // optional: API key for compression model
	Engine                string   // context engine name; default = "compressor"
	CustomEngine          ContextEngine // pre-built custom engine (overrides Engine name)
}

// ConfigOption is a functional option for constructing a Config.
type ConfigOption func(*Config)

// NewConfig creates a Config with the given options applied.
// Zero-value defaults are used for any unset fields; MaxTurns defaults to 20
// when the Config is passed to New().
func NewConfig(opts ...ConfigOption) Config {
	cfg := Config{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// StubConfig returns a minimal Config suitable for tests.
// It sets Model="stub" and Provider=p, which are the most common test setup.
func StubConfig(p Provider, opts ...ConfigOption) Config {
	cfg := Config{
		ModelConfig: ModelConfig{
			Model:    "stub",
			Provider: p,
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// --- Model options ---

// WithModel sets the model identifier.
func WithModel(model string) ConfigOption {
	return func(c *Config) { c.Model = model }
}

// WithProvider sets the LLM provider.
func WithProvider(p Provider) ConfigOption {
	return func(c *Config) { c.Provider = p }
}

// WithName sets the agent name.
func WithName(name string) ConfigOption {
	return func(c *Config) { c.Name = name }
}

// WithTemperature sets the sampling temperature.
func WithTemperature(temp float64) ConfigOption {
	return func(c *Config) { c.Temperature = temp }
}

// WithMaxTokens sets the maximum response tokens.
func WithMaxTokens(n int64) ConfigOption {
	return func(c *Config) { c.MaxTokens = n }
}

// WithStreaming enables or disables streaming.
func WithStreaming(enabled bool) ConfigOption {
	return func(c *Config) { c.Streaming = enabled }
}

// WithResponseFormat sets the response format.
func WithResponseFormat(rf *ResponseFormat) ConfigOption {
	return func(c *Config) { c.ResponseFormat = rf }
}

// WithThinking sets the thinking/reasoning config.
func WithThinking(tc *ThinkingConfig) ConfigOption {
	return func(c *Config) { c.Thinking = tc }
}

// WithFastMode enables/disables priority/low-latency API processing.
func WithFastMode(enabled bool) ConfigOption {
	return func(c *Config) { c.FastMode = enabled }
}

// WithModelConfig applies a full ModelConfig.
func WithModelConfig(mc ModelConfig) ConfigOption {
	return func(c *Config) {
		c.ModelConfig = mc
	}
}

// --- Skill options ---

// WithAvailableSkills sets the available skills.
func WithAvailableSkills(skills []skill.Skill) ConfigOption {
	return func(c *Config) { c.AvailableSkills = skills }
}

// WithSelectedSkills sets the selected skill names.
func WithSelectedSkills(names []string) ConfigOption {
	return func(c *Config) { c.SelectedSkills = names }
}

// WithSkillPaths sets the skill directory paths for hot-reload.
func WithSkillPaths(paths []string) ConfigOption {
	return func(c *Config) { c.SkillPaths = paths }
}

// WithSkillDiagnostics sets pre-loaded skill diagnostics.
func WithSkillDiagnostics(diags []skill.Diagnostic) ConfigOption {
	return func(c *Config) { c.SkillDiagnostics = diags }
}

// WithSkillAPIAuthToken sets the API auth token for skill endpoints.
func WithSkillAPIAuthToken(token string) ConfigOption {
	return func(c *Config) { c.SkillAPIAuthToken = token }
}

// WithDisableSkillRegistryAPI disables the skill registry HTTP API.
func WithDisableSkillRegistryAPI(disabled bool) ConfigOption {
	return func(c *Config) { c.DisableSkillRegistryAPI = disabled }
}

// WithDisableSkillReloadAPI disables the skill reload HTTP API.
func WithDisableSkillReloadAPI(disabled bool) ConfigOption {
	return func(c *Config) { c.DisableSkillReloadAPI = disabled }
}

// WithSkillConfig applies a full SkillConfig.
func WithSkillConfig(sc SkillConfig) ConfigOption {
	return func(c *Config) {
		c.SkillConfig = sc
	}
}

// --- Execution options ---

// WithExecutionMode sets the execution mode (serial or parallel).
func WithExecutionMode(m ExecutionMode) ConfigOption {
	return func(c *Config) { c.ExecutionMode = m }
}

// WithConcurrency sets the max concurrent tool calls.
func WithConcurrency(n int64) ConfigOption {
	return func(c *Config) { c.Concurrency = n }
}

// WithMaxTurns sets the maximum agent loop turns.
func WithMaxTurns(n int64) ConfigOption {
	return func(c *Config) { c.MaxTurns = n }
}

// WithMiddleware appends middleware to the execution chain.
func WithMiddleware(mw ...Middleware) ConfigOption {
	return func(c *Config) { c.Middleware = append(c.Middleware, mw...) }
}

// WithGlobalBefore appends before-hooks.
func WithGlobalBefore(hooks ...BeforeHook) ConfigOption {
	return func(c *Config) { c.GlobalBefore = append(c.GlobalBefore, hooks...) }
}

// WithGlobalAfter appends after-hooks.
func WithGlobalAfter(hooks ...AfterHook) ConfigOption {
	return func(c *Config) { c.GlobalAfter = append(c.GlobalAfter, hooks...) }
}

// WithExecutionConfig applies a full ExecutionConfig.
func WithExecutionConfig(ec ExecutionConfig) ConfigOption {
	return func(c *Config) {
		c.ExecutionConfig = ec
	}
}

// --- Compaction options ---

// WithContextWindow sets the context window size for auto-compaction.
func WithContextWindow(tokens int64) ConfigOption {
	return func(c *Config) { c.ContextWindow = tokens }
}

// WithReserveTokens sets the tokens reserved for response generation.
func WithReserveTokens(tokens int64) ConfigOption {
	return func(c *Config) { c.ReserveTokens = tokens }
}

// WithKeepRecentTokens sets the minimum recent tokens to preserve.
func WithKeepRecentTokens(tokens int64) ConfigOption {
	return func(c *Config) { c.KeepRecentTokens = tokens }
}

// WithStructuredCompaction enables structured JSON compaction summaries.
func WithStructuredCompaction(enabled bool) ConfigOption {
	return func(c *Config) { c.StructuredCompaction = enabled }
}

// WithCompactionConfig applies a full CompactionConfig.
func WithCompactionConfig(cc CompactionConfig) ConfigOption {
	return func(c *Config) {
		c.CompactionConfig = cc
	}
}

// WithProtectFirstN sets the number of head messages to preserve.
func WithProtectFirstN(n int) ConfigOption {
	return func(c *Config) { c.ProtectFirstN = n }
}

// WithCompressionThreshold sets the compression trigger threshold.
func WithCompressionThreshold(ratio float64) ConfigOption {
	return func(c *Config) { c.CompressionThreshold = ratio }
}

// WithAntiThrash enables/disables anti-thrashing protection.
func WithAntiThrash(enabled bool) ConfigOption {
	return func(c *Config) { c.AntiThrashEnabled = enabled }
}

// WithCompressionModel sets a separate model for context compression.
func WithCompressionModel(model string, provider Provider, baseURL string, apiKey string) ConfigOption {
	return func(c *Config) {
		c.CompressionModel = model
		c.CompressionProvider = provider
		c.CompressionBaseURL = baseURL
		c.CompressionAPIKey = apiKey
	}
}

// WithContextEngine sets the context engine name.
func WithContextEngine(name string) ConfigOption {
	return func(c *Config) { c.Engine = name }
}

// WithCustomContextEngine sets a pre-built custom context engine.
func WithCustomContextEngine(engine ContextEngine) ConfigOption {
	return func(c *Config) { c.CustomEngine = engine }
}

// --- Top-level options ---

// WithSystemPrompt sets the system prompt.
func WithSystemPrompt(prompt string) ConfigOption {
	return func(c *Config) { c.SystemPrompt = prompt }
}

// WithTools sets the agent's tools.
func WithTools(tools ...*Tool) ConfigOption {
	return func(c *Config) { c.Tools = tools }
}

// WithStore sets the persistence store.
func WithStore(s Store) ConfigOption {
	return func(c *Config) { c.Store = s }
}

// WithCheckpoint sets the checkpoint settings.
func WithCheckpoint(cs *CheckpointSettings) ConfigOption {
	return func(c *Config) { c.Checkpoint = cs }
}

// WithHandoffs sets the handoff targets.
func WithHandoffs(handoffs ...HandoffConfig) ConfigOption {
	return func(c *Config) { c.Handoffs = handoffs }
}

// WithTracer sets the distributed tracer.
func WithTracer(t Tracer) ConfigOption {
	return func(c *Config) { c.Tracer = t }
}

// WithRetryConfig sets the retry configuration.
func WithRetryConfig(rc *RetryConfig) ConfigOption {
	return func(c *Config) { c.RetryConfig = rc }
}

// WithExtensions sets the extensions.
func WithExtensions(exts ...Extension) ConfigOption {
	return func(c *Config) { c.Extensions = exts }
}

// WithLifecycle sets the lifecycle hook.
func WithLifecycle(hook LifecycleHook) ConfigOption {
	return func(c *Config) { c.Lifecycle = hook }
}

// WithTransformContext sets the context transform function.
func WithTransformContext(fn func(ctx context.Context, msgs []Message) []Message) ConfigOption {
	return func(c *Config) { c.TransformContext = fn }
}

// WithConvertToLLM sets the message converter.
func WithConvertToLLM(fn ConvertToLLMFunc) ConfigOption {
	return func(c *Config) { c.ConvertToLLM = fn }
}

// WithBeforeToolCall sets the before-tool-call hook.
func WithBeforeToolCall(fn func(ctx context.Context, tc ToolCall) *ToolCallOverride) ConfigOption {
	return func(c *Config) { c.BeforeToolCall = fn }
}

// WithAfterToolCall sets the after-tool-call hook.
func WithAfterToolCall(fn func(ctx context.Context, tc ToolCall, result *ToolResult) *ToolResult) ConfigOption {
	return func(c *Config) { c.AfterToolCall = fn }
}

// WithValidateArguments enables JSON Schema validation of tool arguments.
func WithValidateArguments(enabled bool) ConfigOption {
	return func(c *Config) { c.ValidateArguments = enabled }
}

// WithSteeringMode sets the steering message mode.
func WithSteeringMode(m SteeringMode) ConfigOption {
	return func(c *Config) { c.SteeringMode = m }
}

// WithFollowUpMode sets the follow-up message mode.
func WithFollowUpMode(m SteeringMode) ConfigOption {
	return func(c *Config) { c.FollowUpMode = m }
}
