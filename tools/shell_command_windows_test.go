//go:build windows

package tools

import "testing"

func TestNewShellCommandWindows(t *testing.T) {
	t.Setenv("COMSPEC", `C:\Windows\System32\cmd.exe`)
	cmd := newShellCommand("echo ok")
	if cmd.Path != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("shell path = %q", cmd.Path)
	}
	want := []string{`C:\Windows\System32\cmd.exe`, "/D", "/S", "/C", "echo ok"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("shell args = %q", cmd.Args)
	}
	for index := range want {
		if cmd.Args[index] != want[index] {
			t.Fatalf("shell args = %q", cmd.Args)
		}
	}
}
