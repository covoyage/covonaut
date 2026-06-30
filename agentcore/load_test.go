package agentcore

import (
	"context"
	"errors"
	"testing"

	"github.com/covoyage/covonaut/skill"
)

type mockThreadStore struct {
	cfg *CallConfig
	has bool
	err error
}

func (m *mockThreadStore) GetThreadConfig(_ context.Context, _ string) (*CallConfig, bool, error) {
	return m.cfg, m.has, m.err
}

type mockStore struct {
	has bool
	err error
}

func (m *mockStore) Save(_ context.Context, _ string, _ StateSnapshot) error {
	return nil
}
func (m *mockStore) Load(_ context.Context, _ string) (StateSnapshot, error) {
	return StateSnapshot{}, m.err
}
func (m *mockStore) Has(_ context.Context, _ string) (bool, error) {
	return m.has, m.err
}
func (m *mockStore) Delete(_ context.Context, _ string) error {
	return nil
}
func (m *mockStore) List(_ context.Context) ([]string, error) {
	return nil, nil
}

func TestLoadAgentBasic(t *testing.T) {
	cfg := Config{}
	cfg.Model = "test-model"
	cfg.Name = "test-agent"
	agent, err := LoadAgent(context.Background(), cfg, LoadAgentOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
	if agent.Config().Model != "test-model" {
		t.Fatalf("model = %q", agent.Config().Model)
	}
}

func TestLoadAgentWithThreadConfig(t *testing.T) {
	cfg := Config{}
	cfg.Model = "base-model"
	opts := LoadAgentOptions{
		ThreadID: "thread-1",
		ThreadCfgProvider: &mockThreadStore{
			cfg: &CallConfig{Model: "thread-model"},
			has: true,
		},
	}
	agent, err := LoadAgent(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.Config().Model != "thread-model" {
		t.Fatalf("model = %q, want thread-model", agent.Config().Model)
	}
}

func TestLoadAgentWithCallCfg(t *testing.T) {
	cfg := Config{}
	cfg.Model = "base-model"
	opts := LoadAgentOptions{
		ThreadID: "thread-1",
		CallCfg:  &CallConfig{Model: "request-model"},
		ThreadCfgProvider: &mockThreadStore{
			cfg: &CallConfig{Model: "thread-model"},
			has: true,
		},
	}
	agent, err := LoadAgent(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CallCfg should override thread config
	if agent.Config().Model != "request-model" {
		t.Fatalf("model = %q, want request-model", agent.Config().Model)
	}
}

func TestLoadAgentThreadStoreError(t *testing.T) {
	cfg := Config{}
	cfg.Model = "m"
	opts := LoadAgentOptions{
		ThreadID: "tid",
		ThreadCfgProvider: &mockThreadStore{err: errors.New("store error")},
	}
	_, err := LoadAgent(context.Background(), cfg, opts)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadAgentUnknownSkills(t *testing.T) {
	cfg := Config{}
	cfg.Model = "m"
	cfg.SelectedSkills = []string{"nonexistent"}
	// No AvailableSkills means no validation happens
	_, err := LoadAgent(context.Background(), cfg, LoadAgentOptions{})
	if err != nil {
		t.Fatalf("expected no error when AvailableSkills is nil: %v", err)
	}
}

func TestLoadAgentMissingSelectedSkills(t *testing.T) {
	cfg := Config{}
	cfg.Model = "m"
	cfg.AvailableSkills = []skill.Skill{{Name: "existing"}}
	cfg.SelectedSkills = []string{"nonexistent"}
	_, err := LoadAgent(context.Background(), cfg, LoadAgentOptions{})
	if err == nil {
		t.Fatal("expected error for missing skill")
	}
}

func TestLoadAgentWithState(t *testing.T) {
	cfg := Config{}
	cfg.Model = "m"
	cfg.Store = &mockStore{has: true}
	cfg.Checkpoint = &CheckpointSettings{}
	opts := LoadAgentOptions{ThreadID: "tid"}
	agent, err := LoadAgent(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestLoadAgentNoThread(t *testing.T) {
	cfg := Config{}
	cfg.Model = "m"
	cfg.Store = &mockStore{has: true}
	// No ThreadID means no state loading even with Store
	agent, err := LoadAgent(context.Background(), cfg, LoadAgentOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
}
