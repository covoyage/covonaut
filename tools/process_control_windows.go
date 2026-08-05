//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

var runTaskkill = func(pid int) error {
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

var killOSProcess = func(process *os.Process) error { return process.Kill() }

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func terminateProcessTree(pid int) error {
	taskkillErr := runTaskkill(pid)
	if taskkillErr == nil {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("taskkill failed: %v; find process: %w", taskkillErr, err)
	}
	if err := killOSProcess(process); err != nil {
		return fmt.Errorf("taskkill failed: %v; kill process: %w", taskkillErr, err)
	}
	return nil
}
