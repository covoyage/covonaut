package agentcore

import (
	"context"
	"fmt"
)

type strictCompactionKey struct{}

const emptyStructuredSummary = "(empty structured summary)"

// CompressForTransition never replaces history with an error notice on summary
// failure. The caller owns the engine and candidate history until publication.
func (e *CompressorEngine) CompressForTransition(ctx context.Context, messages []Message, focus string) ([]Message, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	previousState, previousCount := e.state, e.compressionCnt
	if previousState != nil {
		copied := *previousState
		e.state = &copied
	}
	committed := false
	defer func() {
		if !committed {
			e.state = previousState
			e.compressionCnt = previousCount
		}
	}()
	copies := make([]Message, len(messages))
	for i, message := range messages {
		copies[i] = message.Clone()
	}
	result, cut, err := e.Compress(context.WithValue(ctx, strictCompactionKey{}, true), copies, focus)
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if cut <= 0 || EstimateMessagesTokens(result) >= EstimateMessagesTokens(messages) {
		return nil, 0, fmt.Errorf("strict compaction made no measurable progress")
	}
	committed = true
	return result, cut, nil
}
