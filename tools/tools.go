// Package tools provides a set of built-in filesystem and shell tools for covonaut agents.
//
// This package is designed as a covonaut Extension. Register it with an agent to add
// read, edit, ls, grep, find, and bash tools to the agent's tool registry.
//
// Usage:
//
//	import "github.com/covoyage/covonaut/tools"
//
//	ext := tools.NewExtension(tools.ExtensionConfig{WorkingDir: "/path/to/project"})
//	agent := agentcore.New(agentcore.NewConfig(agentcore.WithExtensions(ext)))
//
// Each tool supports pluggable operations so you can delegate to remote systems (e.g. SSH).
package tools

import (
	"context"
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
)

// ExtensionConfig configures the built-in tools extension.
type ExtensionConfig struct {
	// WorkingDir is the base directory for all relative paths. Defaults to the
	// current working directory if empty.
	WorkingDir string

	// Read configures the read tool. If nil, default local filesystem operations are used.
	Read *ReadToolConfig

	// Edit configures the edit tool. If nil, default local filesystem operations are used.
	Edit *EditToolConfig

	// Ls configures the ls tool. If nil, default local filesystem operations are used.
	Ls *LsToolConfig

	// Grep configures the grep tool. If nil, default local filesystem + ripgrep is used.
	Grep *GrepToolConfig

	// Find configures the find tool. If nil, default local filesystem + fd is used.
	Find *FindToolConfig

	// Bash configures the bash tool. If nil, default local shell execution is used.
	Bash *BashToolConfig

	// WriteFile configures the write_file tool. If nil, default local filesystem operations are used.
	WriteFile *WriteFileToolConfig

	// Patch configures the patch tool. If nil, default local filesystem operations are used.
	Patch *PatchToolConfig

	// Process configures the process tool. If nil, default local process operations are used.
	Process *ProcessToolConfig

	// Vision configures the vision_analyze tool. If nil, default operations are used.
	Vision *VisionToolConfig

	// View configures the view tool. If nil, default local filesystem operations are used.
	View *ViewToolConfig

	// Glob configures the glob tool. If nil, default local filesystem operations are used.
	Glob *GlobToolConfig

	// Delete configures the delete tool. If nil, default local filesystem operations are used.
	Delete *DeleteToolConfig

	// Move configures the move tool. If nil, default local filesystem operations are used.
	Move *MoveToolConfig

	// Git configures the git tools. If nil, default local git operations are used.
	Git *GitToolConfig

	// Browser configures the browser tools. If nil, browser tools are not included.
	Browser *BrowserToolConfig

	// WebSearch configures the web_search tool. If nil, default auto-selecting search is used.
	WebSearch *WebSearchToolConfig

	// WebFetch configures the web_fetch tool. If nil, default HTTP fetch is used.
	WebFetch *WebFetchToolConfig

	// ExecuteCode configures the execute_code tool. If nil, a default configuration is used.
	ExecuteCode *ExecuteCodeToolConfig

	// ComputerUse enables the computer_use tool (macOS desktop control). macOS only.
	ComputerUse bool

	// MaxBytes is the default output byte limit shared across all tools (default: 50KB).
	MaxBytes int64

	// MaxLines is the default output line limit shared across all tools (default: 2000).
	MaxLines int64
}

func (c *ExtensionConfig) setDefaults() {
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024 // 50KB
	}
	if c.MaxLines <= 0 {
		c.MaxLines = 2000
	}
}

// Extension is a covonaut Extension that registers built-in filesystem and shell tools.
type Extension struct {
	config ExtensionConfig
	tools  []*agentcore.Tool
}

// NewExtension creates a new built-in tools extension with the given configuration.
func NewExtension(cfg ExtensionConfig) *Extension {
	cfg.setDefaults()
	return &Extension{config: cfg}
}

// Name returns the extension name.
func (e *Extension) Name() string { return "builtin-tools" }

// Init initializes the extension and registers all tools with the agent.
func (e *Extension) Init(_ context.Context, agent *agentcore.Agent) error {
	e.tools = BuildTools(e.config)
	agent.RegisterTools(e.tools...)
	return nil
}

// Dispose tears down the extension.
func (e *Extension) Dispose() error { return nil }

// Tools returns the tools managed by this extension.
func (e *Extension) Tools() []*agentcore.Tool { return e.tools }

// BuildTools constructs the full set of built-in tools from the given config.
// This is useful when you want the tools but not the extension lifecycle.
func BuildTools(cfg ExtensionConfig) []*agentcore.Tool {
	cfg.setDefaults()
	tools := []*agentcore.Tool{
		NewReadTool(cfg.WorkingDir, cfg.Read),
		NewEditTool(cfg.WorkingDir, cfg.Edit),
		NewLsTool(cfg.WorkingDir, cfg.Ls),
		NewGrepTool(cfg.WorkingDir, cfg.Grep),
		NewFindTool(cfg.WorkingDir, cfg.Find),
		NewBashTool(cfg.WorkingDir, cfg.Bash),
		NewWriteFileTool(cfg.WorkingDir, cfg.WriteFile),
		NewPatchTool(cfg.WorkingDir, cfg.Patch),
		NewProcessTool(cfg.WorkingDir, cfg.Process),
		NewVisionTool(cfg.WorkingDir, cfg.Vision),
		NewViewTool(cfg.WorkingDir, cfg.View),
		NewGlobTool(cfg.WorkingDir, cfg.Glob),
		NewDeleteTool(cfg.WorkingDir, cfg.Delete),
		NewMoveTool(cfg.WorkingDir, cfg.Move),
		NewGitStatusTool(cfg.WorkingDir, cfg.Git),
		NewGitDiffTool(cfg.WorkingDir, cfg.Git),
		NewGitLogTool(cfg.WorkingDir, cfg.Git),
	}

	tools = append(tools,
		NewWebSearchTool(cfg.WebSearch),
		NewWebFetchTool(cfg.WebFetch),
	)

	if cfg.Browser != nil {
		tools = append(tools,
			NewBrowserTool(cfg.Browser),
		)
	}

	if cfg.ExecuteCode != nil {
		tools = append(tools,
			NewExecuteCodeTool(cfg.ExecuteCode),
		)
	}

	if cfg.ComputerUse {
		tools = append(tools,
			NewComputerUseTool(nil),
		)
	}

	return tools
}

// ToolResult is the standard result type for all built-in tools.
// It serializes cleanly to JSON for the LLM.
type ToolResult struct {
	Content string `json:"content"`
	Details any    `json:"details,omitempty"`
}

func result(content string, details any) (any, error) {
	return ToolResult{Content: content, Details: details}, nil
}

func resultErrf(format string, args ...any) (any, error) {
	return nil, fmt.Errorf(format, args...)
}
