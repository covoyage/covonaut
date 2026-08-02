package agentcore

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// ToolSelectionContext describes the tools and conversation available when
// choosing which definitions to expose to the model for one request.
type ToolSelectionContext struct {
	Messages []Message
	Tools    []ToolDefinition
	Limit    int
}

// ToolSelector chooses tool names to expose to the model. The executor keeps
// the complete registry, so selected tools remain executable by name.
type ToolSelector interface {
	SelectTools(ctx context.Context, selection ToolSelectionContext) ([]string, error)
}

type ToolSelectorFunc func(ctx context.Context, selection ToolSelectionContext) ([]string, error)

func (f ToolSelectorFunc) SelectTools(ctx context.Context, selection ToolSelectionContext) ([]string, error) {
	return f(ctx, selection)
}

// ToolSelectionConfig enables per-request tool visibility filtering.
type ToolSelectionConfig struct {
	Selector      ToolSelector
	MaxVisible    int
	AlwaysVisible []string
}

// KeywordToolSelector ranks tools by overlap with recent user messages. It
// supports Unicode words and CJK character n-grams without external packages.
type KeywordToolSelector struct {
	RecentUserMessages int
	IncludeUnmatched   bool
}

func (selector KeywordToolSelector) SelectTools(_ context.Context, selection ToolSelectionContext) ([]string, error) {
	query := recentToolSelectionQuery(selection.Messages, selector.RecentUserMessages)
	queryTokens := toolSearchTokens(query)
	type scoredTool struct {
		name  string
		score int
	}
	scored := make([]scoredTool, 0, len(selection.Tools))
	for _, tool := range selection.Tools {
		score := 0
		for token := range toolSearchTokens(toolSelectionDocument(tool)) {
			if queryTokens[token] {
				score++
			}
		}
		scored = append(scored, scoredTool{name: tool.Name, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].name < scored[j].name
	})
	limit := selection.Limit
	if limit <= 0 || limit > len(scored) {
		limit = len(scored)
	}
	names := make([]string, 0, limit)
	for _, tool := range scored {
		if len(names) == limit {
			break
		}
		if tool.score == 0 && !selector.IncludeUnmatched {
			continue
		}
		names = append(names, tool.name)
	}
	return names, nil
}

// EmbeddingFunc returns one vector per input text.
type EmbeddingFunc func(ctx context.Context, texts []string) ([][]float64, error)

// EmbeddingToolSelector performs semantic tool retrieval and caches vectors
// for unchanged tool definitions. Fallback is used when embedding fails.
type EmbeddingToolSelector struct {
	Embed           EmbeddingFunc
	MinScore        float64
	Fallback        ToolSelector
	MaxCacheEntries int
	cacheMu         sync.RWMutex
	embedMu         sync.Mutex
	vectorByID      map[string][]float64
}

func (selector *EmbeddingToolSelector) SelectTools(ctx context.Context, selection ToolSelectionContext) ([]string, error) {
	if selector == nil || selector.Embed == nil {
		return nil, fmt.Errorf("tool selection: embedding function is required")
	}
	query := recentToolSelectionQuery(selection.Messages, 3)
	if strings.TrimSpace(query) == "" {
		if selector.Fallback != nil {
			return selector.Fallback.SelectTools(ctx, selection)
		}
		return nil, nil
	}
	queryVectors, err := selector.Embed(ctx, []string{query})
	if err != nil || len(queryVectors) != 1 {
		if selector.Fallback != nil {
			return selector.Fallback.SelectTools(ctx, selection)
		}
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("tool selection: embedding function returned %d query vectors", len(queryVectors))
	}

	toolVectors, err := selector.toolVectors(ctx, selection.Tools)
	if err != nil {
		if selector.Fallback != nil {
			return selector.Fallback.SelectTools(ctx, selection)
		}
		return nil, err
	}
	type scoredTool struct {
		name  string
		score float64
	}
	scored := make([]scoredTool, 0, len(selection.Tools))
	for i, tool := range selection.Tools {
		score := cosineSimilarity(queryVectors[0], toolVectors[i])
		if score >= selector.MinScore {
			scored = append(scored, scoredTool{name: tool.Name, score: score})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].name < scored[j].name
	})
	if len(scored) == 0 && selector.Fallback != nil {
		return selector.Fallback.SelectTools(ctx, selection)
	}
	limit := selection.Limit
	if limit <= 0 || limit > len(scored) {
		limit = len(scored)
	}
	names := make([]string, limit)
	for i := 0; i < limit; i++ {
		names[i] = scored[i].name
	}
	return names, nil
}

func (selector *EmbeddingToolSelector) toolVectors(ctx context.Context, tools []ToolDefinition) ([][]float64, error) {
	selector.embedMu.Lock()
	defer selector.embedMu.Unlock()
	selector.cacheMu.Lock()
	if selector.vectorByID == nil {
		selector.vectorByID = make(map[string][]float64)
	}
	selector.cacheMu.Unlock()

	working := make(map[string][]float64, len(tools))
	missingIDs := make([]string, 0, len(tools))
	missingDocs := make([]string, 0, len(tools))
	missingSet := make(map[string]bool, len(tools))
	for _, tool := range tools {
		id := toolSelectionDocument(tool)
		selector.cacheMu.RLock()
		cached, ok := selector.vectorByID[id]
		selector.cacheMu.RUnlock()
		if ok {
			working[id] = append([]float64(nil), cached...)
		} else if !missingSet[id] {
			missingSet[id] = true
			missingIDs = append(missingIDs, id)
			missingDocs = append(missingDocs, id)
		}
	}
	if len(missingDocs) > 0 {
		vectors, err := selector.Embed(ctx, missingDocs)
		if err != nil {
			return nil, err
		}
		if len(vectors) != len(missingDocs) {
			return nil, fmt.Errorf("tool selection: embedding function returned %d vectors for %d tools", len(vectors), len(missingDocs))
		}
		for i, id := range missingIDs {
			working[id] = append([]float64(nil), vectors[i]...)
		}

		selector.cacheMu.Lock()
		maxEntries := selector.MaxCacheEntries
		if maxEntries <= 0 {
			maxEntries = 2048
		}
		if len(selector.vectorByID)+len(missingIDs) > maxEntries {
			selector.vectorByID = make(map[string][]float64, maxEntries)
			for _, tool := range tools {
				id := toolSelectionDocument(tool)
				if _, exists := selector.vectorByID[id]; exists {
					continue
				}
				selector.vectorByID[id] = append([]float64(nil), working[id]...)
				if len(selector.vectorByID) == maxEntries {
					break
				}
			}
		} else {
			for _, id := range missingIDs {
				selector.vectorByID[id] = append([]float64(nil), working[id]...)
			}
		}
		selector.cacheMu.Unlock()
	}

	out := make([][]float64, len(tools))
	for i, tool := range tools {
		out[i] = append([]float64(nil), working[toolSelectionDocument(tool)]...)
	}
	return out, nil
}

func cosineSimilarity(left, right []float64) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return -1
	}
	var dot, leftNorm, rightNorm float64
	for i := range left {
		dot += left[i] * right[i]
		leftNorm += left[i] * left[i]
		rightNorm += right[i] * right[i]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return -1
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func recentToolSelectionQuery(messages []Message, maxUserMessages int) string {
	if maxUserMessages <= 0 {
		maxUserMessages = 3
	}
	parts := make([]string, 0, maxUserMessages)
	for i := len(messages) - 1; i >= 0 && len(parts) < maxUserMessages; i-- {
		if messages[i].Role == RoleUser && messages[i].Content != "" {
			parts = append(parts, messages[i].Content)
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return strings.Join(parts, "\n")
}

func toolSelectionDocument(tool ToolDefinition) string {
	return tool.Name + " " + tool.Description + " " + strings.Join(tool.SearchTerms, " ")
}

func toolSearchTokens(value string) map[string]bool {
	tokens := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		if token != "" {
			out[token] = true
		}
	}
	var cjk []rune
	flushCJK := func() {
		for i, r := range cjk {
			out["cjk:"+string(r)] = true
			if i > 0 {
				out["cjk2:"+string(cjk[i-1:i+1])] = true
			}
		}
		cjk = cjk[:0]
	}
	for _, r := range []rune(strings.ToLower(value)) {
		if unicode.Is(unicode.Han, r) {
			cjk = append(cjk, r)
		} else if len(cjk) > 0 {
			flushCJK()
		}
	}
	if len(cjk) > 0 {
		flushCJK()
	}
	return out
}

func selectToolDefinitions(ctx context.Context, cfg *ToolSelectionConfig, messages []Message, tools []ToolDefinition) ([]ToolDefinition, error) {
	if cfg == nil {
		return tools, nil
	}
	selector := cfg.Selector
	if selector == nil {
		selector = KeywordToolSelector{}
	}
	names, err := selector.SelectTools(ctx, ToolSelectionContext{
		Messages: messages,
		Tools:    tools,
		Limit:    cfg.MaxVisible,
	})
	if err != nil {
		return nil, err
	}
	visible := make(map[string]bool, len(names)+len(cfg.AlwaysVisible))
	for _, name := range names {
		visible[name] = true
	}
	for _, name := range cfg.AlwaysVisible {
		visible[name] = true
	}
	selected := make([]ToolDefinition, 0, len(visible))
	for _, tool := range tools {
		if visible[tool.Name] {
			selected = append(selected, tool)
		}
	}
	return selected, nil
}

func ensureToolDefinition(selected, all []ToolDefinition, name string) []ToolDefinition {
	for _, tool := range selected {
		if tool.Name == name {
			return selected
		}
	}
	for _, tool := range all {
		if tool.Name == name {
			return append(selected, tool)
		}
	}
	return selected
}
