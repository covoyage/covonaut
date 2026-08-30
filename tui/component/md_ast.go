package component

// Block-level AST for the markdown renderer.
//
// Parsing is CommonMark + GFM (tables, task lists, strikethrough, autolinks),
// plus footnotes, definition lists, and GitHub-style alerts.
// This tree is a terminal-oriented view of that AST. Rendering stays in this
// package so TUI theme hooks (syntax, mermaid, images) are not tied to HTML.

type mdBlockKind int

const (
	mdPara mdBlockKind = iota
	mdHeading
	mdFence
	mdQuote
	mdList
	mdListItem
	mdHR
	mdTable
	mdBlank
	mdMath
	mdFootnote
	mdDefList
	mdDefItem
	mdImage
)

type mdAlign int

const (
	mdAlignNone mdAlign = iota
	mdAlignLeft
	mdAlignCenter
	mdAlignRight
)

type mdBlock struct {
	Kind mdBlockKind

	// Heading level (1-6), quote depth, or list marker indent.
	Level int

	// Fence language (first word of the info string) and raw info string.
	Lang string
	Info string

	// Leaf text for math blocks.
	Text string

	// Parsed inlines for paragraphs and headings.
	Inlines []inlineNode

	// Fence body lines (not joined), so empty lines are preserved.
	Lines []string

	// Lists.
	Ordered  bool
	Start    int
	Checked  *bool // task-list checkbox; nil = not a task
	MarkerW  int   // columns taken by the list marker + following space
	Tight    bool
	Children []mdBlock

	// Tables.
	Align       []mdAlign
	HeaderCells [][]inlineNode
	RowCells    [][][]inlineNode

	// Alert is a GitHub-style callout type when Kind is mdQuote
	// (NOTE, TIP, IMPORTANT, WARNING, CAUTION). Empty = normal quote.
	Alert string
}
