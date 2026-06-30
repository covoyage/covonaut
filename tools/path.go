package tools

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// resolvePath resolves a user-provided path against the working directory.
// Handles ~ expansion, absolute paths, and empty strings.
func resolvePath(userPath, cwd string) string {
	if userPath == "" {
		return cwd
	}

	// Expand ~ to home directory.
	if userPath == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return userPath
	}
	if strings.HasPrefix(userPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, userPath[2:])
		}
	}

	// If already absolute, return as-is.
	if filepath.IsAbs(userPath) {
		return userPath
	}

	// Resolve relative to cwd.
	return filepath.Join(cwd, userPath)
}

// resolveReadPath resolves a path and handles macOS NFD normalization.
// macOS stores filenames in NFD (decomposed) form. If a user-supplied
// path is in NFC (composed) form, the file won't be found by the OS.
func resolveReadPath(userPath, cwd string) string {
	resolved := resolvePath(userPath, cwd)

	if _, err := os.Stat(resolved); err == nil {
		return resolved
	}

	// Only attempt NFD normalization if the original path contains
	// characters that differ between NFC and NFD forms.
	nfdPath := norm.NFD.String(resolved)
	if nfdPath != resolved {
		if _, err := os.Stat(nfdPath); err == nil {
			return nfdPath
		}
	}

	return resolved
}
