package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

type lifecycleStore struct{ metas []SessionMeta }

func (s *lifecycleStore) LoadSessionMeta(id string) (SessionMeta, error) {
	for _, meta := range s.metas {
		if meta.SessionID == id {
			return meta, nil
		}
	}
	return SessionMeta{}, io.EOF
}

func (*lifecycleStore) SaveSessionMeta(SessionMeta) error { return nil }
func (s *lifecycleStore) ListSessions(string) []SessionMeta {
	return append([]SessionMeta(nil), s.metas...)
}

type lifecycleAgent struct {
	core    *agentcore.Agent
	started chan struct{}
	once    sync.Once
}

func (a *lifecycleAgent) Run(ctx context.Context, _ string) (string, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	return "", ctx.Err()
}
func (a *lifecycleAgent) Core() *agentcore.Agent { return a.core }
func (*lifecycleAgent) Model() string            { return "test" }
func (*lifecycleAgent) Mode() string             { return "test" }

type lifecycleFactory struct {
	created atomic.Int64
	agent   *lifecycleAgent
}

func (f *lifecycleFactory) CreateAgent(context.Context, string, string, string, string) (AgentInstance, error) {
	f.created.Add(1)
	if f.agent == nil {
		f.agent = &lifecycleAgent{core: agentcore.New(agentcore.Config{}), started: make(chan struct{})}
	}
	return f.agent, nil
}
func (*lifecycleFactory) DefaultModel() string          { return "test" }
func (*lifecycleFactory) DefaultMode() string           { return "test" }
func (*lifecycleFactory) AvailableModes() []SessionMode { return nil }

type streamingACPProvider struct{}

func (streamingACPProvider) Complete(context.Context, *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	return &agentcore.ProviderResponse{Content: "hello"}, nil
}

func (streamingACPProvider) Stream(context.Context, *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	stream := make(chan agentcore.StreamDelta, 2)
	stream <- agentcore.StreamDelta{Content: "hello"}
	stream <- agentcore.StreamDelta{Done: true, FinishReason: "stop"}
	close(stream)
	return stream, nil
}

type coreAgentInstance struct{ core *agentcore.Agent }

func (a *coreAgentInstance) Run(ctx context.Context, input string) (string, error) {
	return a.core.Run(ctx, input)
}
func (a *coreAgentInstance) Core() *agentcore.Agent { return a.core }
func (*coreAgentInstance) Model() string            { return "test" }
func (*coreAgentInstance) Mode() string             { return "test" }

type coreAgentFactory struct{ instance *coreAgentInstance }

func (f *coreAgentFactory) CreateAgent(context.Context, string, string, string, string) (AgentInstance, error) {
	if f.instance == nil {
		f.instance = &coreAgentInstance{core: agentcore.New(agentcore.NewConfig(
			agentcore.WithProvider(streamingACPProvider{}),
			agentcore.WithStreaming(true),
		))}
	}
	return f.instance, nil
}
func (*coreAgentFactory) DefaultModel() string          { return "test" }
func (*coreAgentFactory) DefaultMode() string           { return "test" }
func (*coreAgentFactory) AvailableModes() []SessionMode { return nil }

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

type blockingReader struct{ released chan struct{} }

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.released
	return 0, io.EOF
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b.Bytes()...)
}

func TestSessionManagerRestoresPersistedSessionsLazily(t *testing.T) {
	factory := &lifecycleFactory{}
	store := &lifecycleStore{metas: []SessionMeta{{SessionID: "persisted", CWD: "/tmp", Model: "test", Mode: "test"}}}
	manager := NewSessionManager(SessionManagerConfig{AgentFactory: factory, SessionStore: store})
	if factory.created.Load() != 0 {
		t.Fatal("persisted agents must not be created during manager startup")
	}
	if sessions := manager.ListSessions(""); len(sessions) != 1 || sessions[0].SessionID != "persisted" {
		t.Fatalf("persisted metadata missing from list: %+v", sessions)
	}
	if _, err := manager.RestoreSession("persisted"); err != nil {
		t.Fatal(err)
	}
	if factory.created.Load() != 1 {
		t.Fatalf("created agents = %d, want 1", factory.created.Load())
	}
	manager.Cleanup()
}

func TestACPRejectsOverlappingPromptAndCancelHasNoResponse(t *testing.T) {
	factory := &lifecycleFactory{}
	manager := NewSessionManager(SessionManagerConfig{AgentFactory: factory})
	state, err := manager.CreateSession(t.TempDir(), "session")
	if err != nil {
		t.Fatal(err)
	}
	output := &lockedBuffer{}
	server := NewServer(ServerConfig{SessionManager: manager, Reader: bytes.NewReader(nil), Writer: output})
	prompt, _ := json.Marshal(map[string]any{"sessionId": state.SessionID, "prompt": []map[string]any{{"type": "text", "text": "hello"}}})
	server.handlePrompt(context.Background(), &JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "session/prompt", Params: prompt})
	<-factory.agent.started

	server.handlePrompt(context.Background(), &JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "session/prompt", Params: prompt})
	if !bytes.Contains(output.Bytes(), []byte("Session already running")) {
		t.Fatalf("overlapping prompt was not rejected: %s", output.Bytes())
	}

	beforeCancel := len(output.Bytes())
	cancelParams, _ := json.Marshal(CancelParams{SessionID: state.SessionID})
	server.handleCancel(&JSONRPCRequest{JSONRPC: "2.0", Method: "session/cancel", Params: cancelParams})
	if len(output.Bytes()) != beforeCancel {
		t.Fatalf("cancel notification produced a response: %s", output.Bytes()[beforeCancel:])
	}
	manager.Cleanup()
}

func TestACPStreamingOutputIsNotRepeatedAtCompletion(t *testing.T) {
	factory := &coreAgentFactory{}
	manager := NewSessionManager(SessionManagerConfig{AgentFactory: factory})
	state, err := manager.CreateSession(t.TempDir(), "session")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Cleanup()
	output := &lockedBuffer{}
	server := NewServer(ServerConfig{SessionManager: manager, Reader: bytes.NewReader(nil), Writer: output})
	prompt, _ := json.Marshal(map[string]any{"sessionId": state.SessionID, "prompt": []map[string]any{{"type": "text", "text": "start"}}})
	server.handlePrompt(context.Background(), &JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "session/prompt", Params: prompt})

	deadline := time.Now().Add(time.Second)
	for !bytes.Contains(output.Bytes(), []byte(`"id":1`)) {
		if time.Now().After(deadline) {
			t.Fatalf("prompt did not complete: %s", output.Bytes())
		}
		time.Sleep(time.Millisecond)
	}
	if count := bytes.Count(output.Bytes(), []byte("hello")); count != 1 {
		t.Fatalf("streamed output occurrence count = %d, want 1: %s", count, output.Bytes())
	}
}

func TestACPInitializeNegotiatesToSupportedProtocolVersion(t *testing.T) {
	output := &lockedBuffer{}
	server := NewServer(ServerConfig{Writer: output})
	params, _ := json.Marshal(InitializeParams{ProtocolVersion: ProtocolVersion + 1})
	server.handleInitialize(&JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: params})

	var response JSONRPCResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("response error = %+v", response.Error)
	}
	var result InitializeResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Fatalf("negotiated version = %d, want %d", result.ProtocolVersion, ProtocolVersion)
	}
}

func TestACPRunReturnsWhenContextCancelledDuringBlockedRead(t *testing.T) {
	reader := &blockingReader{released: make(chan struct{})}
	server := NewServer(ServerConfig{Reader: reader, Writer: io.Discard})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ACP Run did not return after context cancellation")
	}
	close(reader.released)
}
