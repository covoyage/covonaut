package agentcore

import (
	"encoding/json"
	"strings"
)

// BlockKind classifies a segment of assistant (or user) content.
type BlockKind string

const (
	// BlockKindText is ordinary visible text.
	BlockKindText BlockKind = "text"
	// BlockKindThinking is model reasoning / chain-of-thought (often hidden from tools).
	BlockKindThinking BlockKind = "thinking"
	// BlockKindImage is an image input such as an HTTPS URL or data URL.
	BlockKindImage BlockKind = "image"
	// BlockKindToolCall is a structured tool call emitted by the assistant.
	BlockKindToolCall BlockKind = "tool_call"
)

// ContentBlock is one segment inside a message body (multi-block messages).
// DefaultConvertToLLM collapses text/thinking blocks into Content for providers
// that only support strings, while preserving richer blocks like images for
// providers that can send native multipart content.
type ContentBlock struct {
	Kind       BlockKind `json:"kind"`
	Text       string    `json:"text,omitempty"`
	URL        string    `json:"url,omitempty"`
	MediaType  string    `json:"media_type,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Signature  string    `json:"signature,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Name       string    `json:"name,omitempty"`
	Arguments  string    `json:"arguments,omitempty"`
}

// MessageTextBody returns legacy Content plus all text/thinking blocks in order.
// Thinking segments are wrapped in <thinking>...</thinking> for traceability;
// callers that feed the LLM should use MessageCollapseForLLM instead.
func MessageTextBody(m Message) string {
	var b strings.Builder
	if m.Content != "" {
		b.WriteString(m.Content)
	}
	for _, bl := range m.Blocks {
		switch bl.Kind {
		case BlockKindText:
			if bl.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(bl.Text)
		case BlockKindThinking:
			if bl.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("<thinking>\n")
			b.WriteString(bl.Text)
			b.WriteString("\n</thinking>")
		case BlockKindImage:
			if bl.URL == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("[image] ")
			b.WriteString(bl.URL)
		case BlockKindToolCall:
			if bl.Name == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("→ tool_call(")
			b.WriteString(bl.Name)
			b.WriteString("): ")
			b.WriteString(bl.Arguments)
		default:
			if bl.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(bl.Text)
			}
		}
	}
	return b.String()
}

// MessageCollapseForLLM builds a provider-facing copy: concatenates text blocks
// and legacy Content. Thinking blocks are preserved on out.Blocks so that
// providers can serialize them back (e.g. OpenAI reasoning_content, Anthropic
// thinking content blocks). Non-text rich blocks (for example images) are also
// preserved on out.Blocks.
func MessageCollapseForLLM(m Message, includeThinking bool) Message {
	out := m
	var b strings.Builder
	var kept []ContentBlock
	if m.Content != "" {
		b.WriteString(m.Content)
	}
	for _, bl := range m.Blocks {
		switch bl.Kind {
		case BlockKindText:
			if bl.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(bl.Text)
		case BlockKindThinking:
			if bl.Text == "" {
				continue
			}
			if includeThinking {
				kept = append(kept, bl)
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(bl.Text)
			}
		case BlockKindImage:
			if bl.URL == "" {
				continue
			}
			kept = append(kept, bl)
		case BlockKindToolCall:
			// Tool call structure is already preserved on Message.ToolCalls.
			continue
		default:
			if bl.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(bl.Text)
			}
		}
	}
	out.Content = b.String()
	out.Blocks = kept
	return out
}

// MessageStringForSummary formats a message for compaction / logging (includes
// thinking, tool calls, and tool-call payloads in plain text).
func MessageStringForSummary(m Message) string {
	var b strings.Builder
	body := MessageTextBody(m)
	if body != "" {
		b.WriteString(body)
	}
	for _, tc := range m.ToolCalls {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("→ tool_call(")
		b.WriteString(tc.Name)
		b.WriteString("): ")
		b.WriteString(tc.Arguments)
	}
	return b.String()
}

// AppendTextBlock appends a text block and returns the mutated message.
func (m Message) AppendTextBlock(text string) Message {
	if text == "" {
		return m
	}
	m.Blocks = append(m.Blocks, ContentBlock{Kind: BlockKindText, Text: text})
	return m
}

// AppendThinkingBlock appends a thinking block.
func (m Message) AppendThinkingBlock(text string) Message {
	if text == "" {
		return m
	}
	m.Blocks = append(m.Blocks, ContentBlock{Kind: BlockKindThinking, Text: text})
	return m
}

// AppendImageURLBlock appends an image block using an HTTPS URL or data URL.
func (m Message) AppendImageURLBlock(url string) Message {
	if url == "" {
		return m
	}
	m.Blocks = append(m.Blocks, ContentBlock{Kind: BlockKindImage, URL: url})
	return m
}

// MergeContentBlocks appends blocks while coalescing adjacent text/thinking
// segments so streaming providers do not produce one block per token chunk.
func MergeContentBlocks(dst []ContentBlock, src ...ContentBlock) []ContentBlock {
	for _, bl := range src {
		if len(dst) > 0 {
			last := &dst[len(dst)-1]
			if last.Kind == bl.Kind && last.URL == "" && bl.URL == "" &&
				last.MediaType == bl.MediaType && last.Detail == bl.Detail &&
				(bl.Kind == BlockKindText || bl.Kind == BlockKindThinking) {
				last.Text += bl.Text
				if bl.Signature != "" {
					last.Signature = bl.Signature
				}
				continue
			}
			if last.Kind == BlockKindToolCall && bl.Kind == BlockKindToolCall &&
				(last.ToolCallID == bl.ToolCallID || last.ToolCallID == "" || bl.ToolCallID == "") &&
				(last.Name == bl.Name || last.Name == "" || bl.Name == "") {
				if last.ToolCallID == "" {
					last.ToolCallID = bl.ToolCallID
				}
				if last.Name == "" {
					last.Name = bl.Name
				}
				last.Arguments += bl.Arguments
				if bl.Signature != "" {
					last.Signature = bl.Signature
				}
				continue
			}
		}
		dst = append(dst, bl)
	}
	return dst
}

// StructuredCompactionSummary is the JSON shape requested when
// Config.StructuredCompaction is enabled.
type StructuredCompactionSummary struct {
	ActiveTask         string `json:"active_task"`
	Goal               string `json:"goal"`
	ConstraintsPrefs   string `json:"constraints_preferences"`
	CompletedActions   string `json:"completed_actions"`
	ActiveState        string `json:"active_state"`
	InProgress         string `json:"in_progress"`
	Blocked            string `json:"blocked"`
	KeyDecisions       string `json:"key_decisions"`
	ResolvedQuestions  string `json:"resolved_questions"`
	PendingUserAsks    string `json:"pending_user_asks"`
	RelevantFiles      string `json:"relevant_files"`
	RemainingWork      string `json:"remaining_work"`
	CriticalContext    string `json:"critical_context"`
}

// ToReadableSummary renders the structured fields as a markdown block for the
// compaction user message and for models that only see text.
func (s StructuredCompactionSummary) ToReadableSummary() string {
	var b strings.Builder
	w := func(title, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(title)
		b.WriteString("\n")
		b.WriteString(body)
	}
	w("Active Task", s.ActiveTask)
	w("Goal", s.Goal)
	w("Constraints & Preferences", s.ConstraintsPrefs)
	w("Completed Actions", s.CompletedActions)
	w("Active State", s.ActiveState)
	w("In Progress", s.InProgress)
	w("Blocked", s.Blocked)
	w("Key Decisions", s.KeyDecisions)
	w("Resolved Questions", s.ResolvedQuestions)
	w("Pending User Asks", s.PendingUserAsks)
	w("Relevant Files", s.RelevantFiles)
	w("Remaining Work", s.RemainingWork)
	w("Critical Context", s.CriticalContext)
	if b.Len() == 0 {
		return "(empty structured summary)"
	}
	return b.String()
}

// MarshalJSONMetadata stores the structured summary on message metadata.
func (s StructuredCompactionSummary) MarshalJSONMetadata() map[string]any {
	raw, _ := json.Marshal(s)
	return map[string]any{
		"structured_compaction": json.RawMessage(raw),
	}
}
