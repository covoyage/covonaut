package agentcore

import (
	"context"
	"fmt"
	"sync"
)

// CheckpointSaver persists StateSnapshot sequences for a logical thread
// (conversation). Implementations must be safe for concurrent use if agents
// share a saver.
type CheckpointSaver interface {
	// Append stores a snapshot for threadID and returns a monotonically increasing
	// sequence number for that thread.
	Append(ctx context.Context, threadID string, snap StateSnapshot) (seq int64, err error)
	// Latest returns the most recent snapshot and its sequence number.
	Latest(ctx context.Context, threadID string) (snap StateSnapshot, seq int64, err error)
}

// CheckpointSettings configures automatic checkpointing during Run/Continue.
// When Saver is non-nil, by default a checkpoint is written after each completed
// turn (after model + optional tool persistence). Set SkipSaveOnTurnEnd to rely
// only on SaveOnTurnStart and/or Agent.SaveCheckpoint.
type CheckpointSettings struct {
	Saver CheckpointSaver
	// ThreadID logical conversation key; empty defaults to "default" at save time.
	ThreadID string
	// SkipSaveOnTurnEnd disables automatic append after each turn end.
	SkipSaveOnTurnEnd bool
	// SaveOnTurnStart appends a snapshot before each LLM call (after steering/compaction).
	SaveOnTurnStart bool
}

// MemoryCheckpointSaver is an in-memory CheckpointSaver for tests and single-process resume.
// To prevent unbounded memory growth, set MaxCheckpointsPerThread to limit how many
// checkpoints are retained per thread (oldest are evicted). Zero means unlimited.
type MemoryCheckpointSaver struct {
	mu                     sync.Mutex
	nextSeq                int64
	byThread               map[string][]memoryCP
	MaxCheckpointsPerThread int
}

type memoryCP struct {
	Seq  int64
	Snap StateSnapshot
}

// NewMemoryCheckpointSaver creates an empty in-memory saver.
func NewMemoryCheckpointSaver() *MemoryCheckpointSaver {
	return &MemoryCheckpointSaver{byThread: make(map[string][]memoryCP)}
}

func (m *MemoryCheckpointSaver) Append(ctx context.Context, threadID string, snap StateSnapshot) (int64, error) {
	if threadID == "" {
		threadID = "default"
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSeq++
	seq := m.nextSeq
	sl := m.byThread[threadID]
	sl = append(sl, memoryCP{Seq: seq, Snap: snap})

	// Evict oldest checkpoints if limit is set.
	if m.MaxCheckpointsPerThread > 0 && len(sl) > m.MaxCheckpointsPerThread {
		sl = sl[len(sl)-m.MaxCheckpointsPerThread:]
	}

	m.byThread[threadID] = sl
	return seq, nil
}

func (m *MemoryCheckpointSaver) Latest(ctx context.Context, threadID string) (StateSnapshot, int64, error) {
	if threadID == "" {
		threadID = "default"
	}
	select {
	case <-ctx.Done():
		return StateSnapshot{}, 0, ctx.Err()
	default:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sl := m.byThread[threadID]
	if len(sl) == 0 {
		return StateSnapshot{}, 0, fmt.Errorf("checkpoint: no snapshots for thread %q", threadID)
	}
	last := sl[len(sl)-1]
	return last.Snap, last.Seq, nil
}

// All returns every checkpoint for threadID (oldest first). For debugging/tests.
func (m *MemoryCheckpointSaver) All(threadID string) []StateSnapshot {
	if threadID == "" {
		threadID = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sl := m.byThread[threadID]
	out := make([]StateSnapshot, len(sl))
	for i, e := range sl {
		out[i] = e.Snap
	}
	return out
}
