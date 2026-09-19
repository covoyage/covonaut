package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var errNotRecoverableDelete = errors.New("not a recoverable delete")

var recoverableDeleteNames = map[string]bool{
	"rm":          true,
	"unlink":      true,
	"rmdir":       true,
	"del":         true,
	"erase":       true,
	"rd":          true,
	"remove":      true,
	"ri":          true,
	"remove-item": true,
}

var irrecoverableDeleteNames = map[string]bool{
	"shred": true,
}

// maybeTrashShellCommand intercepts a top-level recoverable delete command
// (rm / unlink / rmdir / Remove-Item) and moves the literal targets to trash.
// Compound commands, dynamic paths, and unknown syntax fall through.
func maybeTrashShellCommand(command, cwd string) error {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return errNotRecoverableDelete
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(trimmed), "")
	if err != nil {
		return errNotRecoverableDelete
	}
	if len(file.Stmts) != 1 {
		return errNotRecoverableDelete
	}
	stmt := file.Stmts[0]
	if stmt.Negated || stmt.Background || stmt.Coprocess || len(stmt.Redirs) > 0 {
		return errNotRecoverableDelete
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return errNotRecoverableDelete
	}

	words := make([]shellWord, len(call.Args))
	for i, word := range call.Args {
		words[i].value, words[i].static = staticShellWord(word)
		if !words[i].static {
			return errNotRecoverableDelete
		}
	}
	name := strings.ToLower(filepath.Base(words[0].value))
	if irrecoverableDeleteNames[name] {
		return fmt.Errorf("irrecoverable delete command %q is blocked; use rm so files go to trash", words[0].value)
	}
	if !recoverableDeleteNames[name] {
		return errNotRecoverableDelete
	}

	targets, force, err := parseDeleteArgs(words[1:])
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		if force {
			return nil
		}
		return fmt.Errorf("rm requires at least one path")
	}

	var missing []string
	resolved := make([]string, 0, len(targets))
	for _, target := range targets {
		path := resolvePath(target, cwd)
		if _, err := os.Lstat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, target)
				continue
			}
			return err
		}
		resolved = append(resolved, path)
	}
	if len(missing) > 0 && !force {
		return fmt.Errorf("path not found: %s", strings.Join(missing, ", "))
	}
	for _, path := range resolved {
		if err := TrashPath(path); err != nil {
			return err
		}
	}
	return nil
}

func parseDeleteArgs(args []shellWord) (targets []string, force bool, err error) {
	endFlags := false
	for _, arg := range args {
		value := arg.value
		if !endFlags && strings.HasPrefix(value, "-") && value != "-" {
			if value == "--" {
				endFlags = true
				continue
			}
			if strings.HasPrefix(value, "--") {
				switch value {
				case "--force":
					force = true
				case "--recursive", "--recursive=", "--dir", "--directory", "--verbose":
					// Compatibility flags. Trashing a directory already moves the tree.
				case "--help", "--version":
					return nil, false, errNotRecoverableDelete
				default:
					return nil, false, fmt.Errorf("unsupported rm flag %q; use a top-level rm -- <path>", value)
				}
				continue
			}
			for _, r := range value[1:] {
				switch r {
				case 'f':
					force = true
				case 'r', 'R', 'd', 'v':
					// Compatibility flags.
				default:
					return nil, false, fmt.Errorf("unsupported rm flag %q; use a top-level rm -- <path>", value)
				}
			}
			continue
		}
		targets = append(targets, value)
	}
	return targets, force, nil
}
