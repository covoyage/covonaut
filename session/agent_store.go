package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/covoyage/covonaut/agentcore"
)

const agentStateEntryKind = "agent_state"
const threadThinkingEntryKind = "thread_thinking"
const threadConfigEntryKind = "thread_config"

type agentStateData struct {
	Kind       string               `json:"kind"`
	Status     agentcore.Status     `json:"status"`
	Turn       int64                `json:"turn"`
	TotalUsage agentcore.TokenUsage `json:"total_usage"`
}

type threadThinkingData struct {
	Kind     string                    `json:"kind"`
	Thinking *agentcore.ThinkingConfig `json:"thinking,omitempty"`
}

type threadConfigData struct {
	Kind   string                `json:"kind"`
	Config *agentcore.CallConfig `json:"config,omitempty"`
}

// AgentStore adapts session.FileStore to agentcore.Store so thread state can be
// persisted as append-only JSONL sessions while preserving message history.
type AgentStore struct {
	sessions *FileStore
	cwd      string
}

// ThreadMessage is one transcript item with a stable entry id for branching.
type ThreadMessage struct {
	EntryID string            `json:"entry_id,omitempty"`
	Message agentcore.Message `json:"message"`
}

// ThreadSnapshot is a thread-oriented view over a persisted agent session.
type ThreadSnapshot struct {
	Info       Info                      `json:"info"`
	Messages   []agentcore.Message       `json:"messages"`
	Transcript []ThreadMessage           `json:"transcript,omitempty"`
	Status     agentcore.Status          `json:"status"`
	Turn       int64                     `json:"turn"`
	TotalUsage agentcore.TokenUsage      `json:"total_usage"`
	Config     *agentcore.CallConfig     `json:"config,omitempty"`
	Thinking   *agentcore.ThinkingConfig `json:"thinking,omitempty"`
}

// NewAgentStore wraps a session FileStore for agent state persistence.
func NewAgentStore(sessions *FileStore, cwd string) *AgentStore {
	return &AgentStore{sessions: sessions, cwd: cwd}
}

func (s *AgentStore) Save(ctx context.Context, key string, snap agentcore.StateSnapshot) error {
	mgr, err := s.openOrCreate(ctx, key)
	if err != nil {
		return err
	}
	threadCfg, threadCfgSet := latestThreadConfig(mgr)

	mgr, err = s.syncMessages(ctx, mgr, snap.Messages)
	if err != nil {
		return err
	}
	if threadCfgSet {
		currentCfg, currentSet := latestThreadConfig(mgr)
		if !currentSet || !reflect.DeepEqual(currentCfg, threadCfg) {
			if err := appendThreadConfig(ctx, mgr, threadCfg); err != nil {
				return err
			}
		}
	}

	meta, err := json.Marshal(agentStateData{
		Kind:       agentStateEntryKind,
		Status:     snap.Status,
		Turn:       snap.Turn,
		TotalUsage: snap.TotalUsage,
	})
	if err != nil {
		return fmt.Errorf("marshal agent state: %w", err)
	}

	if err := mgr.Append(ctx, Entry{Type: EntryCustom, Data: meta}); err != nil {
		return fmt.Errorf("append agent state: %w", err)
	}
	return nil
}

func (s *AgentStore) Load(ctx context.Context, key string) (agentcore.StateSnapshot, error) {
	mgr, err := s.sessions.Open(ctx, key)
	if err != nil {
		return agentcore.StateSnapshot{}, fmt.Errorf("open agent session: %w", err)
	}

	snap := agentcore.StateSnapshot{
		Messages: mgr.MessagesOnPath(),
	}

	if meta, ok := latestAgentState(mgr); ok {
		snap.Status = meta.Status
		snap.Turn = meta.Turn
		snap.TotalUsage = meta.TotalUsage
	} else if len(snap.Messages) > 0 {
		snap.Status = agentcore.StatusFinished
	} else {
		snap.Status = agentcore.StatusIdle
	}

	return snap, nil
}

func (s *AgentStore) Delete(ctx context.Context, key string) error {
	return s.sessions.Delete(ctx, key)
}

func (s *AgentStore) Has(ctx context.Context, key string) (bool, error) {
	return s.sessions.Has(ctx, key)
}

func (s *AgentStore) List(ctx context.Context) ([]string, error) {
	info, err := s.sessions.List(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(info))
	for _, item := range info {
		keys = append(keys, item.ID)
	}
	return keys, nil
}

// ListThreads returns thread metadata ordered by most recently updated first.
func (s *AgentStore) ListThreads(ctx context.Context) ([]Info, error) {
	return s.sessions.List(ctx)
}

// GetThread returns the persisted thread transcript and agent metadata.
func (s *AgentStore) GetThread(ctx context.Context, key string) (*ThreadSnapshot, error) {
	mgr, err := s.sessions.Open(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("open agent session: %w", err)
	}
	return buildThreadSnapshot(mgr), nil
}

// GetThreadConfig returns the persisted call config for a thread. The second
// return value reports whether an explicit thread-level config exists.
func (s *AgentStore) GetThreadConfig(ctx context.Context, key string) (*agentcore.CallConfig, bool, error) {
	mgr, err := s.sessions.Open(ctx, key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("open agent session: %w", err)
	}
	cfg, ok := latestThreadConfig(mgr)
	return agentcore.CloneCallConfig(cfg), ok, nil
}

// SetThreadConfig persists a thread-level config override. Passing nil clears
// the effective thread override while recording that reset in history.
func (s *AgentStore) SetThreadConfig(ctx context.Context, key string, cfg *agentcore.CallConfig) (*ThreadSnapshot, error) {
	mgr, err := s.openOrCreate(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := appendThreadConfig(ctx, mgr, cfg); err != nil {
		return nil, err
	}
	if err := mgr.flushAll(); err != nil {
		return nil, err
	}
	return buildThreadSnapshot(mgr), nil
}

// GetThreadThinking returns the persisted thinking config for a thread. The
// second return value reports whether an explicit thread-level config exists.
func (s *AgentStore) GetThreadThinking(ctx context.Context, key string) (*agentcore.ThinkingConfig, bool, error) {
	cfg, ok, err := s.GetThreadConfig(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if !ok || cfg == nil {
		return nil, false, nil
	}
	return agentcore.CloneThinkingConfig(cfg.Thinking), cfg.Thinking != nil, nil
}

// SetThreadThinking persists a thread-level thinking override. Passing nil
// clears the effective thread override while recording that reset in history.
func (s *AgentStore) SetThreadThinking(ctx context.Context, key string, cfg *agentcore.ThinkingConfig) (*ThreadSnapshot, error) {
	return s.SetThreadConfig(ctx, key, &agentcore.CallConfig{
		Thinking: agentcore.CloneThinkingConfig(cfg),
	})
}

// CreateThread creates a new empty thread and returns its initial snapshot.
func (s *AgentStore) CreateThread(ctx context.Context) (*ThreadSnapshot, error) {
	mgr, err := s.sessions.Create(ctx, CreateOptions{Cwd: s.cwd})
	if err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return &ThreadSnapshot{
		Info:       mgr.Info(),
		Status:     agentcore.StatusIdle,
		Messages:   nil,
		Transcript: nil,
	}, nil
}

// BranchThread creates a new thread from an existing thread. If entryID is empty,
// it branches from the current leaf; otherwise it branches from the given entry.
func (s *AgentStore) BranchThread(ctx context.Context, key, entryID string) (*ThreadSnapshot, error) {
	mgr, err := s.sessions.Open(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("open agent session: %w", err)
	}
	if entryID != "" {
		if err := mgr.Branch(entryID); err != nil {
			return nil, fmt.Errorf("branch thread at entry: %w", err)
		}
	}

	newID, err := mgr.CreateBranchedSession(ctx, s.sessions)
	if err != nil {
		return nil, fmt.Errorf("branch thread: %w", err)
	}
	return s.GetThread(ctx, newID)
}

func (s *AgentStore) openOrCreate(ctx context.Context, key string) (*Manager, error) {
	mgr, err := s.sessions.Open(ctx, key)
	if err == nil {
		return mgr, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open session %q: %w", key, err)
	}
	mgr, err = s.sessions.Create(ctx, CreateOptions{ID: key, Cwd: s.cwd})
	if err != nil {
		return nil, fmt.Errorf("create session %q: %w", key, err)
	}
	return mgr, nil
}

func (s *AgentStore) syncMessages(ctx context.Context, mgr *Manager, want []agentcore.Message) (*Manager, error) {
	have := mgr.MessagesOnPath()
	if len(have) > len(want) || !messagesHavePrefix(want, have) {
		return s.rewriteSession(ctx, mgr, want)
	}
	for _, msg := range want[len(have):] {
		if err := mgr.AppendMessage(ctx, msg); err != nil {
			return nil, fmt.Errorf("append session message: %w", err)
		}
	}
	return mgr, nil
}

func (s *AgentStore) rewriteSession(ctx context.Context, prev *Manager, msgs []agentcore.Message) (*Manager, error) {
	header := prev.Header()
	threadCfg, threadCfgSet := latestThreadConfig(prev)

	// Create the new session first (overwrites the old file in place),
	// then purge the stale lock — this avoids the data-loss window that
	// would exist if we deleted before creating.
	mgr, err := s.sessions.Create(ctx, CreateOptions{
		ID:            header.ID,
		Cwd:           header.Cwd,
		ParentSession: header.ParentSession,
	})
	if err != nil {
		return nil, fmt.Errorf("recreate diverged session: %w", err)
	}

	// File already created — safe to purge the stale lock without a delete.
	s.sessions.lockCleanup(header.ID)

	for _, msg := range msgs {
		if err := mgr.AppendMessage(ctx, msg); err != nil {
			return nil, fmt.Errorf("rewrite session message: %w", err)
		}
	}
	if threadCfgSet {
		if err := appendThreadConfig(ctx, mgr, threadCfg); err != nil {
			return nil, err
		}
	}
	return mgr, nil
}

func latestAgentState(mgr *Manager) (agentStateData, bool) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	for i := len(mgr.entries) - 1; i >= 0; i-- {
		entry := mgr.entries[i]
		if entry.Type != EntryCustom {
			continue
		}
		var data agentStateData
		if json.Unmarshal(entry.Data, &data) == nil && data.Kind == agentStateEntryKind {
			return data, true
		}
	}
	return agentStateData{}, false
}

func buildThreadSnapshot(mgr *Manager) *ThreadSnapshot {
	snap := agentStateFromManager(mgr)
	cfg, _ := latestThreadConfig(mgr)
	return &ThreadSnapshot{
		Info:       mgr.Info(),
		Messages:   snap.Messages,
		Transcript: threadMessagesOnPath(mgr),
		Status:     snap.Status,
		Turn:       snap.Turn,
		TotalUsage: snap.TotalUsage,
		Config:     agentcore.CloneCallConfig(cfg),
		Thinking:   agentcore.CloneThinkingConfig(configThinking(cfg)),
	}
}

func agentStateFromManager(mgr *Manager) agentcore.StateSnapshot {
	snap := agentcore.StateSnapshot{
		Messages: mgr.MessagesOnPath(),
	}
	if meta, ok := latestAgentState(mgr); ok {
		snap.Status = meta.Status
		snap.Turn = meta.Turn
		snap.TotalUsage = meta.TotalUsage
	} else if len(snap.Messages) > 0 {
		snap.Status = agentcore.StatusFinished
	} else {
		snap.Status = agentcore.StatusIdle
	}
	return snap
}

func threadMessagesOnPath(mgr *Manager) []ThreadMessage {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	path := mgr.pathToLeaf()
	var transcript []ThreadMessage
	var lastCompaction *CompactionData
	var lastCompactionEntryID string

	for _, entry := range path {
		if entry.Type == EntryCompaction {
			var cd CompactionData
			if json.Unmarshal(entry.Data, &cd) == nil {
				lastCompaction = &cd
				lastCompactionEntryID = entry.ID
			}
		}
	}

	skipUntil := ""
	if lastCompaction != nil {
		skipUntil = lastCompaction.FirstKeptEntryID
		transcript = append(transcript, ThreadMessage{
			EntryID: lastCompactionEntryID,
			Message: agentcore.Message{
				Role:    agentcore.RoleSystem,
				Content: lastCompaction.Summary,
				Type:    agentcore.MessageTypeCompactionSummary,
			},
		})
	}

	skipping := skipUntil != ""
	for _, entry := range path {
		if skipping {
			if entry.ID == skipUntil {
				skipping = false
			} else {
				continue
			}
		}

		switch entry.Type {
		case EntryMessage:
			var msg agentcore.Message
			if json.Unmarshal(entry.Data, &msg) == nil {
				transcript = append(transcript, ThreadMessage{
					EntryID: entry.ID,
					Message: msg,
				})
			}
		case EntryBranchSummary:
			var bs BranchSummaryData
			if json.Unmarshal(entry.Data, &bs) == nil {
				transcript = append(transcript, ThreadMessage{
					EntryID: entry.ID,
					Message: agentcore.Message{
						Role:    agentcore.RoleSystem,
						Content: bs.Summary,
						Type:    agentcore.MessageTypeBranchSummary,
					},
				})
			}
		}
	}

	return transcript
}

func messagesHavePrefix(full, prefix []agentcore.Message) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		if !reflect.DeepEqual(full[i], prefix[i]) {
			return false
		}
	}
	return true
}

func latestThreadConfig(mgr *Manager) (*agentcore.CallConfig, bool) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	for i := len(mgr.entries) - 1; i >= 0; i-- {
		entry := mgr.entries[i]
		if entry.Type != EntryCustom {
			continue
		}
		var cfgData threadConfigData
		if json.Unmarshal(entry.Data, &cfgData) == nil && cfgData.Kind == threadConfigEntryKind {
			return cfgData.Config, true
		}
		var data threadThinkingData
		if json.Unmarshal(entry.Data, &data) == nil && data.Kind == threadThinkingEntryKind {
			return &agentcore.CallConfig{Thinking: agentcore.CloneThinkingConfig(data.Thinking)}, true
		}
	}
	return nil, false
}

func appendThreadConfig(ctx context.Context, mgr *Manager, cfg *agentcore.CallConfig) error {
	meta, err := json.Marshal(threadConfigData{
		Kind:   threadConfigEntryKind,
		Config: agentcore.CloneCallConfig(cfg),
	})
	if err != nil {
		return fmt.Errorf("marshal thread config: %w", err)
	}
	if err := mgr.Append(ctx, Entry{Type: EntryCustom, Data: meta}); err != nil {
		return fmt.Errorf("append thread config: %w", err)
	}
	return nil
}

func configThinking(cfg *agentcore.CallConfig) *agentcore.ThinkingConfig {
	if cfg == nil {
		return nil
	}
	return cfg.Thinking
}

var _ agentcore.Store = (*AgentStore)(nil)
