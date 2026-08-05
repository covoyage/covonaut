//go:build windows

package tools

import (
	"os"
	"os/exec"
)

func newShellCommand(command string) *exec.Cmd {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return exec.Command(shell, "/D", "/S", "/C", command)
}
