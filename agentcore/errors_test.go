package agentcore

import (
	"errors"
	"fmt"
	"testing"
)

func TestNodeError(t *testing.T) {
	err := &NodeError{
		Path:    []string{"coordinator", "turn:3", "tool:get_weather"},
		Message: "api call failed",
		Err:     errors.New("connection refused"),
	}
	got := err.Error()
	want := "[coordinator → turn:3 → tool:get_weather] api call failed: connection refused"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !errors.Is(err, err.Err) {
		t.Fatal("Unwrap should return the inner error")
	}
}

func TestNodeErrorNoCause(t *testing.T) {
	err := &NodeError{
		Path:    []string{"root"},
		Message: "something went wrong",
	}
	got := err.Error()
	want := "[root] something went wrong"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNewNodeError(t *testing.T) {
	cause := errors.New("boom")
	err := NewNodeError("failed", cause, "step1", "step2")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if len(err.Path) != 2 || err.Path[0] != "step1" || err.Path[1] != "step2" {
		t.Fatalf("unexpected path: %v", err.Path)
	}
	if err.Message != "failed" {
		t.Fatalf("unexpected message: %s", err.Message)
	}
	if !errors.Is(err, cause) {
		t.Fatal("should wrap the cause")
	}
}

func TestWrapNodeError(t *testing.T) {
	// Wrapping a plain error
	err := WrapNodeError(errors.New("oops"), "turn:1")
	if err == nil {
		t.Fatal("expected non-nil")
	}
	var ne *NodeError
	if !errors.As(err, &ne) {
		t.Fatal("expected NodeError")
	}
	if len(ne.Path) != 1 || ne.Path[0] != "turn:1" {
		t.Fatalf("unexpected path: %v", ne.Path)
	}

	// Wrapping an existing NodeError
	err2 := WrapNodeError(err, "agent")
	var ne2 *NodeError
	if !errors.As(err2, &ne2) {
		t.Fatal("expected NodeError")
	}
	if len(ne2.Path) != 2 || ne2.Path[0] != "agent" || ne2.Path[1] != "turn:1" {
		t.Fatalf("unexpected path: %v", ne2.Path)
	}
}

func TestWrapNodeErrorNil(t *testing.T) {
	if err := WrapNodeError(nil, "step"); err != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestErrExceedMaxSteps(t *testing.T) {
	if ErrExceedMaxSteps.Error() != "exceeded maximum execution steps" {
		t.Fatalf("unexpected message: %s", ErrExceedMaxSteps.Error())
	}
}

func TestDefaultUnknownToolHandler(t *testing.T) {
	handler := DefaultUnknownToolHandler([]string{"tool_a", "tool_b"})
	result, err := handler(nil, ToolCall{Name: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `Error: tool "nonexistent" does not exist. Available tools: tool_a, tool_b` {
		t.Fatalf("unexpected result: %s", result)
	}
}

type mockRegistry struct{}

func (m *mockRegistry) Names() []string {
	return []string{"alpha", "beta"}
}

func TestDynamicUnknownToolHandler(t *testing.T) {
	handler := DynamicUnknownToolHandler(&mockRegistry{})
	result, err := handler(nil, ToolCall{Name: "gamma"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `Error: tool "gamma" does not exist. Available tools: alpha, beta` {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestNodeErrorFormats(t *testing.T) {
	// Verify it implements the fmt.Formatter interface-ish
	err := &NodeError{Path: []string{"a"}, Message: "msg", Err: fmt.Errorf("err")}
	got := fmt.Sprintf("%v", err)
	if got == "" {
		t.Fatal("expected formatted output")
	}

	// With nil Err
	err2 := &NodeError{Path: []string{"b"}, Message: "msg2"}
	got2 := fmt.Sprintf("%v", err2)
	if got2 == "" {
		t.Fatal("expected formatted output")
	}
}
