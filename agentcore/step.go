package agentcore

import "context"

// Step is a unit of work in a workflow or graph.
// All workflow primitives (Pipeline, Parallel, Router, CompiledGraph) implement Step.
type Step interface {
	Run(ctx context.Context, input string) (string, error)
}
