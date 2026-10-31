package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

type refusingTransitionAgent struct {
	core  *agentcore.Agent
	calls atomic.Int64
	fail  bool
}

func (a *refusingTransitionAgent) Core() *agentcore.Agent { return a.core }
func (*refusingTransitionAgent) Model() string            { return "old" }
func (*refusingTransitionAgent) Mode() string             { return "general" }
func (a *refusingTransitionAgent) Rebuild(string, string) error {
	a.calls.Add(1)
	if a.fail {
		return errors.New("compaction refused; old model preserved")
	}
	return nil
}
func (a *refusingTransitionAgent) Run(_ context.Context, _ string) (string, error) { return "", nil }

func TestACPModelAndModeFailureDoNotPublishMetadataOrSuccess(t *testing.T) {
	for _, kind := range []string{"model", "mode", "busy"} {
		t.Run(kind, func(t *testing.T) {
			manager := NewSessionManager(SessionManagerConfig{AgentFactory: &lifecycleFactory{}})
			state, err := manager.CreateSession(t.TempDir(), "session")
			if err != nil {
				t.Fatal(err)
			}

			defer manager.Cleanup()
			state.Model, state.Mode = "old", "general"
			mock := &refusingTransitionAgent{core: state.Agent.Core(), fail: true}
			state.Agent = mock
			writer := &lockedBuffer{}
			server := NewServer(ServerConfig{SessionManager: manager, Reader: bytes.NewReader(nil), Writer: writer})
			if kind == "mode" {
				params, _ := json.Marshal(SetSessionModeParams{SessionID: state.SessionID, ModeID: "code"})
				server.handleSetMode(context.Background(), &JSONRPCRequest{ID: 1, Params: params})
			} else {
				if kind == "busy" {
					manager.SetRunning(state.SessionID, func() {})
				}
				params, _ := json.Marshal(SetSessionModelParams{SessionID: state.SessionID, ModelID: "target"})
				server.handleSetModel(context.Background(), &JSONRPCRequest{ID: 1, Params: params})
			}
			if state.Model != "old" || state.Mode != "general" || !bytes.Contains(writer.Bytes(), []byte(`"error"`)) {
				t.Fatalf("failed transition changed metadata or reported success: %s", writer.Bytes())
			}
			if kind == "busy" && mock.calls.Load() != 0 {
				t.Fatal("busy session began a rebuild")
			}
		})
	}
}

type contextTransitionAgent struct {
	*refusingTransitionAgent
	contextSeen bool
}

func (a *contextTransitionAgent) RebuildContext(ctx context.Context, _ string, _ string) error {
	a.contextSeen = true
	return ctx.Err()
}

func TestACPTransitionPropagatesServerCancellation(t *testing.T) {
	mock := &contextTransitionAgent{refusingTransitionAgent: &refusingTransitionAgent{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rebuildSessionAgent(ctx, mock, "general", "target"); !errors.Is(err, context.Canceled) || !mock.contextSeen || mock.calls.Load() != 0 {
		t.Fatal("server cancellation was lost or legacy rebuild bypassed contextual admission")
	}
}
