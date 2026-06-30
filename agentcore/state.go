package agentcore

import (
	"encoding/json"
	"sync"
)

type Status string

const (
	StatusIdle     Status = "idle"
	StatusRunning  Status = "running"
	StatusFinished Status = "finished"
	StatusError    Status = "error"
)

// AgentState holds the mutable conversation state across turns.
type AgentState struct {
	mu             sync.RWMutex
	status         Status
	messages       []Message
	turn           int64
	pendingHandoff *PendingHandoff
	totalUsage     TokenUsage
}

func NewState() *AgentState {
	return &AgentState{status: StatusIdle}
}

func (s *AgentState) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *AgentState) SetStatus(st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
}

func (s *AgentState) Messages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]Message, len(s.messages))
	for i, m := range s.messages {
		cp[i] = m.Clone()
	}
	return cp
}

func (s *AgentState) AddMessage(m Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ID != "" {
		for i := range s.messages {
			if s.messages[i].ID == m.ID {
				s.messages[i] = m
				return
			}
		}
	}
	s.messages = append(s.messages, m)
}

// HasSystemPrompt returns true if the conversation history already contains a
// system prompt message. Used by Agent.Run to avoid appending duplicate system
// prompts when reusing an agent across multiple calls.
func (s *AgentState) HasSystemPrompt() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages {
		if m.Role == RoleSystem {
			return true
		}
	}
	return false
}

// ReplaceMessages atomically replaces the entire message history.
// Used by compaction to swap old messages with a summary.
func (s *AgentState) ReplaceMessages(msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = msgs
}

func (s *AgentState) Turn() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.turn
}

func (s *AgentState) NextTurn() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turn++
	return s.turn
}

func (s *AgentState) SetPendingHandoff(h *PendingHandoff) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingHandoff = h
}

func (s *AgentState) PendingHandoff() *PendingHandoff {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pendingHandoff
}

func (s *AgentState) ClearPendingHandoff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingHandoff = nil
}

// AddUsage accumulates token usage across turns.
func (s *AgentState) AddUsage(usage TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalUsage.PromptTokens += usage.PromptTokens
	s.totalUsage.CompletionTokens += usage.CompletionTokens
	s.totalUsage.TotalTokens += usage.TotalTokens
}

// TotalUsage returns the accumulated token usage across all turns.
func (s *AgentState) TotalUsage() TokenUsage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalUsage
}

// Snapshot serializes the current state for persistence / resume.
type StateSnapshot struct {
	Status     Status     `json:"status"`
	Messages   []Message  `json:"messages"`
	Turn       int64      `json:"turn"`
	TotalUsage TokenUsage `json:"total_usage"`
}

func (s *AgentState) Snapshot() StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := make([]Message, len(s.messages))
	for i, m := range s.messages {
		msgs[i] = m.Clone()
	}
	return StateSnapshot{
		Status:     s.status,
		Messages:   msgs,
		Turn:       s.turn,
		TotalUsage: s.totalUsage,
	}
}

func (s *AgentState) Restore(snap StateSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = snap.Status
	s.messages = snap.Messages
	s.turn = snap.Turn
	s.totalUsage = snap.TotalUsage
}

func (s *AgentState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Snapshot())
}

func (s *AgentState) UnmarshalJSON(data []byte) error {
	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	s.Restore(snap)
	return nil
}
