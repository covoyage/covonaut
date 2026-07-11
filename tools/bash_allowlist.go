package tools

import (
	"fmt"
	"strings"
)

// commandAllowed checks a shell command against an allow-list and a
// deny-list. The command is split on shell separators (&&, ||, ;, |) and
// each sub-command is checked independently.
//
// Matching uses prefix comparison with a word boundary: "git" matches
// "git" and "git status" but not "github". List entries may contain
// spaces (e.g. "rm -rf", "go test").
//
// Deny-list is evaluated first and wins over allow-list: a blocked
// command is rejected even if it also matches an allow-list entry.
//
// Limitations: separators inside quotes are not respected (the command is
// split naively). For strong isolation use a sandboxed BashOperations
// backend instead of relying on the allow-list alone.
func commandAllowed(cmd string, allow, block []string) error {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return nil
	}
	for _, sub := range splitSubcommands(trimmed) {
		s := strings.TrimSpace(sub)
		if s == "" {
			continue
		}
		for _, b := range block {
			if matchesCommandPrefix(s, b) {
				return fmt.Errorf("command blocked by deny-list: %q", b)
			}
		}
		if len(allow) > 0 {
			ok := false
			for _, a := range allow {
				if matchesCommandPrefix(s, a) {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("command not in allow-list: %q", firstToken(s))
			}
		}
	}
	return nil
}

// splitSubcommands splits a command string on shell separators. It does
// not parse quotes — separators inside double-quoted strings are still
// treated as separators. This is a documented limitation.
func splitSubcommands(cmd string) []string {
	r := strings.NewReplacer("&&", "\x00", "||", "\x00", ";", "\x00", "|", "\x00")
	return strings.Split(r.Replace(cmd), "\x00")
}

// matchesCommandPrefix reports whether cmd is exactly prefix or starts
// with prefix followed by a space (a word boundary), so "git" does not
// match "github".
func matchesCommandPrefix(cmd, prefix string) bool {
	if cmd == prefix {
		return true
	}
	return strings.HasPrefix(cmd, prefix+" ")
}

func firstToken(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}
