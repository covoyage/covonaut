package agentcore

import "context"

// Step is a unit of work in a workflow or graph.
// All workflow primitives (Pipeline, Parallel, Router, CompiledGraph) implement Step.
type Step interface {
	Run(ctx context.Context, input string) (string, error)
}

// StreamStep is a streaming variant of Step. Nodes implementing this can
// produce output progressively without waiting for all input to arrive,
// enabling pipelined execution between graph layers.
type StreamStep interface {
	RunStream(ctx context.Context, input *StreamReader[string]) (*StreamReader[string], error)
}

// StepToStreamStep adapts a Step to StreamStep by collecting the entire input
// stream, running once, and wrapping the result as a single-element stream.
func StepToStreamStep(s Step) StreamStep {
	return &stepToStreamAdapter{step: s}
}

// StreamStepToStep adapts a StreamStep to Step by feeding the input as a
// single-element stream and collecting all output chunks into one string.
func StreamStepToStep(s StreamStep) Step {
	return &streamToStepAdapter{step: s}
}

type stepToStreamAdapter struct {
	step Step
}

func (a *stepToStreamAdapter) RunStream(ctx context.Context, input *StreamReader[string]) (*StreamReader[string], error) {
	in, err := CollectString(input)
	if err != nil {
		return nil, err
	}
	out, err := a.step.Run(ctx, in)
	if err != nil {
		return nil, err
	}
	return NewStreamFromValue(out), nil
}

type streamToStepAdapter struct {
	step StreamStep
}

func (a *streamToStepAdapter) Run(ctx context.Context, input string) (string, error) {
	in := NewStreamFromValue(input)
	out, err := a.step.RunStream(ctx, in)
	if err != nil {
		return "", err
	}
	return CollectString(out)
}

// AsStreamStep returns the Step as a StreamStep if it implements the interface,
// or wraps it with StepToStreamStep otherwise.
func AsStreamStep(s Step) StreamStep {
	if ss, ok := s.(StreamStep); ok {
		return ss
	}
	return StepToStreamStep(s)
}
