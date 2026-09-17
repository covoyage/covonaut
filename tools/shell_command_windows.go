//go:build windows

package tools

import (
	"os"
	"os/exec"
	"sync"
)

// resolvePowerShell finds a native PowerShell executable once: pwsh
// (PowerShell 7+) first, then the built-in Windows PowerShell 5.1.
var (
	shellPwshOnce sync.Once
	shellPwshPath string
)

func resolvePowerShell() string {
	shellPwshOnce.Do(func() {
		for _, name := range []string{"pwsh", "powershell"} {
			if p, err := exec.LookPath(name); err == nil {
				shellPwshPath = p
				return
			}
		}
	})
	return shellPwshPath
}

// newShellCommand prefers native PowerShell over cmd.exe: modern Windows
// setups standardize on PowerShell, and POSIX-flavored commands the model
// emits (ls, rm, cat, env vars) map to PowerShell far better than to
// cmd.exe. Falls back to COMSPEC/cmd.exe when no PowerShell is installed.
func newShellCommand(command string) *exec.Cmd {
	if ps := resolvePowerShell(); ps != "" {
		return exec.Command(ps, "-NoProfile", "-NoLogo", "-NonInteractive", "-Command", command)
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return exec.Command(shell, "/D", "/S", "/C", command)
}
