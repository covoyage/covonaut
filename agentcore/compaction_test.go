package agentcore

import (
	"testing"
	"time"
)

func TestContentLengthForBudgetString(t *testing.T) {
	got := contentLengthForBudget("hello")
	if got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}

func TestContentLengthForBudgetNil(t *testing.T) {
	got := contentLengthForBudget(nil)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestContentLengthForBudgetContentBlocks(t *testing.T) {
	blocks := []ContentBlock{
		{Kind: BlockKindText, Text: "hello"},
		{Kind: BlockKindImage, URL: "data:image/png;base64,abc"},
		{Kind: BlockKindText, Text: "world"},
	}
	got := contentLengthForBudget(blocks)
	// image: 1600*4 = 6400, text: 5 + 5 = 10
	if got != 6410 {
		t.Fatalf("expected 6410, got %d", got)
	}
}

func TestContentLengthForBudgetDefault(t *testing.T) {
	got := contentLengthForBudget(42)
	// fmt.Sprintf("%v", 42) = "42" → 2 chars
	if got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
}

func TestShouldCompactDisabled(t *testing.T) {
	if shouldCompact(nil, nil, 0, 0, 0, 0, nil) {
		t.Fatal("should not compact when contextWindow <= 0")
	}
}

func TestShouldCompactBelowThreshold(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "system prompt"},
		{Role: RoleUser, Content: "hi"},
	}
	if shouldCompact(msgs, nil, 100000, 25000, 0, 0, nil) {
		t.Fatal("should not compact when below threshold")
	}
}

func TestShouldCompactAboveThreshold(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: string(make([]byte, 400000))}, // ~100k tokens
	}
	if !shouldCompact(msgs, nil, 100000, 25000, 0, 0, nil) {
		t.Fatal("should compact when above threshold")
	}
}

func TestShouldCompactCooldown(t *testing.T) {
	cs := &compactionState{}
	cs.summaryFailureCooldown = time.Now().Add(time.Hour)
	if shouldCompact(nil, nil, 100000, 0, 0, 0, cs) {
		t.Fatal("should not compact during cooldown")
	}
}

func TestShouldCompactIneffective(t *testing.T) {
	cs := &compactionState{}
	cs.ineffectiveCompactions = 2
	if shouldCompact(nil, nil, 100000, 0, 0, 0, cs) {
		t.Fatal("should not compact after 2 ineffective compactions")
	}
}

func TestFindCutPointBasic(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		// Each message ~200 tokens (800 chars)
		content := string(make([]byte, 800))
		if i == 0 {
			msgs[i] = Message{Role: RoleSystem, Content: "sys"}
		} else if i%2 == 1 {
			msgs[i] = Message{Role: RoleUser, Content: content}
		} else {
			msgs[i] = Message{Role: RoleAssistant, Content: content}
		}
	}
	cut := findCutPoint(msgs, 2000, 3)
	if cut <= 0 {
		t.Fatal("expected positive cut point")
	}
}

func TestFindCutPointTooFew(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "a"},
		{Role: RoleAssistant, Content: "b"},
	}
	cut := findCutPoint(msgs, 2000, 3)
	if cut != 0 {
		t.Fatalf("expected 0 for too few messages, got %d", cut)
	}
}

func TestFindCutPointSkipsSystem(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		content := string(make([]byte, 800))
		if i == 0 {
			msgs[i] = Message{Role: RoleSystem, Content: "system prompt"}
		} else if i%2 == 1 {
			msgs[i] = Message{Role: RoleUser, Content: content}
		} else {
			msgs[i] = Message{Role: RoleAssistant, Content: content}
		}
	}
	cut := findCutPoint(msgs, 2000, 3)
	// Should skip the system message (index 0)
	if cut < 1 {
		t.Fatalf("expected cut >= 1 to protect system message, got %d", cut)
	}
}

func TestAlignBoundaryForward(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "a"},
		{Role: RoleTool, Content: "result1"},
		{Role: RoleTool, Content: "result2"},
		{Role: RoleAssistant, Content: "b"},
	}
	cut := alignBoundaryForward(msgs, 1)
	if cut != 3 {
		t.Fatalf("expected 3 (skip tool messages), got %d", cut)
	}
}

func TestAlignBoundaryForwardNoTool(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "a"},
		{Role: RoleAssistant, Content: "b"},
	}
	cut := alignBoundaryForward(msgs, 1)
	if cut != 1 {
		t.Fatalf("expected 1, got %d", cut)
	}
}

func TestFindTailCutByTokensBasic(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		content := string(make([]byte, 200)) // ~50 tokens each
		msgs[i] = Message{Role: RoleUser, Content: content}
	}
	tail := findTailCutByTokens(msgs, 2, 100)
	if tail > 10 {
		t.Fatalf("tail cut %d > len(msgs)", tail)
	}
}

func TestFindTailCutByTokensEmpty(t *testing.T) {
	if got := findTailCutByTokens(nil, 0, 100); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestFindTailCutByTokensSkipsSystem(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "a"},
		{Role: RoleAssistant, Content: "b"},
	}
	tail := findTailCutByTokens(msgs, 0, 10)
	if tail <= 0 {
		t.Fatal("expected positive tail cut")
	}
}
