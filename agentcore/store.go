package agentcore

import "context"

// Store persists and retrieves agent state snapshots.
// Implementations: store.FileStore (file-based).
type Store interface {
	Save(ctx context.Context, key string, snap StateSnapshot) error
	Load(ctx context.Context, key string) (StateSnapshot, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context) ([]string, error)
	// Has returns true if a state snapshot exists for the given key.
	Has(ctx context.Context, key string) (bool, error)
}
