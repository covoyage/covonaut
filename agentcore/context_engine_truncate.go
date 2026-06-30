package agentcore

import (
	"context"
)

// TruncateEngine is a simple context engine that drops old messages
// without LLM summarization. Useful for testing or when you want
// fast context management without the cost of a summary LLM call.
//
// It preserves:
//   - System message
//   - First N messages (ProtectFirstN)
//   - Last M tokens (KeepRecentTokens)
//
// Everything in the middle is dropped.
type TruncateEngine struct {
	contextLength    int64
	thresholdTokens  int64
	thresholdPercent float64
	protectFirstN    int
	keepRecentTokens int64
	compressionCnt   int64
}

// NewTruncateEngine creates a truncate-only context engine.
func NewTruncateEngine(cfg ContextEngineConfig) ContextEngine {
	return &TruncateEngine{
		contextLength:    cfg.ContextWindow,
		thresholdPercent: cfg.CompressionThreshold,
		protectFirstN:    cfg.ProtectFirstN,
		keepRecentTokens: cfg.KeepRecentTokens,
	}
}

func (e *TruncateEngine) Name() string {
	return "truncate"
}

func (e *TruncateEngine) OnSessionStart(ctx context.Context, model string, contextLength int64) {
	e.contextLength = contextLength
	if e.thresholdPercent > 0 {
		e.thresholdTokens = int64(float64(contextLength) * e.thresholdPercent)
	}
}

func (e *TruncateEngine) OnSessionReset() {
	e.compressionCnt = 0
}

func (e *TruncateEngine) OnSessionEnd() {}

func (e *TruncateEngine) UpdateFromResponse(usage TokenUsage) {}

func (e *TruncateEngine) ShouldCompact(msgs []Message, toolDefs []ToolDefinition, contextWindow int64) bool {
	if contextWindow <= 0 {
		return false
	}
	reserve := contextWindow / 4
	estimated := EstimateMessagesTokens(msgs) + EstimateToolDefinitionsTokens(toolDefs)
	return estimated > contextWindow-reserve
}

func (e *TruncateEngine) Compress(ctx context.Context, msgs []Message, focusTopic string) ([]Message, int64, error) {
	if len(msgs) <= 3 {
		return msgs, 0, nil
	}

	headProtect := int64(e.protectFirstN)
	if headProtect <= 0 {
		headProtect = 3
	}

	// Find head boundary
	headEnd := int64(0)
	if len(msgs) > 0 && msgs[0].Role == RoleSystem {
		headEnd = 1
	}
	nonSystemCount := int64(0)
	for i := headEnd; i < int64(len(msgs)); i++ {
		if msgs[i].Role != RoleSystem {
			nonSystemCount++
			if nonSystemCount >= headProtect {
				headEnd = i + 1
				break
			}
		}
	}

	// Find tail boundary by token budget
	tailStart := int64(len(msgs))
	accum := int64(0)
	for i := len(msgs) - 1; i >= int(headEnd); i-- {
		if msgs[i].Role == RoleSystem {
			continue
		}
		msgLen := EstimateMessageTokens(msgs[i])
		if accum+msgLen > e.keepRecentTokens && accum > 0 {
			tailStart = int64(i + 1)
			break
		}
		accum += msgLen
	}
	tailStart = alignBoundaryForward(msgs, tailStart)

	if headEnd >= tailStart {
		return msgs, 0, nil
	}

	// Build truncated message list: head + tail
	result := make([]Message, 0, headEnd+int64(len(msgs))-tailStart+1)
	result = append(result, msgs[:headEnd]...)
	result = append(result, Message{
		Role:    RoleSystem,
		Content: "[CONTEXT TRUNCATION] Earlier messages were dropped to free context space. Continue based on the messages below.",
		Type:    MessageTypeCompactionSummary,
	})
	result = append(result, msgs[tailStart:]...)

	result = sanitizeToolPairs(result)

	e.compressionCnt++

	return result, tailStart - headEnd, nil
}

func (e *TruncateEngine) GetToolSchemas() []ToolDefinition {
	return nil
}

func (e *TruncateEngine) ContextLength() int64 {
	return e.contextLength
}

func (e *TruncateEngine) ThresholdTokens() int64 {
	return e.thresholdTokens
}

func (e *TruncateEngine) CompressionCount() int64 {
	return e.compressionCnt
}

func (e *TruncateEngine) LastSavingsPct() float64 {
	return 0
}

func (e *TruncateEngine) CheckFeasibility(mainModelContextLength int64) string {
	return ""
}
