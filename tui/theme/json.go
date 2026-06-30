package theme

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// themeJSONFile matches the subset of pi-mono coding-agent theme JSON we support.
type themeJSONFile struct {
	Name   string         `json:"name"`
	Vars   map[string]any `json:"vars"`
	Colors map[string]any `json:"colors"`
}

// ParseSemanticThemeJSON parses a pi-compatible theme JSON document into SemanticTheme.
// Unknown keys are ignored; missing colors inherit from base (typically DefaultSemanticDark).
func ParseSemanticThemeJSON(data []byte, base *SemanticTheme) (*SemanticTheme, error) {
	var raw themeJSONFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if base == nil {
		base = DefaultSemanticDark()
	}
	out := *base
	if raw.Name != "" {
		out.Name = raw.Name
	}

	vars := map[string]string{}
	for k, v := range raw.Vars {
		s, err := anyToColorString(v)
		if err != nil {
			return nil, fmt.Errorf("vars.%s: %w", k, err)
		}
		vars[k] = s
	}

	resolve := func(v any) (string, error) {
		s, err := anyToColorString(v)
		if err != nil {
			return "", err
		}
		return resolveThemeColorRef(s, vars, map[string]bool{}), nil
	}

	for key, val := range raw.Colors {
		col, err := resolve(val)
		if err != nil {
			return nil, fmt.Errorf("colors.%s: %w", key, err)
		}
		if col == "" {
			continue
		}
		applyColorKey(&out, key, col)
	}
	return &out, nil
}

func anyToColorString(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t), nil
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return "", fmt.Errorf("unsupported number %v", t)
	case json.Number:
		i, err := t.Int64()
		if err == nil {
			return strconv.FormatInt(i, 10), nil
		}
		return t.String(), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unsupported type %T", v)
	}
}

func resolveThemeColorRef(s string, vars map[string]string, seen map[string]bool) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return s
	}
	if _, err := strconv.ParseInt(s, 10, 32); err == nil {
		return s
	}
	if seen[s] {
		return ""
	}
	seen[s] = true
	if v, ok := vars[s]; ok {
		return resolveThemeColorRef(v, vars, seen)
	}
	return ""
}

func applyColorKey(t *SemanticTheme, key, col string) {
	switch key {
	case "accent":
		t.Accent = col
	case "border":
		t.Border = col
	case "borderAccent":
		t.BorderAccent = col
	case "borderMuted":
		t.BorderMuted = col
	case "success":
		t.Success = col
	case "error":
		t.Error = col
	case "warning":
		t.Warning = col
	case "muted":
		t.Muted = col
	case "dim":
		t.Dim = col
	case "text":
		t.Text = col
	case "thinkingText":
		t.ThinkingText = col
	case "userMessageText":
		t.UserMessage = col
	case "selectedBg":
		t.SelectedBg = col
	case "userMessageBg":
		t.UserMessageBg = col
	case "toolPendingBg":
		t.ToolPendingBg = col
	case "toolSuccessBg":
		t.ToolSuccessBg = col
	case "toolErrorBg":
		t.ToolErrorBg = col
	case "mdHeading":
		t.MdHeading = col
	case "mdLink":
		t.MdLink = col
	case "mdLinkUrl":
		t.MdLinkUrl = col
	case "mdCode":
		t.MdCode = col
	case "mdCodeBlock":
		t.MdCodeBlock = col
	case "mdCodeBlockBorder":
		t.MdCodeBlockBorder = col
	case "mdQuote":
		t.MdQuote = col
	case "mdQuoteBorder":
		t.MdQuoteBorder = col
	case "mdHr":
		t.MdHr = col
	case "mdListBullet":
		t.MdListBullet = col
	case "syntaxComment":
		t.SyntaxComment = col
	case "syntaxKeyword":
		t.SyntaxKeyword = col
	case "syntaxFunction":
		t.SyntaxFunction = col
	case "syntaxVariable":
		t.SyntaxVariable = col
	case "syntaxString":
		t.SyntaxString = col
	case "syntaxNumber":
		t.SyntaxNumber = col
	case "syntaxType":
		t.SyntaxType = col
	case "syntaxOperator":
		t.SyntaxOperator = col
	case "syntaxPunctuation":
		t.SyntaxPunctuation = col
	}
}
