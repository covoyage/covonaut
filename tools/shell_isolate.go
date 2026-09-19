package tools

import "os/exec"

// IsolateCommand keeps a shell child off the TUI controlling terminal.
// Stdin stays nil so Run connects /dev/null; process-group flags are set
// so the child cannot steal mouse, focus, or job-control from the chat UI.
func IsolateCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.Stdin = nil
	configureProcessGroup(cmd)
}
