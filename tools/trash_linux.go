//go:build linux

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func trashPathPlatform(abs string, info os.FileInfo) error {
	_ = info
	if !forceTrashMove() {
		if err := gioTrash(abs); err == nil {
			return nil
		}
	}
	return xdgTrash(abs)
}

func gioTrash(abs string) error {
	gio, err := exec.LookPath("gio")
	if err != nil {
		return err
	}
	cmd := exec.Command(gio, "trash", abs)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gio trash: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func xdgTrash(abs string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	filesDir := filepath.Join(dataHome, "Trash", "files")
	infoDir := filepath.Join(dataHome, "Trash", "info")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return fmt.Errorf("create trash files dir: %w", err)
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return fmt.Errorf("create trash info dir: %w", err)
	}
	dest, err := uniqueDest(filesDir, filepath.Base(abs))
	if err != nil {
		return err
	}
	infoPath := filepath.Join(infoDir, filepath.Base(dest)+".trashinfo")
	payload := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		escapeTrashPath(abs), time.Now().Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(infoPath, []byte(payload), 0o600); err != nil {
		return fmt.Errorf("write trashinfo: %w", err)
	}
	if err := os.Rename(abs, dest); err != nil {
		_ = os.Remove(infoPath)
		return fmt.Errorf("move to trash: %w", err)
	}
	return nil
}

func escapeTrashPath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r <= 0x20 || r == '%' {
			fmt.Fprintf(&b, "%%%02X", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
