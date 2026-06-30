package agentcore

import (
	"context"
	"fmt"
)

// Runnable[I, O] is a generic interface for any executable component.
// Inspired by eino's four-mode Runnable, it supports all data flow patterns:
//   - Invoke:    single input  → single output
//   - Stream:    single input  → stream output
//   - Collect:   stream input  → single output
//   - Transform: stream input  → stream output
//
// Components only need to implement the methods they care about.
// Use NewInvokeRunnable / NewStreamRunnable / NewCollectRunnable / NewTransformRunnable
// to create partial implementations — the framework auto-derives the missing modes.
type Runnable[I, O any] interface {
	Invoke(ctx context.Context, input I) (O, error)
	Stream(ctx context.Context, input I) (*StreamReader[O], error)
	Collect(ctx context.Context, input *StreamReader[I]) (O, error)
	Transform(ctx context.Context, input *StreamReader[I]) (*StreamReader[O], error)
}

// ---------------------------------------------------------------------------
// InvokeRunnable: user provides Invoke, others auto-derived
// ---------------------------------------------------------------------------

type InvokeRunnable[I, O any] struct {
	InvokeFn func(ctx context.Context, input I) (O, error)
}

func (r *InvokeRunnable[I, O]) Invoke(ctx context.Context, input I) (O, error) {
	return r.InvokeFn(ctx, input)
}

func (r *InvokeRunnable[I, O]) Stream(ctx context.Context, input I) (*StreamReader[O], error) {
	result, err := r.InvokeFn(ctx, input)
	if err != nil {
		return nil, err
	}
	sr := NewStreamReader[O](1)
	sr.Send(result)
	sr.Close()
	return sr, nil
}

func (r *InvokeRunnable[I, O]) Collect(ctx context.Context, input *StreamReader[I]) (O, error) {
	last, err := collectLast(input)
	if err != nil {
		var zero O
		return zero, err
	}
	return r.InvokeFn(ctx, last)
}

func (r *InvokeRunnable[I, O]) Transform(ctx context.Context, input *StreamReader[I]) (*StreamReader[O], error) {
	last, err := collectLast(input)
	if err != nil {
		return nil, err
	}
	return r.Stream(ctx, last)
}

func NewInvokeRunnable[I, O any](fn func(ctx context.Context, input I) (O, error)) Runnable[I, O] {
	return &InvokeRunnable[I, O]{InvokeFn: fn}
}

// ---------------------------------------------------------------------------
// StreamRunnable: user provides Stream, others auto-derived
// ---------------------------------------------------------------------------

type StreamRunnable[I, O any] struct {
	StreamFn func(ctx context.Context, input I) (*StreamReader[O], error)
}

func (r *StreamRunnable[I, O]) Invoke(ctx context.Context, input I) (O, error) {
	sr, err := r.StreamFn(ctx, input)
	if err != nil {
		var zero O
		return zero, err
	}
	return drainLast(sr)
}

func (r *StreamRunnable[I, O]) Stream(ctx context.Context, input I) (*StreamReader[O], error) {
	return r.StreamFn(ctx, input)
}

func (r *StreamRunnable[I, O]) Collect(ctx context.Context, input *StreamReader[I]) (O, error) {
	last, err := collectLast(input)
	if err != nil {
		var zero O
		return zero, err
	}
	return r.Invoke(ctx, last)
}

func (r *StreamRunnable[I, O]) Transform(ctx context.Context, input *StreamReader[I]) (*StreamReader[O], error) {
	last, err := collectLast(input)
	if err != nil {
		return nil, err
	}
	return r.StreamFn(ctx, last)
}

func NewStreamRunnable[I, O any](fn func(ctx context.Context, input I) (*StreamReader[O], error)) Runnable[I, O] {
	return &StreamRunnable[I, O]{StreamFn: fn}
}

// ---------------------------------------------------------------------------
// CollectRunnable: user provides Collect (stream→single), others auto-derived
// ---------------------------------------------------------------------------

type CollectRunnable[I, O any] struct {
	CollectFn func(ctx context.Context, input *StreamReader[I]) (O, error)
}

func (r *CollectRunnable[I, O]) Invoke(ctx context.Context, input I) (O, error) {
	sr := NewStreamReader[I](1)
	sr.Send(input)
	sr.Close()
	return r.CollectFn(ctx, sr)
}

func (r *CollectRunnable[I, O]) Stream(ctx context.Context, input I) (*StreamReader[O], error) {
	result, err := r.Invoke(ctx, input)
	if err != nil {
		return nil, err
	}
	sr := NewStreamReader[O](1)
	sr.Send(result)
	sr.Close()
	return sr, nil
}

func (r *CollectRunnable[I, O]) Collect(ctx context.Context, input *StreamReader[I]) (O, error) {
	return r.CollectFn(ctx, input)
}

func (r *CollectRunnable[I, O]) Transform(ctx context.Context, input *StreamReader[I]) (*StreamReader[O], error) {
	result, err := r.CollectFn(ctx, input)
	if err != nil {
		return nil, err
	}
	sr := NewStreamReader[O](1)
	sr.Send(result)
	sr.Close()
	return sr, nil
}

func NewCollectRunnable[I, O any](fn func(ctx context.Context, input *StreamReader[I]) (O, error)) Runnable[I, O] {
	return &CollectRunnable[I, O]{CollectFn: fn}
}

// ---------------------------------------------------------------------------
// TransformRunnable: user provides Transform (stream→stream), others auto-derived
// ---------------------------------------------------------------------------

type TransformRunnable[I, O any] struct {
	TransformFn func(ctx context.Context, input *StreamReader[I]) (*StreamReader[O], error)
}

func (r *TransformRunnable[I, O]) Invoke(ctx context.Context, input I) (O, error) {
	sr := NewStreamReader[I](1)
	sr.Send(input)
	sr.Close()
	outStream, err := r.TransformFn(ctx, sr)
	if err != nil {
		var zero O
		return zero, err
	}
	return drainLast(outStream)
}

func (r *TransformRunnable[I, O]) Stream(ctx context.Context, input I) (*StreamReader[O], error) {
	sr := NewStreamReader[I](1)
	sr.Send(input)
	sr.Close()
	return r.TransformFn(ctx, sr)
}

func (r *TransformRunnable[I, O]) Collect(ctx context.Context, input *StreamReader[I]) (O, error) {
	outStream, err := r.TransformFn(ctx, input)
	if err != nil {
		var zero O
		return zero, err
	}
	return drainLast(outStream)
}

func (r *TransformRunnable[I, O]) Transform(ctx context.Context, input *StreamReader[I]) (*StreamReader[O], error) {
	return r.TransformFn(ctx, input)
}

func NewTransformRunnable[I, O any](fn func(ctx context.Context, input *StreamReader[I]) (*StreamReader[O], error)) Runnable[I, O] {
	return &TransformRunnable[I, O]{TransformFn: fn}
}

// ---------------------------------------------------------------------------
// Helpers: drain last element / collect last from stream
// ---------------------------------------------------------------------------

func drainLast[T any](sr *StreamReader[T]) (T, error) {
	defer sr.Close()
	var last T
	var hasValue bool
	for {
		val, ok := sr.Recv()
		if !ok {
			break
		}
		last = val
		hasValue = true
	}
	if !hasValue {
		var zero T
		return zero, sr.Err()
	}
	return last, sr.Err()
}

func collectLast[T any](sr *StreamReader[T]) (T, error) {
	return drainLast(sr)
}

// ---------------------------------------------------------------------------
// Step adapters
// ---------------------------------------------------------------------------

// RunnableStep adapts a Runnable to the Step interface for workflows.
type RunnableStep[I, O any] struct {
	Name       string
	R          Runnable[I, O]
	ToInput    func(string) I
	FromOutput func(O) string
}

func (rs *RunnableStep[I, O]) Run(ctx context.Context, input string) (string, error) {
	in := rs.ToInput(input)
	out, err := rs.R.Invoke(ctx, in)
	if err != nil {
		if rs.Name != "" {
			return "", fmt.Errorf("step %q: %w", rs.Name, err)
		}
		return "", err
	}
	return rs.FromOutput(out), nil
}

type StringRunnable = Runnable[string, string]

func NewStringRunnableStep(r StringRunnable) Step {
	return &RunnableStep[string, string]{
		R:          r,
		ToInput:    func(s string) string { return s },
		FromOutput: func(s string) string { return s },
	}
}
