//go:build !windows

package tools

import "testing"

func TestNewShellCommandUnix(t *testing.T) {
	t.Setenv("SHELL", "/bin/test-shell")
	cmd := newShellCommand("printf ok")
	if cmd.Path != "/bin/test-shell" {
		t.Fatalf("shell path = %q", cmd.Path)
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "-c" || cmd.Args[2] != "printf ok" {
		t.Fatalf("shell args = %q", cmd.Args)
	}
}
