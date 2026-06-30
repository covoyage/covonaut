package mcp

import (
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

const (
	// EventMCPCapabilitiesUpdated reports the currently negotiated MCP capabilities.
	EventMCPCapabilitiesUpdated agentcore.EventType = "mcp_capabilities_updated"
	// EventMCPToolsRefreshed reports that the extension-visible tool set changed.
	EventMCPToolsRefreshed agentcore.EventType = "mcp_tools_refreshed"
	// EventMCPTransportError reports transport-level async/runtime failures.
	EventMCPTransportError agentcore.EventType = "mcp_transport_error"
	// EventMCPReconnect reports reconnect/reinitialize lifecycle transitions.
	EventMCPReconnect agentcore.EventType = "mcp_reconnect"
	// EventMCPRefresh reports refresh scheduler lifecycle transitions.
	EventMCPRefresh agentcore.EventType = "mcp_refresh"
)

const (
	// ReconnectPhaseStarted marks the beginning of a reconnect/reinitialize attempt.
	ReconnectPhaseStarted = "started"
	// ReconnectPhaseSucceeded marks a successful reconnect/reinitialize attempt.
	ReconnectPhaseSucceeded = "succeeded"
	// ReconnectPhaseFailed marks a reconnect/reinitialize attempt that ended in error.
	ReconnectPhaseFailed = "failed"
	// ReconnectPhaseSkipped marks a reconnect request that became unnecessary.
	ReconnectPhaseSkipped = "skipped"
)

const (
	// ReconnectReasonSessionExpired indicates a retry caused by a stale MCP session.
	ReconnectReasonSessionExpired = "session_expired"
	// ReconnectReasonServerStream404 indicates the server-stream session expired.
	ReconnectReasonServerStream404 = "server_stream_404"
	// ReconnectReasonServerStreamEOF indicates the stream ended and will be retried.
	ReconnectReasonServerStreamEOF = "server_stream_eof"
	// ReconnectReasonServerStreamWake indicates the stream successfully resumed.
	ReconnectReasonServerStreamWake = "server_stream_resume"
)

const (
	// RefreshPhaseStarted marks the beginning of a refresh attempt.
	RefreshPhaseStarted = "started"
	// RefreshPhaseSucceeded marks a successful refresh.
	RefreshPhaseSucceeded = "succeeded"
	// RefreshPhaseFailed marks a refresh attempt that ended in error.
	RefreshPhaseFailed = "failed"
	// RefreshPhaseCoalesced marks a refresh request collapsed into an in-flight attempt.
	RefreshPhaseCoalesced = "coalesced"
	// RefreshPhaseSkipped marks a refresh that was suppressed, usually during shutdown.
	RefreshPhaseSkipped = "skipped"
)

// CapabilitiesUpdatedEvent reports the latest capability snapshot advertised by an MCP server.
type CapabilitiesUpdatedEvent struct {
	At           time.Time          `json:"timestamp"`
	Extension    string             `json:"extension"`
	Transport    string             `json:"transport"`
	Capabilities ServerCapabilities `json:"capabilities"`
}

func (e CapabilitiesUpdatedEvent) EventKind() agentcore.EventType { return EventMCPCapabilitiesUpdated }
func (e CapabilitiesUpdatedEvent) EventTime() time.Time           { return e.At }

// ToolsRefreshedEvent reports the before/after tool names visible through an extension.
type ToolsRefreshedEvent struct {
	At        time.Time `json:"timestamp"`
	Extension string    `json:"extension"`
	Transport string    `json:"transport"`
	OldTools  []string  `json:"old_tools"`
	NewTools  []string  `json:"new_tools"`
}

func (e ToolsRefreshedEvent) EventKind() agentcore.EventType { return EventMCPToolsRefreshed }
func (e ToolsRefreshedEvent) EventTime() time.Time           { return e.At }

// TransportErrorEvent reports a transport/runtime failure with enough context for observability.
type TransportErrorEvent struct {
	At          time.Time `json:"timestamp"`
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

func (e TransportErrorEvent) EventKind() agentcore.EventType { return EventMCPTransportError }
func (e TransportErrorEvent) EventTime() time.Time           { return e.At }

// ReconnectEvent reports the lifecycle of reconnect/reinitialize attempts.
type ReconnectEvent struct {
	At             time.Time `json:"timestamp"`
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

func (e ReconnectEvent) EventKind() agentcore.EventType { return EventMCPReconnect }
func (e ReconnectEvent) EventTime() time.Time           { return e.At }

// RefreshEvent reports tool refresh scheduler lifecycle state.
type RefreshEvent struct {
	At        time.Time `json:"timestamp"`
	Extension string    `json:"extension"`
	Transport string    `json:"transport"`
	Phase     string    `json:"phase"`
	Reason    string    `json:"reason,omitempty"`
	Error     string    `json:"error,omitempty"`
	Coalesced bool      `json:"coalesced,omitempty"`
	InFlight  bool      `json:"in_flight,omitempty"`
}

func (e RefreshEvent) EventKind() agentcore.EventType { return EventMCPRefresh }
func (e RefreshEvent) EventTime() time.Time           { return e.At }
