package graph

import (
	"context"
	"strings"
	"testing"
)

// ---- NewDAGRunner ----

func TestNewDAGRunner(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	r := NewDAGRunner(cg)
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
	if r.config.Mode != RunModeDAG {
		t.Fatalf("mode = %s", r.config.Mode)
	}
}

func TestNewDAGRunner_WithOptions(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	store := NewMemoryCheckpointStore()
	r := NewDAGRunner(cg, RunnerConfig{
		Store: store,
	})
	if r.config.Store != store {
		t.Fatal("store not set")
	}
}

func TestNewDAGRunner_CustomMode(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	r := NewDAGRunner(cg, RunnerConfig{
		Mode: RunModeDAG,
	})
	if r.config.Mode != RunModeDAG {
		t.Fatalf("mode = %s", r.config.Mode)
	}
}

func TestRunnerConstructorsPinExecutionMode(t *testing.T) {
	dag := NewGraph()
	dag.AddNode("a", constStep("a", "ok"))
	compiledDAG, _ := dag.Compile(CompileOptions{EntryNode: "a"})
	if got := NewDAGRunner(compiledDAG, RunnerConfig{Mode: RunModePregl}).config.Mode; got != RunModeDAG {
		t.Fatalf("DAG runner mode = %q", got)
	}

	pregel := NewPregelGraph()
	pregel.AddNode("a", func(_ context.Context, state PregelState) (PregelState, error) { return state, nil })
	compiledPregel, _ := pregel.Compile("a")
	state := PregelState{}
	if got := NewPregelRunner(compiledPregel, &state, RunnerConfig{Mode: RunModeDAG}).config.Mode; got != RunModePregl {
		t.Fatalf("Pregel runner mode = %q", got)
	}
}

// ---- NewPregelRunner ----

func TestNewPregelRunner(t *testing.T) {
	pg := NewPregelGraph()
	pg.AddNode("a", func(_ context.Context, state PregelState) (PregelState, error) {
		state["output"] = "hello"
		return state, nil
	})
	cpg, _ := pg.Compile("a")
	state := PregelState{}

	r := NewPregelRunner(cpg, &state)
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
	if r.config.Mode != RunModePregl {
		t.Fatalf("mode = %s", r.config.Mode)
	}
}

// ---- Runner Run DAG ----

func TestRunner_DAG_Single(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	r := NewDAGRunner(cg)
	out, err := r.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("output = %q", out)
	}
}

func TestRunner_DAG_Chain(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	g.AddNode("b", &testStep{name: "b", fn: func(_ context.Context, input string) (string, error) {
		return input + " world", nil
	}})
	g.AddEdge("a", "b")
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	r := NewDAGRunner(cg)
	out, err := r.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello world" {
		t.Fatalf("output = %q", out)
	}
}

func TestRunner_DAG_Parallel(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "prefix"))
	g.AddNode("b", &testStep{name: "b", fn: func(_ context.Context, input string) (string, error) {
		return input + " hello", nil
	}})
	g.AddNode("c", &testStep{name: "c", fn: func(_ context.Context, input string) (string, error) {
		return input + " world", nil
	}})
	g.AddEdge("a", "b")
	g.AddEdge("a", "c")
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	r := NewDAGRunner(cg)
	out, err := r.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Fatalf("output = %q", out)
	}
}

func TestRunner_DAG_Error(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", errStep("a", "fail"))
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	r := NewDAGRunner(cg)
	_, err := r.Run(context.Background(), "input")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunner_DAG_MaxSteps(t *testing.T) {
	g := NewGraph()
	g.AddNode("a", constStep("a", "hello"))
	g.AddNode("b", constStep("b", "world"))
	g.AddEdge("a", "b")
	cg, _ := g.Compile(CompileOptions{EntryNode: "a"})

	r := NewDAGRunner(cg, RunnerConfig{MaxSteps: 1})
	_, err := r.Run(context.Background(), "input")
	if err == nil {
		t.Fatal("expected max steps error")
	}
}

// ---- Runner Run Pregel ----

func TestRunner_Pregel_Single(t *testing.T) {
	pg := NewPregelGraph()
	pg.AddNode("a", func(_ context.Context, state PregelState) (PregelState, error) {
		state["output"] = "hello"
		return state, nil
	})
	cpg, _ := pg.Compile("a")
	state := PregelState{}

	r := NewPregelRunner(cpg, &state)
	out, err := r.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("output = %q", out)
	}
	if state.GetString("output") != "hello" {
		t.Fatalf("state output = %q", state.GetString("output"))
	}
}

func TestRunnerPregelExecutesConditionalTarget(t *testing.T) {
	graph := NewPregelGraph()
	if err := graph.AddNode("start", func(_ context.Context, state PregelState) (PregelState, error) {
		state["route"] = "next"
		return state, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode("next", func(_ context.Context, state PregelState) (PregelState, error) {
		state["visited"] = "yes"
		return state, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.SetConditionalEdgeWithTargets("start", []string{"next"}, func(_ context.Context, state PregelState) []string {
		return []string{state.GetString("route")}
	}); err != nil {
		t.Fatal(err)
	}
	compiled, err := graph.Compile("start")
	if err != nil {
		t.Fatal(err)
	}
	state := PregelState{}
	if _, err := NewPregelRunner(compiled, &state).Run(context.Background(), "input"); err != nil {
		t.Fatal(err)
	}
	if state.GetString("visited") != "yes" {
		t.Fatalf("state = %#v", state)
	}
}

func TestRunnerPregelParallelNodesShareOneSuperstepSnapshot(t *testing.T) {
	graph := NewPregelGraph()
	graph.AddNode("start", func(_ context.Context, state PregelState) (PregelState, error) { return state, nil })
	graph.AddNode("left", func(_ context.Context, state PregelState) (PregelState, error) {
		state["left_saw"] = state.GetString("value")
		state["left"] = "done"
		return state, nil
	})
	graph.AddNode("right", func(_ context.Context, state PregelState) (PregelState, error) {
		state["right_saw"] = state.GetString("value")
		state["right"] = "done"
		return state, nil
	})
	graph.AddEdge("start", "left")
	graph.AddEdge("start", "right")
	compiled, _ := graph.Compile("start")
	state := PregelState{"value": "before"}
	if _, err := NewPregelRunner(compiled, &state).Run(context.Background(), "input"); err != nil {
		t.Fatal(err)
	}
	if state.GetString("left_saw") != "before" || state.GetString("right_saw") != "before" || state.GetString("left") != "done" || state.GetString("right") != "done" {
		t.Fatalf("state = %#v", state)
	}
}

func TestNewPregelRunnerInitializesNilState(t *testing.T) {
	graph := NewPregelGraph()
	graph.AddNode("start", func(_ context.Context, state PregelState) (PregelState, error) {
		state["route"] = "next"
		return state, nil
	})
	graph.AddNode("next", func(_ context.Context, state PregelState) (PregelState, error) {
		state["visited"] = "yes"
		return state, nil
	})
	graph.SetConditionalEdgeWithTargets("start", []string{"next"}, func(_ context.Context, state PregelState) []string {
		return []string{state.GetString("route")}
	})
	compiled, _ := graph.Compile("start")
	if _, err := NewPregelRunner(compiled, nil).Run(context.Background(), "input"); err != nil {
		t.Fatal(err)
	}
}

func TestRunner_Pregel_Chain(t *testing.T) {
	pg := NewPregelGraph()
	pg.AddNode("a", func(_ context.Context, state PregelState) (PregelState, error) {
		state["step_a"] = "done"
		return state, nil
	})
	pg.AddNode("b", func(_ context.Context, state PregelState) (PregelState, error) {
		state["step_b"] = state.GetString("step_a") + "_b"
		return state, nil
	})
	pg.AddEdge("a", "b")
	cpg, _ := pg.Compile("a")
	state := PregelState{}

	r := NewPregelRunner(cpg, &state)
	_, err := r.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if state.GetString("step_b") != "done_b" {
		t.Fatalf("step_b = %q", state.GetString("step_b"))
	}
}

func TestRunner_Pregel_EndNode(t *testing.T) {
	pg := NewPregelGraph()
	pg.AddNode("a", func(_ context.Context, state PregelState) (PregelState, error) {
		state["output"] = "done"
		return state, nil
	})
	pg.AddEdge("a", PregelEnd)
	cpg, _ := pg.Compile("a")
	state := PregelState{}

	r := NewPregelRunner(cpg, &state)
	out, err := r.Run(context.Background(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("output = %q", out)
	}
}

func TestRunner_Pregel_Error(t *testing.T) {
	pg := NewPregelGraph()
	pg.AddNode("a", func(_ context.Context, _ PregelState) (PregelState, error) {
		return nil, nil
	})
	cpg, _ := pg.Compile("a")
	state := PregelState{}

	r := NewPregelRunner(cpg, &state)
	_, err := r.Run(context.Background(), "input")
	_ = err
}
