package components

import (
	"context"
	"testing"
)

type mockLoader struct {
	docs []*Document
	err  error
}

func (m *mockLoader) Load(ctx context.Context, src Source) ([]*Document, error) {
	return m.docs, m.err
}

type mockEmbedder struct {
	vecs [][]float64
	err  error
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	return m.vecs, m.err
}

type mockRetriever struct {
	docs []*Document
	err  error
}

func (m *mockRetriever) Retrieve(ctx context.Context, query string) ([]*Document, error) {
	return m.docs, m.err
}

type mockIndexer struct {
	err error
}

func (m *mockIndexer) Index(ctx context.Context, docs []*Document) error {
	return m.err
}

func TestDocumentInit(t *testing.T) {
	d := &Document{
		ID:      "1",
		Content: "hello",
		Metadata: map[string]any{
			"source": "test",
		},
	}
	if d.ID != "1" || d.Content != "hello" {
		t.Fatal("unexpected document fields")
	}
	if d.Metadata["source"] != "test" {
		t.Fatal("unexpected metadata")
	}
}

func TestLoaderInterface(t *testing.T) {
	m := &mockLoader{docs: []*Document{{ID: "1", Content: "test"}}}
	docs, err := m.Load(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "1" {
		t.Fatal("unexpected loader result")
	}
}

func TestEmbedderInterface(t *testing.T) {
	m := &mockEmbedder{vecs: [][]float64{{0.1, 0.2}}}
	vecs, err := m.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 2 || vecs[0][0] != 0.1 {
		t.Fatal("unexpected embedder result")
	}
}

func TestRetrieverInterface(t *testing.T) {
	m := &mockRetriever{docs: []*Document{{ID: "1", Content: "result"}}}
	docs, err := m.Retrieve(context.Background(), "query")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "1" {
		t.Fatal("unexpected retriever result")
	}
}

func TestIndexerInterface(t *testing.T) {
	m := &mockIndexer{}
	err := m.Index(context.Background(), []*Document{{ID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
}
