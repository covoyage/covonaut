// Package components defines the core RAG (Retrieval-Augmented Generation)
// interfaces for the covonaut agent framework. Each interface follows the
// project convention of single-method, context-first signatures.
//
// The package is intentionally abstract — concrete implementations live
// outside this repository (or in provider-specific packages), keeping the
// core dependency-free.
package components

import "context"

// Document represents a single document with its content and metadata.
type Document struct {
	ID       string
	Content  string
	Metadata map[string]any
}

// Source identifies a document source. It may be a file path, URL, database
// connection string, or any other implementation-defined identifier.
type Source string

// Loader loads documents from a source.
type Loader interface {
	Load(ctx context.Context, src Source) ([]*Document, error)
}

// Embedder converts text into vector embeddings.
type Embedder interface {
	// Embed generates embeddings for the given texts. The returned slice
	// has the same length as texts, with each element being the embedding
	// vector for the corresponding input.
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// Retriever retrieves relevant documents for a query.
type Retriever interface {
	Retrieve(ctx context.Context, query string) ([]*Document, error)
}

// Indexer persists documents into a searchable index for later retrieval.
type Indexer interface {
	Index(ctx context.Context, docs []*Document) error
}
