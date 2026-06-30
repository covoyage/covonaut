package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// Step is a unit of work in a workflow. Re-exported for convenience.
type Step = agentcore.Step

// AgentStep wraps an Agent Config as a workflow step.
type AgentStep struct {
	Config agentcore.Config
}

func NewAgentStep(cfg agentcore.Config) *AgentStep {
	return &AgentStep{Config: cfg}
}

func (s *AgentStep) Run(ctx context.Context, input string) (string, error) {
	return agentcore.New(s.Config).Run(ctx, input)
}

// FuncStep wraps a plain function as a workflow step.
type FuncStep struct {
	Fn func(ctx context.Context, input string) (string, error)
}

func NewFuncStep(fn func(ctx context.Context, input string) (string, error)) *FuncStep {
	return &FuncStep{Fn: fn}
}

func (s *FuncStep) Run(ctx context.Context, input string) (string, error) {
	return s.Fn(ctx, input)
}

// Pipeline runs steps sequentially.
type Pipeline struct {
	Steps []Step
}

func (p *Pipeline) Run(ctx context.Context, input string) (string, error) {
	current := input
	for i, step := range p.Steps {
		output, err := step.Run(ctx, current)
		if err != nil {
			return "", agentcore.WrapNodeError(err, fmt.Sprintf("pipeline[%d]", i))
		}
		current = output
	}
	return current, nil
}

// Parallel runs steps concurrently with the same input and merges the results.
type Parallel struct {
	Steps []Step
	Merge func(results []string) string
}

func (p *Parallel) Run(ctx context.Context, input string) (string, error) {
	results := make([]string, len(p.Steps))
	errs := make([]error, len(p.Steps))
	var wg sync.WaitGroup

	for i, step := range p.Steps {
		wg.Add(1)
		go func(idx int, s Step) {
			defer wg.Done()
			out, err := s.Run(ctx, input)
			results[idx] = out
			errs[idx] = err
		}(i, step)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return "", agentcore.WrapNodeError(err, fmt.Sprintf("parallel[%d]", i))
		}
	}

	if p.Merge != nil {
		return p.Merge(results), nil
	}
	return strings.Join(results, "\n---\n"), nil
}

// Router dynamically picks a step based on the input.
type Router struct {
	Route func(ctx context.Context, input string) string
	Steps map[string]Step
}

func (r *Router) Run(ctx context.Context, input string) (string, error) {
	key := r.Route(ctx, input)
	step, ok := r.Steps[key]
	if !ok {
		return "", agentcore.NewNodeError("no step found", nil, "router", key)
	}
	output, err := step.Run(ctx, input)
	if err != nil {
		return "", agentcore.WrapNodeError(err, "router:"+key)
	}
	return output, nil
}
