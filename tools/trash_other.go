//go:build !darwin && !linux && !windows

package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

func trashPathPlatform(abs string, info os.FileInfo) error {
	_ = info
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}
	return moveToDir(abs, filepath.Join(home, ".Trash"))
}
