package server

import (
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/mcp"
	"github.com/covoyage/covonaut/pkg/util"
	"github.com/covoyage/covonaut/skill"
)

const (
	streamSchemaAgentEvent          = "agent.event.v1"
	streamSchemaMCPAbilitiesUpdated = "mcp.capabilities_updated.v1"
	streamSchemaMCPToolsRefreshed   = "mcp.tools_refreshed.v1"
	streamSchemaMCPTransportError   = "mcp.transport_error.v1"
	streamSchemaMCPReconnect        = "mcp.reconnect.v1"
	streamSchemaMCPRefresh          = "mcp.refresh.v1"
	streamSchemaSkillsSnapshot      = "skills.snapshot.v1"
	streamSchemaChatDone            = "chat.done.v1"
)

type MCPStreamCapabilitiesEvent struct {
	Schema       string                 `json:"schema"`
	Type         string                 `json:"type"`
	ThreadID     string                 `json:"thread_id,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
	Extension    string                 `json:"extension"`
	Transport    string                 `json:"transport"`
	Capabilities mcp.ServerCapabilities `json:"capabilities"`
}

type MCPStreamToolsRefreshedEvent struct {
	Schema    string    `json:"schema"`
	Type      string    `json:"type"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Extension string    `json:"extension"`
	Transport string    `json:"transport"`
	OldTools  []string  `json:"old_tools"`
	NewTools  []string  `json:"new_tools"`
}

type MCPStreamTransportErrorEvent struct {
	Schema      string    `json:"schema"`
	Type        string    `json:"type"`
	ThreadID    string    `json:"thread_id,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Extension   string    `json:"extension"`
	Transport   string    `json:"transport"`
	Operation   string    `json:"operation"`
	Message     string    `json:"message"`
	Reason      string    `json:"reason,omitempty"`
	StatusCode  int       `json:"status_code,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	LastEventID string    `json:"last_event_id,omitempty"`
	Recoverable bool      `json:"recoverable,omitempty"`
}

type MCPStreamReconnectEvent struct {
	Schema         string    `json:"schema"`
	Type           string    `json:"type"`
	ThreadID       string    `json:"thread_id,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
	Extension      string    `json:"extension"`
	Transport      string    `json:"transport"`
	Phase          string    `json:"phase"`
	Reason         string    `json:"reason"`
	Attempt        int       `json:"attempt,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	StaleSessionID string    `json:"stale_session_id,omitempty"`
	LastEventID    string    `json:"last_event_id,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type MCPStreamRefreshEvent struct {
	Schema    string    `json:"schema"`
	Type      string    `json:"type"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Extension string    `json:"extension"`
	Transport string    `json:"transport"`
	Phase     string    `json:"phase"`
	Reason    string    `json:"reason,omitempty"`
	Error     string    `json:"error,omitempty"`
	Coalesced bool      `json:"coalesced,omitempty"`
	InFlight  bool      `json:"in_flight,omitempty"`
}

type AgentStreamEvent struct {
	Schema    string    `json:"schema"`
	Type      string    `json:"type"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload"`
}

type SkillsSnapshotStreamEvent struct {
	Schema    string                      `json:"schema"`
	Type      string                      `json:"type"`
	Timestamp time.Time                   `json:"timestamp"`
	Payload   SkillRegistryStatusResponse `json:"payload"`
}

type StreamDoneEvent struct {
	Schema   string `json:"schema"`
	Type     string `json:"type"`
	ThreadID string `json:"thread_id,omitempty"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

type AgentStartStreamPayload struct {
	AgentName string `json:"agent_name,omitempty"`
	Input     string `json:"input,omitempty"`
}

type AgentEndStreamPayload struct {
	AgentName string `json:"agent_name,omitempty"`
	Output    string `json:"output"`
}

type AgentErrorStreamPayload struct {
	Error string `json:"error,omitempty"`
}

type SkillLoadedStreamPayload struct {
	SkillName string `json:"skill_name"`
	Path      string `json:"path,omitempty"`
	Source    string `json:"source"`
	Arguments string `json:"arguments,omitempty"`
}

type SkillsReloadedStreamPayload struct {
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

type TurnStartStreamPayload struct {
	Turn int64 `json:"turn"`
}

type TurnEndStreamPayload struct {
	Turn  int64                `json:"turn"`
	Usage agentcore.TokenUsage `json:"usage"`
}

type MessageDeltaStreamPayload struct {
	Delta string `json:"delta"`
}

type ToolCallStartStreamPayload struct {
	ToolCall agentcore.ToolCall `json:"tool_call"`
}

type ToolCallEndStreamPayload struct {
	ToolCallID string        `json:"tool_call_id"`
	ToolName   string        `json:"tool_name"`
	Result     string        `json:"result"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"duration"`
}

type HandoffStartStreamPayload struct {
	SourceAgent string `json:"source_agent"`
	TargetAgent string `json:"target_agent"`
	Mode        string `json:"mode"`
	Context     string `json:"context"`
}

type HandoffEndStreamPayload struct {
	TargetAgent string        `json:"target_agent"`
	Output      string        `json:"output"`
	Duration    time.Duration `json:"duration"`
	Error       string        `json:"error,omitempty"`
}

type CompactionStartStreamPayload struct {
	TokensBefore  int64 `json:"tokens_before"`
	ContextWindow int64 `json:"context_window"`
}

type CompactionEndStreamPayload struct {
	TokensBefore int64         `json:"tokens_before"`
	TokensAfter  int64         `json:"tokens_after"`
	MessagesCut  int64         `json:"messages_cut"`
	Duration     time.Duration `json:"duration"`
}

type AutoRetryStreamPayload struct {
	Attempt    int64         `json:"attempt"`
	MaxRetries int64         `json:"max_retries"`
	Delay      time.Duration `json:"delay"`
	Error      string        `json:"error,omitempty"`
}

func streamEventPayload(threadID string, e agentcore.Event) any {
	switch ev := e.(type) {
	case mcp.CapabilitiesUpdatedEvent:
		return MCPStreamCapabilitiesEvent{
			Schema:       streamSchemaMCPAbilitiesUpdated,
			Type:         string(ev.EventKind()),
			ThreadID:     threadID,
			Timestamp:    ev.EventTime(),
			Extension:    ev.Extension,
			Transport:    ev.Transport,
			Capabilities: ev.Capabilities,
		}
	case mcp.ToolsRefreshedEvent:
		return MCPStreamToolsRefreshedEvent{
			Schema:    streamSchemaMCPToolsRefreshed,
			Type:      string(ev.EventKind()),
			ThreadID:  threadID,
			Timestamp: ev.EventTime(),
			Extension: ev.Extension,
			Transport: ev.Transport,
			OldTools:  append([]string(nil), ev.OldTools...),
			NewTools:  append([]string(nil), ev.NewTools...),
		}
	case mcp.TransportErrorEvent:
		return MCPStreamTransportErrorEvent{
			Schema:      streamSchemaMCPTransportError,
			Type:        string(ev.EventKind()),
			ThreadID:    threadID,
			Timestamp:   ev.EventTime(),
			Extension:   ev.Extension,
			Transport:   ev.Transport,
			Operation:   ev.Operation,
			Message:     ev.Message,
			Reason:      ev.Reason,
			StatusCode:  ev.StatusCode,
			SessionID:   ev.SessionID,
			LastEventID: ev.LastEventID,
			Recoverable: ev.Recoverable,
		}
	case mcp.ReconnectEvent:
		return MCPStreamReconnectEvent{
			Schema:         streamSchemaMCPReconnect,
			Type:           string(ev.EventKind()),
			ThreadID:       threadID,
			Timestamp:      ev.EventTime(),
			Extension:      ev.Extension,
			Transport:      ev.Transport,
			Phase:          ev.Phase,
			Reason:         ev.Reason,
			Attempt:        ev.Attempt,
			SessionID:      ev.SessionID,
			StaleSessionID: ev.StaleSessionID,
			LastEventID:    ev.LastEventID,
			Error:          ev.Error,
		}
	case mcp.RefreshEvent:
		return MCPStreamRefreshEvent{
			Schema:    streamSchemaMCPRefresh,
			Type:      string(ev.EventKind()),
			ThreadID:  threadID,
			Timestamp: ev.EventTime(),
			Extension: ev.Extension,
			Transport: ev.Transport,
			Phase:     ev.Phase,
			Reason:    ev.Reason,
			Error:     ev.Error,
			Coalesced: ev.Coalesced,
			InFlight:  ev.InFlight,
		}
	default:
		return agentEventEnvelope(threadID, e)
	}
}

func agentEventEnvelope(threadID string, e agentcore.Event) AgentStreamEvent {
	return AgentStreamEvent{
		Schema:    streamSchemaAgentEvent,
		Type:      string(e.EventKind()),
		ThreadID:  threadID,
		Timestamp: e.EventTime(),
		Payload:   agentEventPayload(e),
	}
}

func agentEventPayload(e agentcore.Event) any {
	switch ev := e.(type) {
	case agentcore.AgentStartEvent:
		return AgentStartStreamPayload{
			AgentName: ev.AgentName,
			Input:     ev.Input,
		}
	case *agentcore.AgentStartEvent:
		return AgentStartStreamPayload{
			AgentName: ev.AgentName,
			Input:     ev.Input,
		}
	case agentcore.AgentEndEvent:
		return AgentEndStreamPayload{
			AgentName: ev.AgentName,
			Output:    ev.Output,
		}
	case *agentcore.AgentEndEvent:
		return AgentEndStreamPayload{
			AgentName: ev.AgentName,
			Output:    ev.Output,
		}
	case agentcore.AgentErrorEvent:
		return AgentErrorStreamPayload{
			Error: util.ErrorString(ev.Err),
		}
	case *agentcore.AgentErrorEvent:
		return AgentErrorStreamPayload{
			Error: util.ErrorString(ev.Err),
		}
	case agentcore.SkillLoadedEvent:
		return SkillLoadedStreamPayload{
			SkillName: ev.SkillName,
			Path:      ev.Path,
			Source:    ev.Source,
			Arguments: ev.Arguments,
		}
	case *agentcore.SkillLoadedEvent:
		return SkillLoadedStreamPayload{
			SkillName: ev.SkillName,
			Path:      ev.Path,
			Source:    ev.Source,
			Arguments: ev.Arguments,
		}
	case agentcore.SkillsReloadedEvent:
		return SkillsReloadedStreamPayload{
			SkillPaths:         append([]string(nil), ev.SkillPaths...),
			TotalSkills:        ev.TotalSkills,
			VisibleSkills:      ev.VisibleSkills,
			HiddenSkills:       ev.HiddenSkills,
			DiagnosticsCount:   ev.DiagnosticsCount,
			AddedSkills:        append([]string(nil), ev.AddedSkills...),
			RemovedSkills:      append([]string(nil), ev.RemovedSkills...),
			UpdatedSkills:      append([]string(nil), ev.UpdatedSkills...),
			AddedDiagnostics:   append([]skill.Diagnostic(nil), ev.AddedDiagnostics...),
			RemovedDiagnostics: append([]skill.Diagnostic(nil), ev.RemovedDiagnostics...),
		}
	case *agentcore.SkillsReloadedEvent:
		return SkillsReloadedStreamPayload{
			SkillPaths:         append([]string(nil), ev.SkillPaths...),
			TotalSkills:        ev.TotalSkills,
			VisibleSkills:      ev.VisibleSkills,
			HiddenSkills:       ev.HiddenSkills,
			DiagnosticsCount:   ev.DiagnosticsCount,
			AddedSkills:        append([]string(nil), ev.AddedSkills...),
			RemovedSkills:      append([]string(nil), ev.RemovedSkills...),
			UpdatedSkills:      append([]string(nil), ev.UpdatedSkills...),
			AddedDiagnostics:   append([]skill.Diagnostic(nil), ev.AddedDiagnostics...),
			RemovedDiagnostics: append([]skill.Diagnostic(nil), ev.RemovedDiagnostics...),
		}
	case agentcore.TurnStartEvent:
		return TurnStartStreamPayload{Turn: ev.Turn}
	case *agentcore.TurnStartEvent:
		return TurnStartStreamPayload{Turn: ev.Turn}
	case agentcore.TurnEndEvent:
		return TurnEndStreamPayload{
			Turn:  ev.Turn,
			Usage: ev.Usage,
		}
	case *agentcore.TurnEndEvent:
		return TurnEndStreamPayload{
			Turn:  ev.Turn,
			Usage: ev.Usage,
		}
	case agentcore.MessageDeltaEvent:
		return MessageDeltaStreamPayload{Delta: ev.Delta}
	case *agentcore.MessageDeltaEvent:
		return MessageDeltaStreamPayload{Delta: ev.Delta}
	case agentcore.ToolCallStartEvent:
		return ToolCallStartStreamPayload{ToolCall: ev.ToolCall}
	case *agentcore.ToolCallStartEvent:
		return ToolCallStartStreamPayload{ToolCall: ev.ToolCall}
	case agentcore.ToolCallEndEvent:
		return ToolCallEndStreamPayload{
			ToolCallID: ev.ToolCallID,
			ToolName:   ev.ToolName,
			Result:     ev.Result,
			Error:      util.ErrorString(ev.Err),
			Duration:   ev.Duration,
		}
	case *agentcore.ToolCallEndEvent:
		return ToolCallEndStreamPayload{
			ToolCallID: ev.ToolCallID,
			ToolName:   ev.ToolName,
			Result:     ev.Result,
			Error:      util.ErrorString(ev.Err),
			Duration:   ev.Duration,
		}
	case agentcore.HandoffStartEvent:
		return HandoffStartStreamPayload{
			SourceAgent: ev.SourceAgent,
			TargetAgent: ev.TargetAgent,
			Mode:        ev.Mode,
			Context:     ev.Context,
		}
	case *agentcore.HandoffStartEvent:
		return HandoffStartStreamPayload{
			SourceAgent: ev.SourceAgent,
			TargetAgent: ev.TargetAgent,
			Mode:        ev.Mode,
			Context:     ev.Context,
		}
	case agentcore.HandoffEndEvent:
		return HandoffEndStreamPayload{
			TargetAgent: ev.TargetAgent,
			Output:      ev.Output,
			Duration:    ev.Duration,
			Error:       util.ErrorString(ev.Err),
		}
	case *agentcore.HandoffEndEvent:
		return HandoffEndStreamPayload{
			TargetAgent: ev.TargetAgent,
			Output:      ev.Output,
			Duration:    ev.Duration,
			Error:       util.ErrorString(ev.Err),
		}
	case agentcore.CompactionStartEvent:
		return CompactionStartStreamPayload{
			TokensBefore:  ev.TokensBefore,
			ContextWindow: ev.ContextWindow,
		}
	case *agentcore.CompactionStartEvent:
		return CompactionStartStreamPayload{
			TokensBefore:  ev.TokensBefore,
			ContextWindow: ev.ContextWindow,
		}
	case agentcore.CompactionEndEvent:
		return CompactionEndStreamPayload{
			TokensBefore: ev.TokensBefore,
			TokensAfter:  ev.TokensAfter,
			MessagesCut:  ev.MessagesCut,
			Duration:     ev.Duration,
		}
	case *agentcore.CompactionEndEvent:
		return CompactionEndStreamPayload{
			TokensBefore: ev.TokensBefore,
			TokensAfter:  ev.TokensAfter,
			MessagesCut:  ev.MessagesCut,
			Duration:     ev.Duration,
		}
	case agentcore.AutoRetryEvent:
		return AutoRetryStreamPayload{
			Attempt:    ev.Attempt,
			MaxRetries: ev.MaxRetries,
			Delay:      ev.Delay,
			Error:      util.ErrorString(ev.Err),
		}
	case *agentcore.AutoRetryEvent:
		return AutoRetryStreamPayload{
			Attempt:    ev.Attempt,
			MaxRetries: ev.MaxRetries,
			Delay:      ev.Delay,
			Error:      util.ErrorString(ev.Err),
		}
	default:
		return e
	}
}
