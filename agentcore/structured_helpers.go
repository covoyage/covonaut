package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
)

// DecodeStructured unmarshals a structured JSON string into T.
func DecodeStructured[T any](raw string) (T, error) {
	var out T
	if err := DecodeStructuredInto(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

// DecodeStructuredInto unmarshals a structured JSON string into dst.
func DecodeStructuredInto(raw string, dst any) error {
	if dst == nil {
		return fmt.Errorf("decode structured: destination is nil")
	}
	if raw == "" {
		return fmt.Errorf("decode structured: empty response")
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return fmt.Errorf("decode structured: %w", err)
	}
	return nil
}

// RunStructured runs the agent and decodes the final output into T.
func RunStructured[T any](ctx context.Context, agent *Agent, input string) (T, error) {
	var out T
	_, err := RunStructuredInto(ctx, agent, input, &out)
	return out, err
}

// RunStructuredInto runs the agent and decodes the final output into dst.
// It returns the raw output string for callers that also want the original JSON.
func RunStructuredInto[T any](ctx context.Context, agent *Agent, input string, dst *T) (string, error) {
	raw, err := agent.Run(ctx, input)
	if err != nil {
		return raw, err
	}
	if err := DecodeStructuredInto(raw, dst); err != nil {
		return raw, err
	}
	return raw, nil
}

// ContinueStructured resumes the agent and decodes the final output into T.
func ContinueStructured[T any](ctx context.Context, agent *Agent) (T, error) {
	var out T
	_, err := ContinueStructuredInto(ctx, agent, &out)
	return out, err
}

// ContinueStructuredInto resumes the agent and decodes the final output into dst.
func ContinueStructuredInto[T any](ctx context.Context, agent *Agent, dst *T) (string, error) {
	raw, err := agent.Continue(ctx)
	if err != nil {
		return raw, err
	}
	if err := DecodeStructuredInto(raw, dst); err != nil {
		return raw, err
	}
	return raw, nil
}
