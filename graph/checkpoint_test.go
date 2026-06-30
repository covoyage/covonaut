package graph

import (
	"context"
	"testing"
)

func TestNewMemoryCheckpointStore(t *testing.T) {
	store := NewMemoryCheckpointStore()
	if store == nil {
		t.Fatal("expected non-nil")
	}
}

func TestMemoryCheckpointStore_SaveAndLoad(t *testing.T) {
	store := NewMemoryCheckpointStore()
	ctx := context.Background()

	cp := Checkpoint{
		ID:      "cp1",
		GraphID: "g1",
	}
	if err := store.Save(ctx, cp); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ctx, "cp1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != "cp1" {
		t.Fatalf("id = %q", loaded.ID)
	}
	if loaded.GraphID != "g1" {
		t.Fatalf("graphID = %q", loaded.GraphID)
	}
}

func TestMemoryCheckpointStore_LoadNotFound(t *testing.T) {
	store := NewMemoryCheckpointStore()
	_, err := store.Load(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestMemoryCheckpointStore_List(t *testing.T) {
	store := NewMemoryCheckpointStore()
	ctx := context.Background()

	store.Save(ctx, Checkpoint{ID: "cp1", GraphID: "g1"})
	store.Save(ctx, Checkpoint{ID: "cp2", GraphID: "g1"})

	list, err := store.List(ctx, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list length = %d", len(list))
	}
}

func TestMemoryCheckpointStore_Delete(t *testing.T) {
	store := NewMemoryCheckpointStore()
	ctx := context.Background()

	store.Save(ctx, Checkpoint{ID: "cp1"})
	if err := store.Delete(ctx, "cp1"); err != nil {
		t.Fatal(err)
	}

	_, err := store.Load(ctx, "cp1")
	if err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestNewInterruptableGraph(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", identityStep("a"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	if ig == nil {
		t.Fatal("expected non-nil")
	}
}

func TestInterruptableGraph_SetInterrupt(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", identityStep("a"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	ig.SetInterrupt("a", InterruptConfig{Before: true})
	ig.SetInterrupt("a", InterruptConfig{After: true})

	// Override should work
	ig.SetInterrupt("a", InterruptConfig{Before: false, After: true})
}

func TestInterruptableGraph_Run_NoInterrupt(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	out, ir, err := ig.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if ir != nil {
		t.Fatalf("expected nil interrupt, got %+v", ir)
	}
	if out != "hello" {
		t.Fatalf("output = %q", out)
	}
}

func TestInterruptableGraph_Run_BeforeInterrupt(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	ig.SetInterrupt("a", InterruptConfig{Before: true})

	_, ir, err := ig.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if ir == nil {
		t.Fatal("expected interrupt result")
	}
	if ir.NodeName != "a" || ir.Phase != "before" {
		t.Fatalf("ir = %+v", ir)
	}
}

func TestInterruptableGraph_Run_AfterInterrupt(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	ig.SetInterrupt("a", InterruptConfig{After: true})

	_, ir, err := ig.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if ir == nil {
		t.Fatal("expected interrupt result")
	}
	if ir.NodeName != "a" || ir.Phase != "after" {
		t.Fatalf("ir = %+v", ir)
	}
}

func TestInterruptableGraph_RunAndResume(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	g.AddNode("b", constStep("b", "world"))
	g.AddEdge("a", "b")
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	ig.SetInterrupt("a", InterruptConfig{Before: true})

	_, ir, err := ig.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}

	// Resume after interrupt
	out, ir2, err := ig.Resume(context.Background(), ir.CheckpointID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ir2 != nil {
		t.Fatalf("expected no more interrupts, got %+v", ir2)
	}
	if out != "world" {
		t.Fatalf("output = %q", out)
	}
}

func TestInterruptableGraph_Resume_InvalidCheckpoint(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	_, _, err := ig.Resume(context.Background(), "nonexistent", "")
	if err == nil {
		t.Fatal("expected error for invalid checkpoint")
	}
}

func TestInterruptableGraph_Run_NodeError(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", errStep("a", "fail"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})
	store := NewMemoryCheckpointStore()

	ig := NewInterruptableGraph(cg, store)
	_, _, err := ig.Run(context.Background(), "input")
	if err == nil {
		t.Fatal("expected error")
	}
}
