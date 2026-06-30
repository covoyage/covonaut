package tools

import (
	"github.com/covoyage/covonaut/agentcore"
)

// ToolsExtension implements the covonaut Extension interface to provide
// filesystem, shell, and web tools as a bundled extension.
type ToolsExtension struct {
	cwd string
}

// NewToolsExtension creates a new tools extension with the given working directory.
func NewToolsExtension(cwd string) *ToolsExtension {
	return &ToolsExtension{cwd: cwd}
}

// Init implements the Extension interface.
func (e *ToolsExtension) Init() error {
	return nil
}

// Dispose implements the Extension interface.
func (e *ToolsExtension) Dispose() error {
	return nil
}

// ProvideTools implements the ToolProvider interface.
func (e *ToolsExtension) ProvideTools() []*agentcore.Tool {
	return []*agentcore.Tool{
		NewReadTool(e.cwd, nil),
		NewEditTool(e.cwd, nil),
		NewLsTool(e.cwd, nil),
		NewGrepTool(e.cwd, nil),
		NewFindTool(e.cwd, nil),
		NewBashTool(e.cwd, nil),
		NewWriteFileTool(e.cwd, nil),
		NewPatchTool(e.cwd, nil),
		NewProcessTool(e.cwd, nil),
		NewVisionTool(e.cwd, nil),
		NewWebSearchTool(nil),
		NewWebFetchTool(nil),
		NewViewTool(e.cwd, nil),
		NewGlobTool(e.cwd, nil),
		NewDeleteTool(e.cwd, nil),
		NewMoveTool(e.cwd, nil),
		NewGitStatusTool(e.cwd, nil),
		NewGitDiffTool(e.cwd, nil),
		NewGitLogTool(e.cwd, nil),
	}
}

// ProvideSystemPrompt implements the SystemPromptProvider interface.
func (e *ToolsExtension) ProvideSystemPrompt() string {
	return `You have access to the following tools:
- read: Read file contents with offset/limit support
- edit: Edit files using exact text replacement
- write_file: Write content to a file (creates or overwrites)
- patch: Replace an exact string in a file
- ls: List directory contents
- grep: Search file contents with regex support
- find: Search for files by glob pattern
- bash: Execute shell commands
- process: Manage background processes (spawn/status/wait/kill/list)
- vision_analyze: Analyze images using a vision-capable LLM
- web_search: Search the web
- web_fetch: Fetch and extract content from URLs
- view: View directory structure as a tree
- glob: Find files matching a glob pattern
- delete: Delete a file or directory
- move: Move or rename a file or directory
- git_status: Show git working tree status
- git_diff: Show git diff
- git_log: Show commit logs

When reading files, use offset and limit to read specific sections.
When editing files, ensure oldText is unique and non-overlapping.
When writing files, parent directories are created automatically.
When patching, old_string must match exactly once in the file.
When executing commands, be aware of the working directory.`
}
