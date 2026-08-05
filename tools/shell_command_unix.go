//go:build !windows

package tools

import (
	"os"
	"os/exec"
)

func newShellCommand(command string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return exec.Command(shell, "-c", command)
}
