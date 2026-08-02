package agentcore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Artifact struct {
	ID        string            `json:"id"`
	Content   []byte            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

type ArtifactStore interface {
	Put(ctx context.Context, artifact Artifact) (Artifact, error)
	Get(ctx context.Context, id string) (Artifact, error)
}

type ArtifactOffloadConfig struct {
	Store        ArtifactStore
	MinBytes     int
	ExcludeTools []string
}

type MemoryArtifactStore struct {
	mu        sync.RWMutex
	artifacts map[string]Artifact
}

func NewMemoryArtifactStore() *MemoryArtifactStore {
	return &MemoryArtifactStore{artifacts: make(map[string]Artifact)}
}

func (s *MemoryArtifactStore) Put(ctx context.Context, artifact Artifact) (Artifact, error) {
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	if artifact.ID == "" {
		artifact.ID = newArtifactID()
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now()
	}
	s.mu.Lock()
	s.artifacts[artifact.ID] = cloneArtifact(artifact)
	s.mu.Unlock()
	return cloneArtifact(artifact), nil
}

func (s *MemoryArtifactStore) Get(ctx context.Context, id string) (Artifact, error) {
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	s.mu.RLock()
	artifact, ok := s.artifacts[id]
	s.mu.RUnlock()
	if !ok {
		return Artifact{}, fmt.Errorf("artifact %q not found", id)
	}
	return cloneArtifact(artifact), nil
}

func cloneArtifact(artifact Artifact) Artifact {
	artifact.Content = append([]byte(nil), artifact.Content...)
	if artifact.Metadata != nil {
		metadata := make(map[string]string, len(artifact.Metadata))
		for key, value := range artifact.Metadata {
			metadata[key] = value
		}
		artifact.Metadata = metadata
	}
	return artifact
}

func newArtifactID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func offloadToolResults(ctx context.Context, cfg *ArtifactOffloadConfig, results []ToolResult) []ToolResult {
	if cfg == nil || cfg.Store == nil {
		return results
	}
	minBytes := cfg.MinBytes
	if minBytes <= 0 {
		minBytes = 50 * 1024
	}
	excluded := make(map[string]bool, len(cfg.ExcludeTools))
	for _, name := range cfg.ExcludeTools {
		excluded[name] = true
	}
	for i := range results {
		result := &results[i]
		content := result.EffectiveResult()
		if result.Err != nil || excluded[result.ToolName] || len(content) < minBytes {
			continue
		}
		artifact, err := cfg.Store.Put(ctx, Artifact{
			Content: []byte(content),
			Metadata: map[string]string{
				"tool_name":    result.ToolName,
				"tool_call_id": result.ToolCallID,
			},
		})
		if err != nil {
			continue
		}
		if result.ForUser == "" {
			result.ForUser = result.Result
		}
		result.Result = fmt.Sprintf("Tool output was offloaded to artifact://%s (%d bytes). Use artifact_read with this ID to retrieve it.", artifact.ID, len(content))
		result.ForLLM = result.Result
	}
	return results
}

func NewArtifactReadTool(store ArtifactStore) *Tool {
	return &Tool{
		Name:        "artifact_read",
		Description: "Read content previously offloaded from a large tool result.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
			"required": []string{"id"},
		},
		Func: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			artifact, err := store.Get(ctx, input.ID)
			if err != nil {
				return nil, err
			}
			return string(artifact.Content), nil
		},
	}
}
