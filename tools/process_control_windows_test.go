//go:build windows

package tools

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTerminateProcessTreeUsesTaskkill(t *testing.T) {
	previous := runTaskkill
	t.Cleanup(func() { runTaskkill = previous })
	calledWith := 0
	runTaskkill = func(pid int) error {
		calledWith = pid
		return nil
	}
	if err := terminateProcessTree(42); err != nil {
		t.Fatal(err)
	}
	if calledWith != 42 {
		t.Fatalf("taskkill pid = %d", calledWith)
	}
}

func TestTerminateProcessTreeReportsFallback(t *testing.T) {
	previous := runTaskkill
	previousKill := killOSProcess
	t.Cleanup(func() { runTaskkill = previous; killOSProcess = previousKill })
	runTaskkill = func(int) error { return errors.New("unavailable") }
	killOSProcess = func(*os.Process) error { return errors.New("kill failed") }
	err := terminateProcessTree(-1)
	if err == nil || !strings.Contains(err.Error(), "taskkill failed") {
		t.Fatalf("fallback error = %v", err)
	}
}

func TestTerminateProcessTreeAcceptsDirectKillFallback(t *testing.T) {
	previous := runTaskkill
	previousKill := killOSProcess
	t.Cleanup(func() { runTaskkill = previous; killOSProcess = previousKill })
	runTaskkill = func(int) error { return errors.New("unavailable") }
	killOSProcess = func(*os.Process) error { return nil }
	if err := terminateProcessTree(42); err != nil {
		t.Fatalf("direct kill fallback = %v", err)
	}
}
