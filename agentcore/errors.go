package agentcore

import (
	"context"
	"fmt"
	"strings"
)

// NodeError is a structured error that carries the execution path
// (agent name, turn, node/tool) where the error occurred.
// This makes debugging complex multi-agent workflows significantly easier.
type NodeError struct {
	Path    []string // execution path, e.g. ["coordinator", "turn:3", "tool:get_weather"]
	Message string
	Err     error
}

func (e *NodeError) Error() string {
	path := strings.Join(e.Path, " → ")
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", path, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", path, e.Message)
}

func (e *NodeError) Unwrap() error { return e.Err }

// NewNodeError creates a NodeError with the given path segments.
func NewNodeError(msg string, err error, path ...string) *NodeError {
	return &NodeError{Path: path, Message: msg, Err: err}
}

// WrapNodeError appends a path segment to an existing NodeError,
// or wraps a plain error into a new NodeError.
func WrapNodeError(err error, pathSegment string) error {
	if err == nil {
		return nil
	}
	if ne, ok := err.(*NodeError); ok {
		ne.Path = append([]string{pathSegment}, ne.Path...)
		return ne
	}
	return &NodeError{
		Path:    []string{pathSegment},
		Message: err.Error(),
		Err:     err,
	}
}

// ErrExceedMaxSteps is returned when a graph/workflow exceeds the maximum step count.
var ErrExceedMaxSteps = fmt.Errorf("exceeded maximum execution steps")

// UnknownToolHandler is called when the model requests a tool that doesn't exist.
// It receives the tool call and should return a result string to send back to the model.
// This is the recommended way to handle LLM "hallucinated" tool names gracefully.
type UnknownToolHandler func(ctx context.Context, tc ToolCall) (string, error)

// DefaultUnknownToolHandler returns an error message listing available tools.
func DefaultUnknownToolHandler(availableNames []string) UnknownToolHandler {
	return func(_ context.Context, tc ToolCall) (string, error) {
		return fmt.Sprintf(
			"Error: tool %q does not exist. Available tools: %s",
			tc.Name, strings.Join(availableNames, ", "),
		), nil
	}
}

// DynamicUnknownToolHandler returns an error message listing the registry's
// current tool names, which is useful when tools can be hot-reloaded.
func DynamicUnknownToolHandler(registry interface{ Names() []string }) UnknownToolHandler {
	return func(_ context.Context, tc ToolCall) (string, error) {
		availableNames := registry.Names()
		return fmt.Sprintf(
			"Error: tool %q does not exist. Available tools: %s",
			tc.Name, strings.Join(availableNames, ", "),
		), nil
	}
}
