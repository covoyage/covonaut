package theme

// SemanticTheme holds named colors for the TUI (hex "#rrggbb", decimal "0"–"255"
// for 256 palette, or "" for terminal default foreground/background).
// JSON themes use the same keys as pi-mono coding-agent dark.json / light.json.
type SemanticTheme struct {
	Name string `json:"name"`

	Accent        string `json:"accent"`
	Border        string `json:"border"`
	BorderAccent  string `json:"borderAccent"`
	BorderMuted   string `json:"borderMuted"`
	Success       string `json:"success"`
	Error         string `json:"error"`
	Warning       string `json:"warning"`
	Muted         string `json:"muted"`
	Dim           string `json:"dim"`
	Text          string `json:"text"`
	System        string `json:"system"` // system / info lines (optional)
	ThinkingText  string `json:"thinkingText"`
	UserMessage   string `json:"userMessageText"` // if empty, derive from Accent + bold
	AssistantText string `json:"assistantText"`   // extension: not in pi JSON; optional

	SelectedBg    string `json:"selectedBg"`
	UserMessageBg string `json:"userMessageBg"`
	ToolPendingBg string `json:"toolPendingBg"`
	ToolSuccessBg string `json:"toolSuccessBg"`
	ToolErrorBg   string `json:"toolErrorBg"`

	MdHeading         string `json:"mdHeading"`
	MdLink            string `json:"mdLink"`
	MdLinkUrl         string `json:"mdLinkUrl"`
	MdCode            string `json:"mdCode"`
	MdCodeBlock       string `json:"mdCodeBlock"`
	MdCodeBlockBorder string `json:"mdCodeBlockBorder"`
	MdQuote           string `json:"mdQuote"`
	MdQuoteBorder     string `json:"mdQuoteBorder"`
	MdHr              string `json:"mdHr"`
	MdListBullet      string `json:"mdListBullet"`

	SyntaxComment     string `json:"syntaxComment"`
	SyntaxKeyword     string `json:"syntaxKeyword"`
	SyntaxFunction    string `json:"syntaxFunction"`
	SyntaxVariable    string `json:"syntaxVariable"`
	SyntaxString      string `json:"syntaxString"`
	SyntaxNumber      string `json:"syntaxNumber"`
	SyntaxType        string `json:"syntaxType"`
	SyntaxOperator    string `json:"syntaxOperator"`
	SyntaxPunctuation string `json:"syntaxPunctuation"`

	LoaderSpinner string `json:"loaderSpinner"` // optional; default Accent
	ProgressBar   string `json:"progressBar"`   // optional
}

// DefaultSemanticDark uses Claude Code's warm dark palette.
func DefaultSemanticDark() *SemanticTheme {
	return &SemanticTheme{
		Name: "dark",

		Accent:       "#d4a843", // warm amber
		Border:       "#d4a843",
		BorderAccent: "#e6b84c",
		BorderMuted:  "#606070",
		Success:      "#7ec87b", // soft green
		Error:        "#e06c75", // soft red
		Warning:      "#d4a843", // amber
		Muted:        "#a0a0b0",
		Dim:          "#888898",
		Text:         "#e0e0e0",
		System:       "#d4a843",
		ThinkingText: "#9098a8",

		UserMessage:   "#e6b84c",
		AssistantText: "#e0e0e0",

		SelectedBg:    "#3c3c50",
		UserMessageBg: "#2d2d3f",
		ToolPendingBg: "#2d2d3f",
		ToolSuccessBg: "#283228",
		ToolErrorBg:   "#3c2828",

		MdHeading:         "#d4a843",
		MdLink:            "#61afef",
		MdLinkUrl:         "#a0a0b0",
		MdCode:            "#98c379",
		MdCodeBlock:       "#98c379",
		MdCodeBlockBorder: "#606070",
		MdQuote:           "#a0a0b0",
		MdQuoteBorder:     "#d4a843",
		MdHr:              "#606070",
		MdListBullet:      "#d4a843",

		SyntaxComment:     "#9098a8",
		SyntaxKeyword:     "#c678dd",
		SyntaxFunction:    "#e5c07b",
		SyntaxVariable:    "#e06c75",
		SyntaxString:      "#98c379",
		SyntaxNumber:      "#d19a66",
		SyntaxType:        "#56b6c2",
		SyntaxOperator:    "#abb2bf",
		SyntaxPunctuation: "#abb2bf",

		LoaderSpinner: "#d4a843",
		ProgressBar:   "#d4a843",
	}
}

// DefaultSemanticLight is a readable light palette (not identical to pi light.json).
func DefaultSemanticLight() *SemanticTheme {
	return &SemanticTheme{
		Name: "light",

		Accent:       "#0066cc",
		Border:       "#335599",
		BorderAccent: "#0088aa",
		BorderMuted:  "#999999",
		Success:      "#2e7d32",
		Error:        "#c62828",
		Warning:      "#f57f17",
		Muted:        "#757575",
		Dim:          "#9e9e9e",
		Text:         "#212121",
		System:       "#bf360c",
		ThinkingText: "#757575",

		UserMessage:   "#006064",
		AssistantText: "#212121",

		SelectedBg:    "#e3f2fd",
		UserMessageBg: "#eceff1",
		ToolPendingBg: "#fff8e1",
		ToolSuccessBg: "#e8f5e9",
		ToolErrorBg:   "#ffebee",

		MdHeading:         "#b8860b",
		MdLink:            "#1565c0",
		MdLinkUrl:         "#9e9e9e",
		MdCode:            "#00695c",
		MdCodeBlock:       "#2e7d32",
		MdCodeBlockBorder: "#bdbdbd",
		MdQuote:           "#616161",
		MdQuoteBorder:     "#9e9e9e",
		MdHr:              "#bdbdbd",
		MdListBullet:      "#0066cc",

		SyntaxComment:     "#008000",
		SyntaxKeyword:     "#0000ff",
		SyntaxFunction:    "#795e26",
		SyntaxVariable:    "#001080",
		SyntaxString:      "#a31515",
		SyntaxNumber:      "#098658",
		SyntaxType:        "#267f99",
		SyntaxOperator:    "#000000",
		SyntaxPunctuation: "#000000",

		LoaderSpinner: "#0066cc",
		ProgressBar:   "#1565c0",
	}
}
