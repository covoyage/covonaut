//go:build darwin

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func trashPathPlatform(abs string, info os.FileInfo) error {
	_ = info
	if !forceTrashMove() {
		if err := finderTrash(abs); err == nil {
			return nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}
	return moveToDir(abs, filepath.Join(home, ".Trash"))
}

func finderTrash(abs string) error {
	script := fmt.Sprintf(`tell application "Finder" to delete POSIX file %s`, applescriptString(abs))
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("finder trash: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func applescriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
