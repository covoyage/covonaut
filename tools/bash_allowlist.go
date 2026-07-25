package tools

import (
	"fmt"
	"io"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// commandAllowed checks every command in a shell syntax tree against an
// allow-list and a deny-list, including commands nested in substitutions.
//
// Matching uses prefix comparison with a word boundary: "git" matches
// "git" and "git status" but not "github". List entries may contain
// spaces (e.g. "rm -rf", "go test").
//
// Deny-list is evaluated first and wins over allow-list: a blocked
// command is rejected even if it also matches an allow-list entry.
//
// Dynamic command names are rejected whenever filtering is enabled because
// their executable cannot be determined safely before shell expansion.
func commandAllowed(cmd string, allow, block []string) error {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" || len(allow) == 0 && len(block) == 0 {
		return nil
	}

	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(trimmed), "")
	if err != nil {
		return fmt.Errorf("invalid shell syntax: %w", err)
	}

	var checkErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		if checkErr != nil {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]shellWord, len(call.Args))
		for i, word := range call.Args {
			words[i].value, words[i].static = staticShellWord(word)
		}
		if !words[0].static {
			checkErr = fmt.Errorf("dynamic command name is not allowed")
			return false
		}
		for _, denied := range block {
			if commandWordsMatch(words, strings.Fields(denied), true) {
				checkErr = fmt.Errorf("command blocked by deny-list: %q", denied)
				return false
			}
		}
		if len(allow) > 0 {
			allowed := false
			for _, candidate := range allow {
				if commandWordsMatch(words, strings.Fields(candidate), false) {
					allowed = true
					break
				}
			}
			if !allowed {
				checkErr = fmt.Errorf("command not in allow-list: %q", words[0].value)
				return false
			}
		}
		return true
	})
	return checkErr
}

type shellWord struct {
	value  string
	static bool
}

func commandWordsMatch(command []shellWord, prefix []string, conservativeDynamic bool) bool {
	if len(prefix) == 0 || len(command) < len(prefix) {
		return false
	}
	for i, expected := range prefix {
		if !command[i].static {
			return conservativeDynamic && i > 0
		}
		if command[i].value != expected {
			return false
		}
	}
	return true
}

func staticShellWord(word *syntax.Word) (string, bool) {
	var value strings.Builder
	for _, part := range word.Parts {
		if !writeStaticWordPart(&value, part) {
			return "", false
		}
	}
	return value.String(), true
}

func writeStaticWordPart(dst io.StringWriter, part syntax.WordPart) bool {
	switch part := part.(type) {
	case *syntax.Lit:
		_, _ = dst.WriteString(part.Value)
		return true
	case *syntax.SglQuoted:
		_, _ = dst.WriteString(part.Value)
		return true
	case *syntax.DblQuoted:
		for _, nested := range part.Parts {
			if !writeStaticWordPart(dst, nested) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
