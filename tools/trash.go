package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const trashForceMVEnv = "COVO_TRASH_FORCE_MV"
const trashDirEnv = "COVO_TRASH_DIR"

// TrashPath moves path into the platform trash instead of unlinking it.
// It never falls back to permanent deletion.
func TrashPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if err := rejectDangerousTrashTarget(abs, info); err != nil {
		return err
	}
	if override := strings.TrimSpace(os.Getenv(trashDirEnv)); override != "" {
		return moveToDir(abs, override)
	}
	return trashPathPlatform(abs, info)
}

func rejectDangerousTrashTarget(abs string, info os.FileInfo) error {
	if abs == string(filepath.Separator) || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return fmt.Errorf("refusing to trash filesystem root %s", abs)
	}
	if cwd, err := os.Getwd(); err == nil {
		if cwdAbs, err := filepath.Abs(cwd); err == nil && abs == cwdAbs {
			return fmt.Errorf("refusing to trash the current working directory")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if homeAbs, err := filepath.Abs(home); err == nil && abs == homeAbs {
			return fmt.Errorf("refusing to trash the home directory")
		}
	}
	_ = info
	return nil
}

func moveToDir(abs, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create trash dir: %w", err)
	}
	dest, err := uniqueDest(destDir, filepath.Base(abs))
	if err != nil {
		return err
	}
	if err := os.Rename(abs, dest); err != nil {
		return fmt.Errorf("move to trash: %w", err)
	}
	return nil
}

func uniqueDest(dir, base string) (string, error) {
	dest := filepath.Join(dir, base)
	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		return dest, nil
	} else if err != nil {
		return "", err
	}
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	stamp := time.Now().Format("20060102-150405")
	for i := 0; i < 100; i++ {
		candidate := fmt.Sprintf("%s %s%s", name, stamp, ext)
		if i > 0 {
			candidate = fmt.Sprintf("%s %s-%d%s", name, stamp, i, ext)
		}
		dest = filepath.Join(dir, candidate)
		if _, err := os.Lstat(dest); os.IsNotExist(err) {
			return dest, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate a unique trash name for %s", base)
}

func forceTrashMove() bool {
	v := strings.TrimSpace(os.Getenv(trashForceMVEnv))
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
