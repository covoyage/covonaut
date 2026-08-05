// Package chatcompat implements the Chat Completions API protocol.
//
// This is not a vendor-specific provider. It implements the protocol that
// dozens of providers have adopted as their LLM API surface. Any service that
// exposes a POST /v1/chat/completions endpoint compatible with the Chat
// Completions spec can be used through this package — just set BaseURL and
// APIKey.
//
// Provider-specific quirks are handled through Config knobs. For example,
// DisableSystemPrompt supports models that reject the system role.
//   - PrepareMessages: full message transformation for custom workflows.
//   - BuildExtraBody: inject provider-specific JSON fields (e.g. provider
//     tags, safety settings).
//
// The package also implements the newer Responses API (/v1/responses)
// when Config.Protocol is set to APIProtocolResponses.
package chatcompat
