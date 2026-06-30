package theme

import (
	"strings"
	"sync/atomic"
)

// Palette holds pre-built Style values for one semantic theme + color mode.
type Palette struct {
	Semantic *SemanticTheme
	Mode     ColorMode

	User, Assistant, System, Tool, ToolName, Error, Success Style
	Dim, Bold, Handoff, Code, CodeBlock, Usage              Style
	Accent, Muted, BorderMuted, Border, BorderAccent        Style
	SelectHighlight, SelectDescription                      Style
	SettingsKey, SettingsValueSelected                      Style
	LoaderSpinner, ProgressPrompt, ProgressCompletion       Style
	Thinking                                                Style
}

var atomicPalette atomic.Pointer[Palette]

func CurrentPalette() *Palette {
	p := atomicPalette.Load()
	if p == nil {
		p = BuildPalette(DefaultSemanticDark(), DetectColorMode())
		atomicPalette.Store(p)
	}
	return p
}

// BuildPalette materializes SemanticTheme into Style values.
func BuildPalette(sem *SemanticTheme, mode ColorMode) *Palette {
	if sem == nil {
		sem = DefaultSemanticDark()
	}
	p := &Palette{Semantic: sem, Mode: mode}

	fg := func(hex string) Style {
		s := NewStyle()
		c := FgParams(hex, mode)
		if c != "" {
			s = s.WithFgParams(c)
		}
		return s
	}

	userHex := sem.UserMessage
	if userHex == "" {
		userHex = sem.Accent
	}
	p.User = fg(userHex).Bold()

	assistHex := sem.AssistantText
	if assistHex == "" {
		assistHex = sem.Text
	}
	if assistHex == "" {
		p.Assistant = NewStyle().Fg(BrightWhite)
	} else {
		p.Assistant = fg(assistHex)
	}

	sysHex := sem.System
	if sysHex == "" {
		sysHex = sem.Warning
	}
	p.System = fg(sysHex).Italic()
	if FgParams(sysHex, mode) == "" {
		p.System = NewStyle().Fg(BrightYellow).Italic()
	}

	p.Tool = fg(sem.Accent)
	if FgParams(sem.Accent, mode) == "" {
		p.Tool = NewStyle().Fg(BrightMagenta)
	}

	p.ToolName = fg(sem.Accent).Bold()
	if FgParams(sem.Accent, mode) == "" {
		p.ToolName = NewStyle().Fg(Magenta).Bold()
	}

	p.Error = fg(sem.Error).Bold()
	if FgParams(sem.Error, mode) == "" {
		p.Error = NewStyle().Fg(BrightRed).Bold()
	}

	p.Success = fg(sem.Success)
	if FgParams(sem.Success, mode) == "" {
		p.Success = NewStyle().Fg(BrightGreen)
	}

	p.Dim = fg(sem.Dim)
	if FgParams(sem.Dim, mode) == "" {
		p.Dim = NewStyle().Dim()
	}

	p.Bold = NewStyle().Bold()
	p.Handoff = fg(sem.Border).Bold()
	if FgParams(sem.Border, mode) == "" {
		p.Handoff = NewStyle().Fg(BrightBlue).Bold()
	}

	p.Code = fg(sem.MdCode)
	if FgParams(sem.MdCode, mode) == "" {
		p.Code = NewStyle().Fg(BrightGreen)
	}

	p.CodeBlock = fg(sem.MdCodeBlock).Dim()
	if FgParams(sem.MdCodeBlock, mode) == "" {
		p.CodeBlock = NewStyle().Fg(Green).Dim()
	}

	p.Usage = fg(sem.MdCodeBlockBorder)
	if FgParams(sem.MdCodeBlockBorder, mode) == "" {
		p.Usage = NewStyle().Fg(BrightBlack)
	}

	p.Accent = fg(sem.Accent).Bold()
	if FgParams(sem.Accent, mode) == "" {
		p.Accent = NewStyle().Fg(BrightCyan).Bold()
	}

	p.Muted = fg(sem.Muted)
	p.BorderMuted = fg(sem.BorderMuted)
	p.Border = fg(sem.Border)
	p.BorderAccent = fg(sem.BorderAccent)

	p.SelectHighlight = fg(sem.Accent).Bold()
	if FgParams(sem.Accent, mode) == "" {
		p.SelectHighlight = NewStyle().Fg(BrightCyan).Bold()
	}
	p.SelectDescription = p.Dim

	p.SettingsKey = fg(sem.Accent).Bold()
	if FgParams(sem.Accent, mode) == "" {
		p.SettingsKey = NewStyle().Fg(BrightCyan).Bold()
	}
	p.SettingsValueSelected = fg(sem.Warning)
	if FgParams(sem.Warning, mode) == "" {
		p.SettingsValueSelected = NewStyle().Fg(BrightYellow)
	}

	spin := sem.LoaderSpinner
	if spin == "" {
		spin = sem.BorderAccent
	}
	if spin == "" {
		spin = sem.Accent
	}
	p.LoaderSpinner = fg(spin)
	if FgParams(spin, mode) == "" {
		p.LoaderSpinner = NewStyle().Fg(Cyan)
	}

	prog := sem.ProgressBar
	if prog == "" {
		prog = sem.Border
	}
	p.ProgressPrompt = fg(prog)
	if FgParams(prog, mode) == "" {
		p.ProgressPrompt = NewStyle().Fg(Blue)
	}
	p.ProgressCompletion = p.Success

	thinkingHex := sem.ThinkingText
	if thinkingHex == "" {
		thinkingHex = sem.Dim
	}
	p.Thinking = fg(thinkingHex).Italic()
	if FgParams(thinkingHex, mode) == "" {
		p.Thinking = NewStyle().Fg(BrightBlack).Italic()
	}

	return p
}

// SyncPaletteGlobals updates the atomic palette and legacy package-level Style
// variables (same snapshot) for code that still references StyleUser etc.
func SyncPaletteGlobals(sem *SemanticTheme, mode ColorMode) {
	p := BuildPalette(sem, mode)
	atomicPalette.Store(p)
	StyleUser = p.User
	StyleAssistant = p.Assistant
	StyleSystem = p.System
	StyleTool = p.Tool
	StyleToolName = p.ToolName
	StyleError = p.Error
	StyleSuccess = p.Success
	StyleDim = p.Dim
	StyleBold = p.Bold
	StyleHandoff = p.Handoff
	StyleCode = p.Code
	StyleCodeBlock = p.CodeBlock
	StyleUsage = p.Usage
	StyleThinking = p.Thinking
}

func init() {
	SyncPaletteGlobals(DefaultSemanticDark(), DetectColorMode())
}

// SemStyle builds a foreground Style from a hex / 256 index string.
func SemStyle(hex string, mode ColorMode) Style {
	s := NewStyle()
	c := FgParams(strings.TrimSpace(hex), mode)
	if c != "" {
		s = s.WithFgParams(c)
	}
	return s
}
