package agentcore

import (
	"strings"
	"testing"
)

func TestMessageCollapseForLLM_omitsThinking(t *testing.T) {
	m := Message{Role: RoleAssistant}.
		AppendThinkingBlock("secret").
		AppendTextBlock("hello")
	c := MessageCollapseForLLM(m, false)
	if strings.Contains(c.Content, "secret") {
		t.Fatalf("thinking leaked: %q", c.Content)
	}
	if !strings.Contains(c.Content, "hello") {
		t.Fatalf("want hello in %q", c.Content)
	}
	if len(c.Blocks) != 0 {
		t.Fatalf("blocks should be cleared, got %d", len(c.Blocks))
	}
}

func TestMessageCollapseForLLM_includeThinking(t *testing.T) {
	m := Message{}.AppendThinkingBlock("t").AppendTextBlock("x")
	c := MessageCollapseForLLM(m, true)
	if !strings.Contains(c.Content, "t") || !strings.Contains(c.Content, "x") {
		t.Fatalf("content %q", c.Content)
	}
}

func TestMessageCollapseForLLM_preservesImageBlocks(t *testing.T) {
	m := Message{
		Content: "look",
		Blocks: []ContentBlock{
			{Kind: BlockKindText, Text: "here"},
			{Kind: BlockKindImage, URL: "https://example.com/cat.png"},
			{Kind: BlockKindThinking, Text: "secret"},
		},
	}
	c := MessageCollapseForLLM(m, false)
	if got := c.Content; got != "look\nhere" {
		t.Fatalf("content = %q", got)
	}
	if len(c.Blocks) != 1 {
		t.Fatalf("blocks len = %d", len(c.Blocks))
	}
	if c.Blocks[0].Kind != BlockKindImage || c.Blocks[0].URL != "https://example.com/cat.png" {
		t.Fatalf("block = %#v", c.Blocks[0])
	}
}

func TestMessageTextBody_wrapsThinking(t *testing.T) {
	m := Message{}.AppendThinkingBlock("why").AppendTextBlock("hi")
	body := MessageTextBody(m)
	if !strings.Contains(body, "<thinking>") || !strings.Contains(body, "why") {
		t.Fatalf("body %q", body)
	}
}

func TestStructuredCompactionSummary_ToReadableSummary(t *testing.T) {
	s := StructuredCompactionSummary{
		ActiveTask: "deploy to prod",
		Goal:       "ship v2",
		Blocked:    "CI failing",
	}
	out := s.ToReadableSummary()
	if !strings.Contains(out, "deploy to prod") || !strings.Contains(out, "ship v2") || !strings.Contains(out, "CI failing") {
		t.Fatalf("out %q", out)
	}
}

func TestMergeContentBlocks_mergesToolCallFragments(t *testing.T) {
	got := MergeContentBlocks(nil,
		ContentBlock{Kind: BlockKindToolCall, ToolCallID: "call_1", Name: "lookup", Arguments: `{"q":"to`},
		ContentBlock{Kind: BlockKindToolCall, ToolCallID: "call_1", Name: "lookup", Arguments: `kyo"}`},
	)
	if len(got) != 1 {
		t.Fatalf("blocks len = %d", len(got))
	}
	if got[0].Arguments != `{"q":"tokyo"}` {
		t.Fatalf("arguments = %q", got[0].Arguments)
	}
}

func TestMergeContentBlocks_mergesThinkingSignature(t *testing.T) {
	got := MergeContentBlocks(nil,
		ContentBlock{Kind: BlockKindThinking, Text: "step one"},
		ContentBlock{Kind: BlockKindThinking, Text: " + step two"},
		ContentBlock{Kind: BlockKindThinking, Signature: "sig_1"},
	)
	if len(got) != 1 {
		t.Fatalf("blocks len = %d", len(got))
	}
	if got[0].Text != "step one + step two" {
		t.Fatalf("text = %q", got[0].Text)
	}
	if got[0].Signature != "sig_1" {
		t.Fatalf("signature = %q", got[0].Signature)
	}
}
