package agentcore

import (
	"context"
)

// PromptCachingExtension automatically annotates messages with
// CacheControlMarker breakpoints so that providers supporting prompt
// caching (e.g. Anthropic) can cache stable prefixes and reduce token
// costs by up to ~75%.
//
// The extension implements TransformContextProvider: it runs on every
// model call, after the agent assembles the message list and before it
// is sent to the provider. Messages already carrying a CacheControl
// marker are left untouched, so manual markers win over the automatic
// strategy.
//
// Anthropic limits cache_control to 4 breakpoints per request; the
// default strategy uses 2 (one on the system prompt, one near the tail
// of the conversation) which keeps a stable prefix cached while leaving
// room for manual markers if needed.
type PromptCachingExtension struct {
	// MaxBreakpoints caps the total number of markers the extension will
	// add. 0 or negative disables the extension entirely (no markers
	// added). NewPromptCachingExtension defaults to 4 (Anthropic limit).
	MaxBreakpoints int

	// CacheSystemPrompt adds a breakpoint to the first system message.
	// Defaults to true.
	CacheSystemPrompt bool

	// CacheLastN adds a breakpoint to the message at position
	// len(msgs)-CacheLastN, so everything before it stays cached as the
	// conversation grows. Set to 0 to disable. Defaults to 3.
	CacheLastN int
}

// PromptCachingOption configures a PromptCachingExtension.
type PromptCachingOption func(*PromptCachingExtension)

// WithMaxBreakpoints sets the breakpoint cap.
func WithMaxBreakpoints(n int) PromptCachingOption {
	return func(e *PromptCachingExtension) { e.MaxBreakpoints = n }
}

// WithCacheSystemPrompt enables/disables the system-prompt breakpoint.
func WithCacheSystemPrompt(b bool) PromptCachingOption {
	return func(e *PromptCachingExtension) { e.CacheSystemPrompt = b }
}

// WithCacheLastN sets the tail-breakpoint offset. 0 disables it.
func WithCacheLastN(n int) PromptCachingOption {
	return func(e *PromptCachingExtension) { e.CacheLastN = n }
}

// NewPromptCachingExtension creates an extension with sensible defaults:
// up to 4 breakpoints, system prompt cached, tail breakpoint at -3.
func NewPromptCachingExtension(opts ...PromptCachingOption) *PromptCachingExtension {
	e := &PromptCachingExtension{
		MaxBreakpoints:    4,
		CacheSystemPrompt: true,
		CacheLastN:        3,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Name implements Extension.
func (e *PromptCachingExtension) Name() string { return "prompt-caching" }

// Init implements Extension.
func (e *PromptCachingExtension) Init(_ context.Context, _ *Agent) error { return nil }

// Dispose implements Extension.
func (e *PromptCachingExtension) Dispose() error { return nil }

// TransformContext implements TransformContextProvider. It returns a new
// slice; the input messages are not mutated. Messages that already carry
// a CacheControl marker are preserved as-is.
func (e *PromptCachingExtension) TransformContext(_ context.Context, msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)

	if e.MaxBreakpoints <= 0 {
		return out // disabled: zero-value or explicit disable
	}

	used := 0
	max := e.MaxBreakpoints

	if e.CacheSystemPrompt && used < max {
		for i := range out {
			if out[i].Role == RoleSystem && out[i].CacheControl == nil {
				out[i].CacheControl = &CacheControlMarker{Type: "ephemeral"}
				used++
				break
			}
		}
	}

	if e.CacheLastN > 0 && used < max {
		idx := len(out) - e.CacheLastN
		if idx >= 0 && out[idx].CacheControl == nil && out[idx].Role != RoleSystem {
			out[idx].CacheControl = &CacheControlMarker{Type: "ephemeral"}
			used++
		}
	}

	return out
}
