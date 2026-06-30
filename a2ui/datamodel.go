package a2ui

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePointer splits an A2UI data-binding path into its decoded reference
// tokens. It accepts standard RFC 6901 JSON Pointers (leading "/") as well as
// the relative paths A2UI permits inside collection scopes (no leading "/").
// The root pointer ("" or "/") yields a nil token slice.
func ParsePointer(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	trimmed := strings.TrimPrefix(path, "/")
	parts := strings.Split(trimmed, "/")
	for i, p := range parts {
		parts[i] = unescapeToken(p)
	}
	return parts
}

// unescapeToken decodes the RFC 6901 escape sequences ~1 -> "/" and ~0 -> "~".
func unescapeToken(t string) string {
	t = strings.ReplaceAll(t, "~1", "/")
	t = strings.ReplaceAll(t, "~0", "~")
	return t
}

// escapeToken encodes a reference token per RFC 6901.
func escapeToken(t string) string {
	t = strings.ReplaceAll(t, "~", "~0")
	t = strings.ReplaceAll(t, "/", "~1")
	return t
}

// JoinPointer builds an absolute JSON Pointer from decoded reference tokens.
func JoinPointer(tokens ...string) string {
	if len(tokens) == 0 {
		return "/"
	}
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(escapeToken(t))
	}
	return b.String()
}

// GetData resolves a data-binding path against a data model and returns the
// value found there. The second result reports whether the path resolved.
func GetData(model any, path string) (any, bool) {
	cur := model
	for _, tok := range ParsePointer(path) {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[tok]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, _, err := arrayIndex(tok, len(node))
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// ApplyUpdate applies an updateDataModel operation to a data model, returning
// the resulting model (which may be a brand-new root). When hasValue is false
// the key at path is removed. The empty/root path replaces (or clears) the
// entire model. Intermediate map nodes are created as needed.
func ApplyUpdate(model any, path string, value any, hasValue bool) (any, error) {
	tokens := ParsePointer(path)
	if len(tokens) == 0 {
		if !hasValue {
			return nil, nil
		}
		return value, nil
	}
	if model == nil {
		model = map[string]any{}
	}
	return applyTokens(model, tokens, value, hasValue)
}

func applyTokens(node any, tokens []string, value any, hasValue bool) (any, error) {
	key := tokens[0]
	last := len(tokens) == 1

	switch n := node.(type) {
	case map[string]any:
		if last {
			if hasValue {
				n[key] = value
			} else {
				delete(n, key)
			}
			return n, nil
		}
		child, ok := n[key]
		if !ok || child == nil {
			child = map[string]any{}
		}
		updated, err := applyTokens(child, tokens[1:], value, hasValue)
		if err != nil {
			return nil, err
		}
		n[key] = updated
		return n, nil

	case []any:
		idx, isAppend, err := arrayIndex(key, len(n))
		if err != nil {
			return nil, err
		}
		if last {
			if isAppend {
				if hasValue {
					n = append(n, value)
				}
				return n, nil
			}
			if idx < 0 || idx >= len(n) {
				return nil, fmt.Errorf("a2ui: array index %q out of range", key)
			}
			if hasValue {
				n[idx] = value
			} else {
				// Per spec: removing an array element sets it to undefined,
				// preserving length.
				n[idx] = nil
			}
			return n, nil
		}
		if isAppend || idx < 0 || idx >= len(n) {
			return nil, fmt.Errorf("a2ui: cannot descend into array index %q", key)
		}
		child := n[idx]
		if child == nil {
			child = map[string]any{}
		}
		updated, err := applyTokens(child, tokens[1:], value, hasValue)
		if err != nil {
			return nil, err
		}
		n[idx] = updated
		return n, nil

	default:
		// Scalar (or nil) where a container is required: replace with a map so
		// the path can be created.
		return applyTokens(map[string]any{}, tokens, value, hasValue)
	}
}

// arrayIndex parses an array reference token. The special token "-" addresses
// the position one past the end (append). It returns the index, whether the
// token was the append marker, and any parse error.
func arrayIndex(tok string, length int) (idx int, isAppend bool, err error) {
	if tok == "-" {
		return length, true, nil
	}
	i, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false, fmt.Errorf("a2ui: invalid array index %q", tok)
	}
	return i, false, nil
}
