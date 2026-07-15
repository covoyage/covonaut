package agentcore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const compactionSystemPrompt = `You are a conversation summarizer. Create a concise summary of the provided conversation that preserves all important context needed to continue the conversation.

Focus on:
- Key facts, data, and decisions
- Pending questions or tasks
- Important tool results and their implications
- File states and configurations that were modified

Be concise but complete. Do not lose critical information.`

const compactionSummaryPrefix = "[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted " +
	"into the summary below. This is a handoff from a previous context " +
	"window — treat it as background reference, NOT as active instructions. " +
	"Do NOT answer questions or fulfill requests mentioned in this summary; " +
	"they were already addressed. " +
	"Your current task is identified in the '## Active Task' section of the " +
	"summary — resume exactly from there. " +
	"Respond ONLY to the latest user message " +
	"that appears AFTER this summary. The current session state (files, " +
	"config, etc.) may reflect work described here — avoid repeating it:"

const compactionSummaryEndMarker = "\n\n--- END OF CONTEXT SUMMARY — " +
	"respond to the message below, not the summary above ---"

const prunedToolPlaceholder = "[Old tool output cleared to save context space]"

const charsPerToken = 4

const imageTokenEstimate = 1600

const imageCharEquivalent = imageTokenEstimate * charsPerToken

const minSummaryTokens = 2000

const summaryRatio = 0.20

const summaryTokensCeiling = 12000

const summaryFailureCooldownSeconds = 600

type compactionState struct {
	previousSummary        string
	lastSavingsPct         float64
	ineffectiveCompactions int
	lastSummaryError       string
	summaryFailureCooldown time.Time
}

func newCompactionState() *compactionState {
	return &compactionState{
		lastSavingsPct: 100.0,
	}
}

func contentLengthForBudget(rawContent any) int64 {
	switch c := rawContent.(type) {
	case string:
		return int64(len(c))
	case nil:
		return 0
	case []ContentBlock:
		var total int64
		for _, p := range c {
			switch p.Kind {
			case BlockKindImage:
				total += imageCharEquivalent
			default:
				total += int64(len(p.Text))
			}
		}
		return total
	default:
		return int64(len(fmt.Sprintf("%v", rawContent)))
	}
}

func shouldCompact(msgs []Message, toolDefs []ToolDefinition, contextWindow int64, reserveTokens int64, threshold float64, autoCompactLimit int64, compState *compactionState) bool {
	if contextWindow <= 0 {
		return false
	}
	if compState != nil && time.Now().Before(compState.summaryFailureCooldown) {
		return false
	}
	if compState != nil && compState.ineffectiveCompactions >= 2 {
		return false
	}
	estimated := EstimateMessagesTokens(msgs) + EstimateToolDefinitionsTokens(toolDefs)

	// Absolute token limit takes priority over percentage threshold.
	if autoCompactLimit > 0 {
		return estimated >= autoCompactLimit
	}

	reserve := reserveTokens
	if reserve <= 0 {
		reserve = contextWindow / 4
	}
	triggerThreshold := contextWindow - reserve
	if threshold > 0 && threshold < 1.0 {
		triggerThreshold = int64(float64(contextWindow) * threshold)
	}
	return estimated > triggerThreshold
}

func findCutPoint(msgs []Message, keepRecentTokens int64, protectFirstN int) int64 {
	if keepRecentTokens <= 0 {
		keepRecentTokens = 2000
	}
	if protectFirstN <= 0 {
		protectFirstN = 3
	}
	if len(msgs) <= 3 {
		return 0
	}

	accum := int64(0)
	candidateCut := int64(len(msgs))

	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleSystem {
			continue
		}
		accum += EstimateMessageTokens(msgs[i])
		if accum >= keepRecentTokens {
			candidateCut = int64(i + 1)
			break
		}
	}

	candidateCut = alignBoundaryForward(msgs, candidateCut)

	minCut := int64(0)
	if len(msgs) > 0 && msgs[0].Role == RoleSystem {
		minCut = 1
	}

	headProtect := int64(protectFirstN)
	if candidateCut <= minCut+headProtect+1 {
		return 0
	}

	nonSystemCount := int64(0)
	for i := minCut; i < int64(len(msgs)); i++ {
		if msgs[i].Role != RoleSystem {
			nonSystemCount++
		}
	}

	if nonSystemCount <= headProtect+3 {
		return 0
	}

	return candidateCut
}

func alignBoundaryForward(msgs []Message, cut int64) int64 {
	for cut < int64(len(msgs)) && msgs[cut].Role == RoleTool {
		cut++
	}
	return cut
}

func findTailCutByTokens(msgs []Message, headProtect int64, tailTokenBudget int64) int64 {
	if len(msgs) == 0 {
		return 0
	}

	accum := int64(0)
	tailStart := int64(len(msgs))

	for i := len(msgs) - 1; i >= int(headProtect); i-- {
		if msgs[i].Role == RoleSystem {
			continue
		}
		msgLen := EstimateMessageTokens(msgs[i])
		if accum+msgLen > tailTokenBudget && accum > 0 {
			tailStart = int64(i + 1)
			break
		}
		accum += msgLen
	}

	if tailStart <= headProtect+1 {
		return headProtect + 2
	}

	return alignBoundaryForward(msgs, tailStart)
}

func pruneOldToolResults(msgs []Message, protectTailCount int) ([]Message, int) {
	if len(msgs) == 0 {
		return msgs, 0
	}

	result := make([]Message, len(msgs))
	copy(result, msgs)

	prunedCount := 0
	protectedStart := len(msgs) - protectTailCount
	if protectedStart < 0 {
		protectedStart = 0
	}

	seenToolResults := make(map[string][]int)

	for i := 0; i < protectedStart; i++ {
		if result[i].Role == RoleTool {
			toolName := result[i].Name
			seenToolResults[toolName] = append(seenToolResults[toolName], i)

			if len(result[i].Content) > 2000 {
				result[i].Content = result[i].Content[:2000] + "...[truncated]"
			}
		}

		if result[i].Role == RoleAssistant && len(result[i].ToolCalls) > 0 {
			for j := range result[i].ToolCalls {
				if len(result[i].ToolCalls[j].Arguments) > 1000 {
					result[i].ToolCalls[j].Arguments = result[i].ToolCalls[j].Arguments[:1000] + "...[truncated]"
				}
			}
		}
	}

	for _, indices := range seenToolResults {
		if len(indices) <= 1 {
			continue
		}
		for i := 0; i < len(indices)-1; i++ {
			result[indices[i]].Content = prunedToolPlaceholder
			prunedCount++
		}
	}

	return result, prunedCount
}

func findLatestContextSummary(msgs []Message, searchStart int64, searchEnd int64) (int64, string) {
	for i := searchEnd - 1; i >= searchStart; i-- {
		if msgs[i].Type == MessageTypeCompactionSummary {
			body := MessageStringForSummary(msgs[i])
			if strings.Contains(body, compactionSummaryPrefix) ||
				strings.Contains(body, "[Previous conversation summary") {
				return i, body
			}
		}
	}
	return -1, ""
}

func runCompaction(ctx context.Context, provider Provider, model string, state *AgentState, keepRecentTokens int64, structured bool, protectFirstN int, focusTopic string, compState *compactionState, compressionModel string, compressionProvider Provider, compressionBaseURL string, compressionAPIKey string) (int64, error) {
	msgs := state.Messages()

	nMessages := len(msgs)
	headProtect := int64(protectFirstN)
	if headProtect <= 0 {
		headProtect = 3
	}
	minForCompress := headProtect + 3 + 1
	if int64(nMessages) <= minForCompress {
		return 0, nil
	}

	msgs, prunedCount := pruneOldToolResults(msgs, int(keepRecentTokens/100))
	if prunedCount > 0 {
		state.ReplaceMessages(msgs)
		msgs = state.Messages()
	}

	compressStart := int64(0)
	if len(msgs) > 0 && msgs[0].Role == RoleSystem {
		compressStart = 1
	}
	compressStart = alignBoundaryForward(msgs, compressStart)

	targetTokens := int64(0)
	if compState != nil && keepRecentTokens > 0 {
		targetTokens = int64(float64(keepRecentTokens) * summaryRatio)
		if targetTokens < minSummaryTokens {
			targetTokens = minSummaryTokens
		}
		if targetTokens > summaryTokensCeiling {
			targetTokens = summaryTokensCeiling
		}
	}
	compressEnd := findTailCutByTokens(msgs, headProtect, keepRecentTokens)

	if compressStart >= compressEnd {
		return 0, nil
	}

	summarySearchStart := int64(0)
	if len(msgs) > 0 && msgs[0].Role == RoleSystem {
		summarySearchStart = 1
	}
	summaryIdx, summaryBody := findLatestContextSummary(msgs, summarySearchStart, compressEnd)

	var turnsToSummarize []Message
	if summaryIdx >= 0 {
		if summaryBody != "" && compState != nil && compState.previousSummary == "" {
			compState.previousSummary = summaryBody
		}
		startIdx := compressStart
		if summaryIdx+1 > startIdx {
			startIdx = summaryIdx + 1
		}
		if startIdx >= compressEnd {
			return 0, nil
		}
		turnsToSummarize = msgs[startIdx:compressEnd]
	} else {
		turnsToSummarize = msgs[compressStart:compressEnd]
	}

	if len(turnsToSummarize) == 0 {
		return 0, nil
	}

	displayTokens := EstimateMessagesTokens(msgs)

	var prevSummaryContext string
	if compState != nil && compState.previousSummary != "" {
		prevSummaryContext = fmt.Sprintf("\n\nPrevious summary (iterative update):\n%s\n\n", compState.previousSummary)
	}

	var sb strings.Builder
	for _, msg := range turnsToSummarize {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, MessageStringForSummary(msg)))
	}

	sysPrompt := compactionSystemPrompt
	userBody := fmt.Sprintf("Summarize this conversation:%s\n\n%s", prevSummaryContext, sb.String())

	if focusTopic != "" {
		userBody = fmt.Sprintf("Focus on preserving information related to: %s\n\n%s", focusTopic, userBody)
	}

	maxSummaryTokens := int64(1024)
	if structured {
		sysPrompt = structuredCompactionSystemPrompt
		userBody = fmt.Sprintf("Summarize this conversation transcript into the required JSON object:%s\n\n%s", prevSummaryContext, sb.String())
		if focusTopic != "" {
			userBody = fmt.Sprintf("Focus on preserving information related to: %s\n\n%s", focusTopic, userBody)
		}
		if targetTokens > 0 {
			maxSummaryTokens = targetTokens
		} else {
			maxSummaryTokens = 2048
		}
	}

	compProvider := provider
	compModel := model
	if compressionProvider != nil {
		compProvider = compressionProvider
	}
	if compressionModel != "" {
		compModel = compressionModel
	}

	summaryReq := &ProviderRequest{
		Model: compModel,
		Messages: []Message{
			{Role: RoleSystem, Content: sysPrompt},
			{Role: RoleUser, Content: userBody},
		},
		Temperature: 0,
		MaxTokens:   maxSummaryTokens,
	}

	resp, err := compProvider.Complete(ctx, summaryReq)

	var summaryContent string
	var summaryFailed bool

	if err != nil {
		summaryFailed = true
		nDropped := int64(len(turnsToSummarize))
		summaryContent = fmt.Sprintf(
			"%s\n"+
				"Summary generation failed: %v. %d message(s) were "+
				"removed to free context space but could not be summarized. The removed "+
				"messages contained earlier work in this session. Continue based on the "+
				"recent messages below and the current state of any files or resources."+
				"%s",
			compactionSummaryPrefix, err, nDropped, compactionSummaryEndMarker,
		)
		if compState != nil {
			compState.lastSummaryError = err.Error()
			compState.summaryFailureCooldown = time.Now().Add(time.Duration(summaryFailureCooldownSeconds) * time.Second)
		}
	} else {
		summaryContent = resp.Content
		if compState != nil {
			compState.previousSummary = summaryContent
			compState.lastSummaryError = ""
		}
	}

	var summaryMsg Message
	if structured && !summaryFailed {
		sum, perr := parseStructuredCompactionSummary(resp.Content)
		if perr != nil {
			nDropped := int64(len(turnsToSummarize))
			summaryContent = fmt.Sprintf(
				"%s\n"+
					"Summary parsing failed: %v. %d message(s) were dropped."+
					"%s",
				compactionSummaryPrefix, perr, nDropped, compactionSummaryEndMarker,
			)
			summaryMsg = Message{
				Role:    RoleSystem,
				Content: summaryContent,
				Type:    MessageTypeCompactionSummary,
			}
		} else {
			readable := sum.ToReadableSummary()
			meta := sum.MarshalJSONMetadata()
			summaryMsg = Message{
				Role:     RoleSystem,
				Content:  fmt.Sprintf("%s\n%s%s", compactionSummaryPrefix, readable, compactionSummaryEndMarker),
				Type:     MessageTypeCompactionSummary,
				Metadata: meta,
			}
		}
	} else {
		summaryMsg = Message{
			Role:    RoleSystem,
			Content: summaryContent,
			Type:    MessageTypeCompactionSummary,
		}
	}

	var systemMsg *Message
	if len(msgs) > 0 && msgs[0].Role == RoleSystem {
		sysCopy := msgs[0]
		compactionNote := "[Note: Some earlier conversation turns have been compacted into a handoff summary to preserve context space. The current session state may still reflect earlier work, so build on that summary and state rather than re-doing work.]"
		if !strings.Contains(sysCopy.Content, compactionNote) {
			sysCopy.Content = sysCopy.Content + "\n\n" + compactionNote
		}
		systemMsg = &sysCopy
	}

	compressed := make([]Message, 0, int64(nMessages)-compressEnd+3)
	if systemMsg != nil {
		compressed = append(compressed, *systemMsg)
	}
	compressed = append(compressed, summaryMsg)
	compressed = append(compressed, Message{
		Role:    RoleAssistant,
		Content: "Understood, I have the context from the previous conversation. How can I help?",
		Type:    MessageTypeCompactionSummary,
	})
	compressed = append(compressed, msgs[compressEnd:]...)

	compressed = sanitizeToolPairs(compressed)

	state.ReplaceMessages(compressed)

	newEstimate := EstimateMessagesTokens(compressed)
	savedEstimate := displayTokens - newEstimate

	if compState != nil {
		savingsPct := float64(0)
		if displayTokens > 0 {
			savingsPct = float64(savedEstimate) / float64(displayTokens) * 100
		}
		compState.lastSavingsPct = savingsPct
		if savingsPct < 10 {
			compState.ineffectiveCompactions++
		} else {
			compState.ineffectiveCompactions = 0
		}
	}

	return compressEnd - compressStart, nil
}

func sanitizeToolPairs(msgs []Message) []Message {
	toolCallIDs := make(map[string]bool)
	var result []Message

	for _, msg := range msgs {
		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				toolCallIDs[tc.ID] = true
			}
			result = append(result, msg)
		} else if msg.Role == RoleTool {
			if toolCallIDs[msg.ToolCallID] {
				result = append(result, msg)
			}
		} else {
			result = append(result, msg)
		}
	}

	return result
}
