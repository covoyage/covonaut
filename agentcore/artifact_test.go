package agentcore

import (
	"context"
	"strings"
	"testing"
)

func TestOffloadToolResultsPreservesRetrievableContent(t *testing.T) {
	store := NewMemoryArtifactStore()
	original := strings.Repeat("output", 20)
	results := offloadToolResults(context.Background(), &ArtifactOffloadConfig{
		Store:    store,
		MinBytes: 10,
	}, []ToolResult{{ToolCallID: "call-1", ToolName: "bash", Result: original}})

	if len(results) != 1 || !strings.Contains(results[0].Result, "artifact://") {
		t.Fatalf("result = %#v", results)
	}
	id := strings.SplitN(strings.SplitN(results[0].Result, "artifact://", 2)[1], " ", 2)[0]
	artifact, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Content) != original {
		t.Fatalf("artifact content = %q", artifact.Content)
	}
	if results[0].ForUser != original {
		t.Fatal("user output was not preserved")
	}
}

func TestNewRegistersArtifactReadTool(t *testing.T) {
	agent := New(StubConfig(&failoverStubProvider{}, WithArtifactOffload(&ArtifactOffloadConfig{
		Store:    NewMemoryArtifactStore(),
		MinBytes: 10,
	})))
	if _, ok := agent.registry.Get("artifact_read"); !ok {
		t.Fatal("artifact_read was not registered")
	}
}

func TestMemoryArtifactStoreDefensivelyCopiesMetadata(t *testing.T) {
	store := NewMemoryArtifactStore()
	original := Artifact{Content: []byte("content"), Metadata: map[string]string{"key": "value"}}
	stored, err := store.Put(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	original.Content[0] = 'X'
	original.Metadata["key"] = "changed"
	stored.Metadata["key"] = "also changed"
	got, err := store.Get(context.Background(), stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Content) != "content" || got.Metadata["key"] != "value" {
		t.Fatalf("artifact = %#v", got)
	}
}
