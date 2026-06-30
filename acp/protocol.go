package acp

import (
	"encoding/json"
	"fmt"
)

const ProtocolVersion = 1

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("acp error %d: %s", e.Code, e.Message)
}

type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ClientCapabilities struct {
	FS       *FileSystemCapability `json:"fs,omitempty"`
	Terminal bool                  `json:"terminal,omitempty"`
}

type FileSystemCapability struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

type AgentCapabilities struct {
	LoadSession         bool                 `json:"loadSession,omitempty"`
	PromptCapabilities  *PromptCapabilities  `json:"promptCapabilities,omitempty"`
	SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
}

type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type SessionCapabilities struct {
	Fork   *SessionForkCapabilities   `json:"fork,omitempty"`
	List   *SessionListCapabilities   `json:"list,omitempty"`
	Resume *SessionResumeCapabilities `json:"resume,omitempty"`
}

type SessionForkCapabilities struct{}
type SessionListCapabilities struct{}
type SessionResumeCapabilities struct{}

type AuthMethodAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type TerminalAuthMethod struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Args        []string `json:"args,omitempty"`
}

type InitializeParams struct {
	ProtocolVersion    int                 `json:"protocolVersion,omitempty"`
	ClientCapabilities *ClientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *Implementation     `json:"clientInfo,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AuthMethods       []any             `json:"authMethods,omitempty"`
}

type AuthenticateParams struct {
	MethodID string `json:"methodId"`
}

type AuthenticateResult struct{}

type ModelInfo struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []SessionMode `json:"availableModes"`
}

type SessionModelState struct {
	AvailableModels []ModelInfo `json:"availableModels"`
	CurrentModelID  string      `json:"currentModelId"`
}

type NewSessionParams struct {
	CWD        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers,omitempty"`
}

type NewSessionResult struct {
	SessionID string             `json:"sessionId"`
	Models    *SessionModelState `json:"models,omitempty"`
	Modes     *SessionModeState  `json:"modes,omitempty"`
}

type LoadSessionParams struct {
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId"`
}

type LoadSessionResult struct {
	Models *SessionModelState `json:"models,omitempty"`
	Modes  *SessionModeState  `json:"modes,omitempty"`
}

type ResumeSessionParams struct {
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId"`
}

type ResumeSessionResult struct {
	Models *SessionModelState `json:"models,omitempty"`
	Modes  *SessionModeState  `json:"modes,omitempty"`
}

type ForkSessionParams struct {
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId"`
}

type ForkSessionResult struct {
	SessionID string             `json:"sessionId"`
	Models    *SessionModelState `json:"models,omitempty"`
	Modes     *SessionModeState  `json:"modes,omitempty"`
}

type ListSessionsParams struct {
	Cursor string `json:"cursor,omitempty"`
	CWD    string `json:"cwd,omitempty"`
}

type SessionInfo struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type ListSessionsResult struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

type TextContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ImageContentBlock struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

type PromptResponse struct {
	StopReason string `json:"stopReason"`
	Usage      *Usage `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	TotalTokens      int `json:"totalTokens"`
	ThoughtTokens    int `json:"thoughtTokens,omitempty"`
	CachedReadTokens int `json:"cachedReadTokens,omitempty"`
}

type CancelParams struct {
	SessionID string `json:"sessionId"`
}

type SetSessionModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type SetSessionModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// SessionNotification is the params object for a "session/update" notification.
type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate is the discriminated update payload, keyed by "sessionUpdate".
// Variants: user_message_chunk, agent_message_chunk, agent_thought_chunk,
// tool_call, tool_call_update, plan, available_commands_update,
// current_mode_update.
type SessionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`

	// *_message_chunk / agent_thought_chunk
	Content any `json:"content,omitempty"`

	// tool_call / tool_call_update
	ToolCallID string `json:"toolCallId,omitempty"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
	RawInput   any    `json:"rawInput,omitempty"`
	RawOutput  any    `json:"rawOutput,omitempty"`

	// plan
	Entries []PlanEntry `json:"entries,omitempty"`

	// available_commands_update
	AvailableCommands []AvailableCommand `json:"availableCommands,omitempty"`

	// current_mode_update
	CurrentModeID string `json:"currentModeId,omitempty"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

type AvailableCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputHint   string `json:"inputHint,omitempty"`
}

// --- Permission (agent -> client request: "session/request_permission") ---

type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionToolCall struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
	RawInput   any    `json:"rawInput,omitempty"`
}

// PermissionOption.Kind is one of: allow_once, allow_always, reject_once,
// reject_always.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type RequestPermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOutcome.Outcome is "selected" or "cancelled".
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}