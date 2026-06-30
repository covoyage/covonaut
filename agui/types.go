package agui

type EventType string

const (
	EventRunStarted        EventType = "RUN_STARTED"
	EventRunFinished       EventType = "RUN_FINISHED"
	EventRunError          EventType = "RUN_ERROR"
	EventStepStarted       EventType = "STEP_STARTED"
	EventStepFinished      EventType = "STEP_FINISHED"
	EventTextMessageStart  EventType = "TEXT_MESSAGE_START"
	EventTextMessageContent EventType = "TEXT_MESSAGE_CONTENT"
	EventTextMessageEnd    EventType = "TEXT_MESSAGE_END"
	EventThinkingStart     EventType = "THINKING_START"
	EventThinkingTextMessageStart EventType = "THINKING_TEXT_MESSAGE_START"
	EventThinkingTextMessageContent EventType = "THINKING_TEXT_MESSAGE_CONTENT"
	EventThinkingTextMessageEnd EventType = "THINKING_TEXT_MESSAGE_END"
	EventThinkingEnd       EventType = "THINKING_END"
	EventToolCallStart     EventType = "TOOL_CALL_START"
	EventToolCallArgs      EventType = "TOOL_CALL_ARGS"
	EventToolCallEnd       EventType = "TOOL_CALL_END"
	EventToolCallResult    EventType = "TOOL_CALL_RESULT"
	EventStateSnapshot     EventType = "STATE_SNAPSHOT"
	EventStateDelta        EventType = "STATE_DELTA"
	EventMessagesSnapshot  EventType = "MESSAGES_SNAPSHOT"
	EventCustom            EventType = "CUSTOM"
	EventRaw               EventType = "RAW"
)

type BaseEvent struct {
	Type      EventType   `json:"type"`
	Timestamp float64     `json:"timestamp,omitempty"`
	RawEvent  any         `json:"rawEvent,omitempty"`
}

func (b BaseEvent) GetType() EventType { return b.Type }

type RunStartedEvent struct {
	BaseEvent
	ThreadID     string `json:"threadId"`
	RunID        string `json:"runId"`
	ParentRunID  string `json:"parentRunId,omitempty"`
}

type RunFinishedEvent struct {
	BaseEvent
	ThreadID string           `json:"threadId"`
	RunID    string           `json:"runId"`
	Result   any              `json:"result,omitempty"`
	Outcome  *RunFinishedOutcome `json:"outcome,omitempty"`
}

type RunFinishedOutcome struct {
	Type       EventType    `json:"type"`
	Interrupts []Interrupt  `json:"interrupts,omitempty"`
}

type Interrupt struct {
	ID             string         `json:"id"`
	Reason         string         `json:"reason"`
	Message        string         `json:"message,omitempty"`
	ToolCallID     string         `json:"toolCallId,omitempty"`
	ResponseSchema any            `json:"responseSchema,omitempty"`
	ExpiresAt      string         `json:"expiresAt,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type RunErrorEvent struct {
	BaseEvent
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
}

type StepStartedEvent struct {
	BaseEvent
	StepName string `json:"stepName"`
}

type StepFinishedEvent struct {
	BaseEvent
	StepName string `json:"stepName"`
}

type TextMessageStartEvent struct {
	BaseEvent
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
}

type TextMessageContentEvent struct {
	BaseEvent
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

type TextMessageEndEvent struct {
	BaseEvent
	MessageID string `json:"messageId"`
}

type ThinkingStartEvent struct {
	BaseEvent
	ThinkingID string `json:"thinkingId"`
	Title      string `json:"title,omitempty"`
}

type ThinkingTextMessageStartEvent struct {
	BaseEvent
	ThinkingID string `json:"thinkingId"`
	MessageID  string `json:"messageId"`
}

type ThinkingTextMessageContentEvent struct {
	BaseEvent
	ThinkingID string `json:"thinkingId"`
	MessageID  string `json:"messageId"`
	Delta      string `json:"delta"`
}

type ThinkingTextMessageEndEvent struct {
	BaseEvent
	ThinkingID string `json:"thinkingId"`
	MessageID  string `json:"messageId"`
}

type ThinkingEndEvent struct {
	BaseEvent
	ThinkingID string `json:"thinkingId"`
}

type ToolCallStartEvent struct {
	BaseEvent
	ToolCallID      string `json:"toolCallId"`
	ToolCallName    string `json:"toolCallName"`
	ParentMessageID string `json:"parentMessageId,omitempty"`
}

type ToolCallArgsEvent struct {
	BaseEvent
	ToolCallID string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

type ToolCallEndEvent struct {
	BaseEvent
	ToolCallID string `json:"toolCallId"`
}

type ToolCallResultEvent struct {
	BaseEvent
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Content    string `json:"content"`
	Role       string `json:"role,omitempty"`
}

type MessagesSnapshotEvent struct {
	BaseEvent
	Messages []Message `json:"messages"`
}

type StateSnapshotEvent struct {
	BaseEvent
	Snapshot any `json:"snapshot"`
}

type StateDeltaEvent struct {
	BaseEvent
	Delta []jsonPatchOp `json:"delta"`
}

type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

type CustomEvent struct {
	BaseEvent
	Name    string `json:"name"`
	Value   any    `json:"value,omitempty"`
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
	MessageRoleTool      MessageRole = "tool"
	MessageRoleDeveloper MessageRole = "developer"
)

type Message struct {
	ID              string      `json:"id"`
	Role            MessageRole `json:"role"`
	Content         string      `json:"content,omitempty"`
	Name            string      `json:"name,omitempty"`
	ToolCalls       []ToolCall  `json:"toolCalls,omitempty"`
	ToolCallID      string      `json:"toolCallId,omitempty"`
	Error           string      `json:"error,omitempty"`
	EncryptedValue  string      `json:"encryptedValue,omitempty"`
}

type ToolCall struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function ToolCallFunc  `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type RunAgentInput struct {
	ThreadID       string         `json:"threadId,omitempty"`
	RunID          string         `json:"runId,omitempty"`
	ParentRunID    string         `json:"parentRunId,omitempty"`
	Messages       []Message      `json:"messages,omitempty"`
	Tools          []ToolDef      `json:"tools,omitempty"`
	Context        []ContextEntry `json:"context,omitempty"`
	State          any            `json:"state,omitempty"`
	ForwardedProps any            `json:"forwardedProps,omitempty"`
	Resume         []ResumeEntry  `json:"resume,omitempty"`
}

type ResumeEntry struct {
	InterruptID string `json:"interruptId"`
	Status      string `json:"status"`
	Payload     any    `json:"payload,omitempty"`
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  any            `json:"parameters,omitempty"`
}

type ContextEntry struct {
	Description string `json:"description,omitempty"`
	Value       string `json:"value,omitempty"`
}

type AgentCapabilities struct {
	Identity       *IdentityCapabilities       `json:"identity,omitempty"`
	Transport      *TransportCapabilities      `json:"transport,omitempty"`
	Tools          *ToolsCapabilities          `json:"tools,omitempty"`
	Output         *OutputCapabilities         `json:"output,omitempty"`
	State          *StateCapabilities          `json:"state,omitempty"`
	MultiAgent     *MultiAgentCapabilities     `json:"multiAgent,omitempty"`
	Reasoning      *ReasoningCapabilities      `json:"reasoning,omitempty"`
	Multimodal     *MultimodalCapabilities     `json:"multimodal,omitempty"`
	Execution      *ExecutionCapabilities      `json:"execution,omitempty"`
	HumanInTheLoop *HumanInTheLoopCapabilities `json:"humanInTheLoop,omitempty"`
	Custom         map[string]any              `json:"custom,omitempty"`
}

type IdentityCapabilities struct {
	Name            string         `json:"name,omitempty"`
	Type            string         `json:"type,omitempty"`
	Description     string         `json:"description,omitempty"`
	Version         string         `json:"version,omitempty"`
	Provider        string         `json:"provider,omitempty"`
	DocumentationURL string        `json:"documentationUrl,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type TransportCapabilities struct {
	Streaming        bool `json:"streaming,omitempty"`
	Websocket       bool `json:"websocket,omitempty"`
	HTTPBinary      bool `json:"httpBinary,omitempty"`
	PushNotifications bool `json:"pushNotifications,omitempty"`
	Resumable       bool `json:"resumable,omitempty"`
}

type ToolsCapabilities struct {
	Supported      bool      `json:"supported,omitempty"`
	Items          []ToolDef `json:"items,omitempty"`
	ParallelCalls  bool      `json:"parallelCalls,omitempty"`
	ClientProvided bool      `json:"clientProvided,omitempty"`
}

type OutputCapabilities struct {
	StructuredOutput   bool     `json:"structuredOutput,omitempty"`
	SupportedMIMETypes []string `json:"supportedMimeTypes,omitempty"`
}

type StateCapabilities struct {
	Snapshots      bool `json:"snapshots,omitempty"`
	Deltas         bool `json:"deltas,omitempty"`
	Memory         bool `json:"memory,omitempty"`
	PersistentState bool `json:"persistentState,omitempty"`
}

type MultiAgentCapabilities struct {
	Supported  bool                       `json:"supported,omitempty"`
	Delegation bool                       `json:"delegation,omitempty"`
	Handoffs   bool                       `json:"handoffs,omitempty"`
	SubAgents  []SubAgentDescriptor       `json:"subAgents,omitempty"`
}

type SubAgentDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ReasoningCapabilities struct {
	Supported bool `json:"supported,omitempty"`
	Streaming bool `json:"streaming,omitempty"`
	Encrypted bool `json:"encrypted,omitempty"`
}

type MultimodalCapabilities struct {
	Input  *MultimodalInputCapabilities  `json:"input,omitempty"`
	Output *MultimodalOutputCapabilities `json:"output,omitempty"`
}

type MultimodalInputCapabilities struct {
	Image bool `json:"image,omitempty"`
	Audio bool `json:"audio,omitempty"`
	Video bool `json:"video,omitempty"`
	PDF   bool `json:"pdf,omitempty"`
	File  bool `json:"file,omitempty"`
}

type MultimodalOutputCapabilities struct {
	Image bool `json:"image,omitempty"`
	Audio bool `json:"audio,omitempty"`
}

type ExecutionCapabilities struct {
	CodeExecution   bool  `json:"codeExecution,omitempty"`
	Sandboxed       bool  `json:"sandboxed,omitempty"`
	MaxIterations   int64 `json:"maxIterations,omitempty"`
	MaxExecutionTime int64 `json:"maxExecutionTime,omitempty"`
}

type HumanInTheLoopCapabilities struct {
	Supported        bool `json:"supported,omitempty"`
	Approvals        bool `json:"approvals,omitempty"`
	Interventions    bool `json:"interventions,omitempty"`
	Feedback         bool `json:"feedback,omitempty"`
	Interrupts       bool `json:"interrupts,omitempty"`
	ApproveWithEdits bool `json:"approveWithEdits,omitempty"`
}
