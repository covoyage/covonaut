package tools

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestIsolateCommandDetachesStdinAndProcessGroup(t *testing.T) {
	cmd := exec.Command("true")
	IsolateCommand(cmd)
	if cmd.Stdin != nil {
		t.Fatal("stdin should be nil so Run uses /dev/null")
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("expected process isolation attributes")
	}
}

func TestIsolateCommandKeepsChildOffTTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tty check is unix-only")
	}
	cmd := exec.Command("sh", "-c", "test ! -t 0 && echo detached")
	IsolateCommand(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(out) != "detached\n" {
		t.Fatalf("got %q", out)
	}
}
