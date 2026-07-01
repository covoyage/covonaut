// Package chatcompat implements the Chat Completions API protocol.
//
// This is not a vendor-specific provider. It implements the protocol that
// dozens of providers have adopted as their LLM API surface. Any service that
// exposes a POST /v1/chat/completions endpoint compatible with the Chat
// Completions spec can be used through this package — just set BaseURL and
// APIKey.
//
// Verified compatible vendors (in alphabetical order):
//
//   - DeepSeek          — https://api.deepseek.com/v1
//                        (deepseek-reasoner: set DisableSystemPrompt)
//   - GitHub Copilot    — https://api.githubcopilot.com
//                        (use provider/copilot for token resolution)
//   - Groq              — https://api.groq.com/openai/v1
//   - Mistral           — https://api.mistral.ai/v1
//                        (use provider/mistral for default base URL)
//   - Moonshot (Kimi)   — https://api.moonshot.cn/v1
//   - OpenAI            — https://api.openai.com/v1
//   - Together          — https://api.together.xyz/v1
//   - Tongyi Qianwen    — https://dashscope.aliyuncs.com/compatible-mode/v1
//
// Vendor-specific quirks are handled through Config knobs:
//   - DisableSystemPrompt: needed for DeepSeek reasoner and other models
//     that reject the system role.
//   - PrepareMessages: full message transformation for custom workflows.
//   - BuildExtraBody: inject provider-specific JSON fields (e.g. provider
//     tags, safety settings).
//
// The package also implements the newer Responses API (/v1/responses)
// when Config.Protocol is set to APIProtocolResponses.
package chatcompat
