package agentcore

import (
	"sync"
)

// ModelProfile provides model-specific default configuration overrides.
// Register a profile for a model identifier (typically Config.Model) and
// New() will automatically apply it. User-set fields always take
// precedence over profile values; SystemPromptSuffix and ExcludedTools are
// additive/removal and always apply.
//
// This is the lightweight equivalent of a full profile system: enough to
// let the community contribute model-specific tuning without forcing every
// caller to hand-tune Config for each model.
type ModelProfile struct {
	// Name is the profile identifier, matched against Config.Model.
	// Typically the model name (e.g. "claude-sonnet-4-6") or a
	// "provider:model" key.
	Name string

	// SystemPromptSuffix is appended to Config.SystemPrompt when this
	// profile is active. Useful for model-specific guidance such as
	// "use parallel tool calls" or "investigate before answering".
	SystemPromptSuffix string

	// ExcludedTools lists tool names removed from Config.Tools when this
	// profile is active. Use when a model cannot reliably use a tool.
	// Handoff tools (transfer_to_*) are never excluded.
	ExcludedTools []string

	// Temperature overrides Config.Temperature only when the caller left
	// it at the zero value (0). nil means no override.
	Temperature *float64

	// MaxTurns overrides Config.MaxTurns only when the caller left it at
	// zero (before the default-20 fallback applies). nil means no override.
	MaxTurns *int64
}

var (
	profileMu sync.RWMutex
	profiles  = map[string]ModelProfile{}
)

// RegisterProfile registers a model profile globally. A profile with the
// same Name replaces any previously registered one. Registration affects
// all subsequent New() calls — call from init() or program startup.
// It is safe for concurrent use.
func RegisterProfile(p ModelProfile) {
	if p.Name == "" {
		return
	}
	profileMu.Lock()
	defer profileMu.Unlock()
	profiles[p.Name] = p
}

// LookupProfile returns the registered profile for the given model
// identifier. Returns ok=false if no profile matches.
func LookupProfile(model string) (ModelProfile, bool) {
	profileMu.RLock()
	defer profileMu.RUnlock()
	p, ok := profiles[model]
	return p, ok
}

// ResetProfilesForTest clears all registered profiles. Intended for test
// isolation; do not call in production code.
func ResetProfilesForTest() {
	profileMu.Lock()
	defer profileMu.Unlock()
	profiles = map[string]ModelProfile{}
}

// ApplyModelProfile applies any registered profile for cfg.Model to cfg
// and returns the resulting Config. Called automatically by New(); exposed
// publicly so callers can preview the effective configuration.
//
// Precedence rules:
//   - SystemPromptSuffix: always appended (additive, never overwrites).
//   - ExcludedTools: always removed from cfg.Tools.
//   - Temperature: applied only when cfg.Temperature == 0.
//   - MaxTurns: applied only when cfg.MaxTurns <= 0.
//
// Limitation: profiles are applied once during New(). Changing Config.Model
// later (e.g. via Agent.ApplyCallConfig for a thread-level model override)
// does NOT re-apply the new model's profile. Callers that switch models at
// runtime and need profile adjustments should construct a fresh Agent, or
// call ApplyModelProfile manually and apply the resulting field changes.
func ApplyModelProfile(cfg Config) Config {
	if cfg.Model == "" {
		return cfg
	}
	p, ok := LookupProfile(cfg.Model)
	if !ok {
		return cfg
	}

	if p.SystemPromptSuffix != "" {
		if cfg.SystemPrompt != "" {
			cfg.SystemPrompt += "\n\n" + p.SystemPromptSuffix
		} else {
			cfg.SystemPrompt = p.SystemPromptSuffix
		}
	}

	if len(p.ExcludedTools) > 0 && len(cfg.Tools) > 0 {
		excluded := make(map[string]struct{}, len(p.ExcludedTools))
		for _, n := range p.ExcludedTools {
			excluded[n] = struct{}{}
		}
		filtered := make([]*Tool, 0, len(cfg.Tools))
		for _, t := range cfg.Tools {
			if _, drop := excluded[t.Name]; !drop {
				filtered = append(filtered, t)
			}
		}
		cfg.Tools = filtered
	}

	if p.Temperature != nil && cfg.Temperature == 0 {
		cfg.Temperature = *p.Temperature
	}

	if p.MaxTurns != nil && cfg.MaxTurns <= 0 {
		cfg.MaxTurns = *p.MaxTurns
	}

	return cfg
}
