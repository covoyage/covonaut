package agentcore

import "context"

// ModelTarget identifies one provider/model pair in a failover chain.
type ModelTarget struct {
	Name     string
	Model    string
	Provider Provider
}

// ModelFailoverContext describes the last failed target and any partial
// response accumulated before a streaming failure.
type ModelFailoverContext struct {
	Attempt      int
	LastTarget   ModelTarget
	LastResponse *ProviderResponse
	LastErr      error
}

// ModelFailoverConfig switches provider/model targets after normal retry is
// exhausted. SelectTarget overrides the default ordered Targets selection.
type ModelFailoverConfig struct {
	Targets        []ModelTarget
	MaxAttempts    int
	ShouldFailover func(ctx context.Context, failure ModelFailoverContext) bool
	SelectTarget   func(ctx context.Context, failure ModelFailoverContext) (ModelTarget, error)
}

func (c *ModelFailoverConfig) shouldFailover(ctx context.Context, failure ModelFailoverContext) bool {
	if c == nil || ctx.Err() != nil || IsInterrupt(failure.LastErr) || IsRepetitionLoopError(failure.LastErr) {
		return false
	}
	if c.ShouldFailover != nil {
		return c.ShouldFailover(ctx, failure)
	}
	return IsRetryableError(failure.LastErr)
}

func (c *ModelFailoverConfig) target(ctx context.Context, failure ModelFailoverContext) (ModelTarget, error) {
	if c.SelectTarget != nil {
		return c.SelectTarget(ctx, failure)
	}
	start := 0
	if failure.LastTarget.Name != "" {
		for i, target := range c.Targets {
			if target.Name == failure.LastTarget.Name {
				start = i + 1
				break
			}
		}
	}
	index := start + failure.Attempt - 1
	if index < 0 || index >= len(c.Targets) {
		return ModelTarget{}, nil
	}
	return c.Targets[index], nil
}
