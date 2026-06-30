package acp

import (
	"context"

	"github.com/covoyage/covonaut/agentcore"
)

type AgentInfo struct {
	Name    string
	Version string
}

type AuthProvider interface {
	AuthMethods() []any
}

type AgentFactory interface {
	CreateAgent(ctx context.Context, sessionID, cwd, model, mode string) (AgentInstance, error)
	DefaultModel() string
	DefaultMode() string
	AvailableModes() []SessionMode
}

type AgentInstance interface {
	Run(ctx context.Context, input string) (string, error)
	Core() *agentcore.Agent
	Model() string
	Mode() string
}

// PermissionAware is an optional interface an AgentInstance can implement to
// route tool-call authorization to the ACP client (editor). When set, the
// instance consults fn before executing a tool that requires permission; fn
// returns true to allow, false to deny.
type PermissionAware interface {
	SetPermissionRequester(fn func(toolCallID, name string, rawInput any) bool)
}

// ACPFileSystem is the editor-backed filesystem exposed to an agent instance so
// its read/write tools can see unsaved buffers via the ACP client.
type ACPFileSystem interface {
	ReadTextFile(path string) ([]byte, error)
	WriteTextFile(path string, content []byte) error
}

// Rebuildable is an optional interface an AgentInstance can implement to
// support runtime mode/model changes. When not implemented, SetSessionMode
// and SetSessionModel only update persisted metadata.
type Rebuildable interface {
	Rebuild(mode, model string) error
}

// FileSystemAware is an optional interface an AgentInstance can implement to
// route file reads/writes through the ACP client (editor). Set only when the
// client advertised filesystem capability.
type FileSystemAware interface {
	SetFileSystem(fs ACPFileSystem)
}

type SessionStore interface {
	LoadSessionMeta(sessionID string) (SessionMeta, error)
	SaveSessionMeta(meta SessionMeta) error
	ListSessions(cwd string) []SessionMeta
}

type SessionMeta struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Model     string `json:"model"`
	Mode      string `json:"mode"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updated_at"`
}