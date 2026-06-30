package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type cuBackend string

const (
	cuBackendCua        cuBackend = "cua-driver"
	cuBackendCliclick   cuBackend = "cliclick"
	cuBackendOSA        cuBackend = "osascript"
	cuBackendPowerShell cuBackend = "powershell"
	cuBackendXDoTool    cuBackend = "xdotool"
	cuBackendYDoTool    cuBackend = "ydotool"
)

var (
	cuBackendCache cuBackend
	cuBackendOnce  sync.Once
)

func isWayland() bool {
	return os.Getenv("WAYLAND_DISPLAY") != ""
}

func detectCUABackend() cuBackend {
	cuBackendOnce.Do(func() {
		switch runtime.GOOS {
		case "darwin":
			if _, err := exec.LookPath("cua-driver"); err == nil {
				cuBackendCache = cuBackendCua
			} else if _, err := exec.LookPath("cliclick"); err == nil {
				cuBackendCache = cuBackendCliclick
			} else {
				cuBackendCache = cuBackendOSA
			}
		case "windows":
			cuBackendCache = cuBackendPowerShell
		case "linux":
			if isWayland() {
				if _, err := exec.LookPath("ydotool"); err == nil {
					cuBackendCache = cuBackendYDoTool
				} else {
					cuBackendCache = cuBackendYDoTool
				}
			} else if _, err := exec.LookPath("xdotool"); err == nil {
				cuBackendCache = cuBackendXDoTool
			} else {
				cuBackendCache = cuBackendXDoTool
			}
		default:
			cuBackendCache = cuBackendOSA
		}
	})
	return cuBackendCache
}

var (
	cuaDriverClient   *mcpClient
	cuaDriverClientMu sync.Mutex
)

func getCuaDriverClient(ctx context.Context) (*mcpClient, error) {
	cuaDriverClientMu.Lock()
	defer cuaDriverClientMu.Unlock()
	if cuaDriverClient != nil {
		return cuaDriverClient, nil
	}
	client, err := newMCPClient(ctx, "cua-driver", "mcp")
	if err != nil {
		return nil, fmt.Errorf("start cua-driver: %w", err)
	}
	cuaDriverClient = client
	return client, nil
}

func closeCuaDriver() {
	cuaDriverClientMu.Lock()
	defer cuaDriverClientMu.Unlock()
	if cuaDriverClient != nil {
		cuaDriverClient.Close()
		cuaDriverClient = nil
	}
}

// --- Dangerous operation blocking ---

var blockedKeyCombos = []struct {
	keys     string
	reason   string
	platform string // "darwin", "windows", "linux", or "" for all
}{
	// macOS-specific
	{"cmd+shift+backspace", "Empty Trash — blocked for safety", "darwin"},
	{"cmd+option+backspace", "Force Delete — blocked for safety", "darwin"},
	{"cmd+ctrl+q", "Lock Screen — blocked for safety", "darwin"},
	{"cmd+shift+q", "Log Out — blocked for safety", "darwin"},
	{"cmd+option+shift+q", "Force Log Out — blocked for safety", "darwin"},

	// Windows-specific
	{"super+r", "Run dialog — blocked for safety", "windows"},
	{"win+r", "Run dialog — blocked for safety", "windows"},
	{"ctrl+shift+esc", "Task Manager — blocked for safety", "windows"},
	{"ctrl+alt+del", "Security screen — blocked for safety", "windows"},
	{"alt+f4", "Close window (may lose work) — blocked for safety", "windows"},
	{"super+x", "Power menu — blocked for safety", "windows"},
	{"win+x", "Power menu — blocked for safety", "windows"},

	// Linux-specific
	{"ctrl+alt+del", "System reboot dialog — blocked for safety", "linux"},
	{"ctrl+alt+f2", "TTY switch — blocked for safety", "linux"},
	{"alt+f2", "Run dialog — blocked for safety", "linux"},
	{"alt+f4", "Close window (may lose work) — blocked for safety", "linux"},
	{"ctrl+alt+t", "Terminal — blocked for safety", "linux"},

	// Cross-platform shell execution patterns (checked by type pattern matcher)
}

var blockedTypePatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`(?i)curl\s+.*?\|\s*(ba|z|k)?sh\b`), "Piped curl-to-shell execution — blocked for safety"},
	{regexp.MustCompile(`(?i)wget\s+.*?\|\s*(ba|z|k)?sh\b`), "Piped wget-to-shell execution — blocked for safety"},
	{regexp.MustCompile(`(?i)sudo\s+rm\s+-[rfv]+\s+`), "Destructive rm — blocked for safety"},
	{regexp.MustCompile(`(?i)rm\s+-[rfv]+\s+/`), "Root filesystem deletion — blocked for safety"},
	{regexp.MustCompile(`:\(\s*\{[^}]*:[^}]*\}\s*;?\s*:`), "Fork bomb — blocked for safety"},
}

func checkBlockedTypePattern(text string) error {
	for _, bp := range blockedTypePatterns {
		if bp.re.MatchString(text) {
			return fmt.Errorf("BLOCKED: %s", bp.reason)
		}
	}
	return nil
}

func checkBlockedKeyCombo(keys string) error {
	normalized := strings.ToLower(strings.TrimSpace(keys))
	for _, bk := range blockedKeyCombos {
		if bk.platform != "" && bk.platform != runtime.GOOS {
			continue
		}
		if strings.ReplaceAll(normalized, " ", "") == strings.ReplaceAll(bk.keys, " ", "") {
			return fmt.Errorf("BLOCKED: %s", bk.reason)
		}
		parts := strings.Split(bk.keys, "+")
		userParts := strings.Split(normalized, "+")
		if len(parts) == len(userParts) {
			match := true
			for _, up := range userParts {
				up = strings.TrimSpace(up)
				found := false
				for _, bp := range parts {
					bp = strings.TrimSpace(bp)
					if up == bp {
						found = true
						break
					}
				}
				if !found {
					match = false
					break
				}
			}
			if match {
				return fmt.Errorf("BLOCKED: %s", bk.reason)
			}
		}
	}
	return nil
}

// --- Approval system ---

type approvalLevel int
const (
	approvalNone approvalLevel = iota
	approvalOnce
	approvalSession
)

var (
	approvalMode   approvalLevel
	approvalSeen   map[string]bool
	approvalMu     sync.Mutex
)

func initApprovalMode() {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("COMPUTER_USE_APPROVAL")))
	switch mode {
	case "once":
		approvalMode = approvalOnce
	case "session":
		approvalMode = approvalSession
	case "none", "":
		approvalMode = approvalNone
	default:
		approvalMode = approvalNone
	}
	approvalSeen = make(map[string]bool)
}

func isDestructiveAction(action string) bool {
	switch action {
	case "click", "double_click", "right_click", "middle_click", "drag",
		"type", "key", "scroll", "set_value", "focus_app":
		return true
	}
	return false
}

func (b cuBackend) String() string { return string(b) }

// --- Helper functions ---

func osaExec(script string) (string, error) {
	cmd := exec.Command("osascript", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("osascript: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func cliclickExec(args ...string) (string, error) {
	cmd := exec.Command("cliclick", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cliclick: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

var (
	pwshBinary string
	pwshOnce   sync.Once
)

func getPwshBinary() string {
	pwshOnce.Do(func() {
		if _, err := exec.LookPath("pwsh"); err == nil {
			pwshBinary = "pwsh"
		} else {
			pwshBinary = "powershell"
		}
	})
	return pwshBinary
}

func pwshExec(script string) (string, error) {
	binary := getPwshBinary()
	cmd := exec.Command(binary, "-NoProfile", "-NonInteractive", "-Command", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\nstderr: %s", binary, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func xdoExec(args ...string) (string, error) {
	cmd := exec.Command("xdotool", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xdotool: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func ydoExec(args ...string) (string, error) {
	cmd := exec.Command("ydotool", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ydotool: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func wtypeExec(text string) (string, error) {
	cmd := exec.Command("wtype", text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("wtype: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func scrotExec(path string) error {
	if isWayland() {
		if err := exec.Command("grim", path).Run(); err == nil {
			return nil
		}
		return fmt.Errorf("no Wayland screenshot tool found (try: apt install grim)")
	}
	if err := exec.Command("import", "-window", "root", path).Run(); err == nil {
		return nil
	}
	if err := exec.Command("gnome-screenshot", "-f", path).Run(); err == nil {
		return nil
	}
	if err := exec.Command("scrot", path).Run(); err == nil {
		return nil
	}
	return fmt.Errorf("no screenshot tool found (try: apt install imagemagick, gnome-screenshot, grim, or scrot)")
}

// Key name maps for cliclick and osa
var cliclickKeyNames = map[string]string{
	"return": "return", "enter": "return",
	"escape": "escape", "esc": "escape",
	"tab": "tab", "space": "space",
	"delete": "backspace", "backspace": "backspace",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end",
	"pageup": "pageup", "pagedown": "pagedown",
	"pgup": "pageup", "pgdn": "pagedown",
	"f1": "f1", "f2": "f2", "f3": "f3", "f4": "f4",
	"f5": "f5", "f6": "f6", "f7": "f7", "f8": "f8",
	"f9": "f9", "f10": "f10", "f11": "f11", "f12": "f12",
}

var cliclickModMap = map[string]string{
	"cmd": "cmd", "command": "cmd",
	"ctrl": "ctrl", "control": "ctrl",
	"alt": "alt", "option": "alt",
	"shift": "shift",
}

var osaKeyNames = map[string]string{
	"return": "return", "enter": "return",
	"escape": "escape", "esc": "escape",
	"tab": "tab", "space": "space",
	"delete": "delete", "backspace": "delete",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end",
	"pageup": "page up", "pagedown": "page down",
	"pgup": "page up", "pgdn": "page down",
}

var osaModMap = map[string]string{
	"cmd": "command down", "command": "command down",
	"ctrl": "control down", "control": "control down",
	"alt": "option down", "option": "option down",
	"shift": "shift down",
}

var unicodeKeyAliases = map[string]string{
	"⌘": "cmd",
	"⌥": "option",
	"⌃": "ctrl",
	"⇧": "shift",
	"␣": "space",
	"⏎": "return",
	"↩": "return",
	"⌤": "enter",
	"⌫": "backspace",
	"⌦": "delete",
	"⎋": "escape",
	"⇥": "tab",
	"⇞": "pageup",
	"⇟": "pagedown",
	"↖": "home",
	"↘": "end",
	"↑": "up",
	"↓": "down",
	"←": "left",
	"→": "right",
}

func normalizeKeyString(s string) string {
	for unicode, ascii := range unicodeKeyAliases {
		s = strings.ReplaceAll(s, unicode, ascii)
	}
	return s
}

var cuaModMap = map[string]string{
	"cmd": "cmd", "command": "cmd",
	"ctrl": "ctrl", "control": "ctrl",
	"alt": "alt", "option": "alt",
	"shift": "shift",
}

// MCP types
type mcpContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type CuaWindow struct {
	PID      int    `json:"pid"`
	WindowID int    `json:"window_id"`
	Title    string `json:"title"`
	AppName  string `json:"app_name"`
	ZIndex   int    `json:"z_index"`
	OnScreen bool   `json:"on_screen"`
}

type ComputerUseToolConfig struct {
	DefaultClickWait int
}

func NewComputerUseTool(cfg *ComputerUseToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &ComputerUseToolConfig{}
	}
	clickWait := cfg.DefaultClickWait
	if clickWait <= 0 {
		clickWait = 300
	}
	initApprovalMode()

	actionEnum := []string{"capture", "info", "click", "double_click", "right_click", "middle_click", "drag", "type", "key", "scroll", "set_value", "wait", "list_apps", "focus_app"}

	return &agentcore.Tool{
		Name: "computer_use",
		Description: "Control the local desktop. Cross-platform: macOS (cua-driver/cliclick/osascript), " +
			"Windows (PowerShell), Linux (xdotool X11 / ydotool+wtype+grim Wayland). Backend auto-detected by platform. " +
			"cua-driver (macOS only) operates in background without focus stealing. " +
			"Install: brew install cua-driver (macOS) or apt install xdotool (Linux) or apt install ydotool wtype grim (Linux Wayland). " +
			"Actions: capture (screenshot + optional AX tree/SOM), info, click/double_click/right_click/middle_click, " +
			"drag, type, key (combos like cmd+s or ctrl+s), scroll, set_value, " +
			"wait, list_apps, focus_app. " +
			"Dangerous operations (empty trash, log out, rm -rf, ctrl+alt+del, etc.) are blocked per platform. " +
			"Destructive actions prompt for approval via COMPUTER_USE_APPROVAL env (once/session/none).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "The action to perform",
					"enum":        actionEnum,
				},
				"coordinate": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "Pixel coordinates [x, y] for click/double_click/right_click",
				},
				"from_coordinate": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "Start pixel [x, y] for drag",
				},
				"to_coordinate": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "End pixel [x, y] for drag",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Text to type (for action=type) or value to set (for action=set_value on dropdown/slider)",
				},
				"keys": map[string]any{
					"type":        "string",
					"description": "Key combination (for action=key) e.g. 'cmd+s', 'return', 'up', 'pagedown'",
				},
				"direction": map[string]any{
					"type":        "string",
					"description": "Scroll direction (for action=scroll). up/down/left/right",
					"enum":        []string{"up", "down", "left", "right"},
				},
				"amount": map[string]any{
					"type":        "integer",
					"description": "Scroll amount in ticks (for action=scroll, default 3, max 50)",
				},
				"seconds": map[string]any{
					"type":        "number",
					"description": "Seconds to wait (for action=wait, max 30)",
				},
				"app": map[string]any{
					"type":        "string",
					"description": "App name. For focus_app: set target. For capture: capture app window.",
				},
				"element": map[string]any{
					"type":        "integer",
					"description": "AX element index from capture(mode=ax) output (for click/set_value with cua-driver)",
				},
			"capture_mode": map[string]any{
				"type":        "string",
				"enum":        []string{"vision", "ax", "som"},
				"description": "'vision' (screenshot only, default), 'ax' (screenshot + AX accessibility tree with element IDs, cua-driver only), 'som' (screenshot with numbered element overlays, cua-driver only)",
			},
			"raise_window": map[string]any{
				"type":        "boolean",
				"description": "Whether to raise/bring window to front (for action=focus_app, default false)",
			},
			"capture_after": map[string]any{
				"type":        "boolean",
				"description": "Take a screenshot after the action and include it in the result",
			},
			"required": []any{"action"},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			switch runtime.GOOS {
			case "darwin", "windows", "linux":
			default:
				return nil, fmt.Errorf("computer_use is only supported on macOS, Windows, and Linux")
			}

		var input struct {
			Action          string    `json:"action"`
			Coordinate      []int     `json:"coordinate"`
			FromCoordinate  []int     `json:"from_coordinate"`
			ToCoordinate    []int     `json:"to_coordinate"`
			Text            string    `json:"text"`
			Keys            string    `json:"keys"`
			Direction       string    `json:"direction"`
			Amount          int       `json:"amount"`
			Seconds         float64   `json:"seconds"`
			App             string    `json:"app"`
			Element         int       `json:"element"`
			CaptureMode     string    `json:"capture_mode"`
			RaiseWindow     bool      `json:"raise_window"`
			CaptureAfter    bool      `json:"capture_after"`
		}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			backend := detectCUABackend()

			// Pre-flight safety checks
			if input.Action == "key" {
				input.Keys = normalizeKeyString(input.Keys)
				if err := checkBlockedKeyCombo(input.Keys); err != nil {
					return nil, err
				}
			}
			if input.Action == "type" || input.Action == "set_value" {
				if err := checkBlockedTypePattern(input.Text); err != nil {
					return nil, err
				}
			}

			// Approval for destructive actions
			if isDestructiveAction(input.Action) {
				approvalMu.Lock()
				if approvalMode == approvalOnce && approvalSeen[input.Action] {
					approvalMu.Unlock()
					return nil, fmt.Errorf("BLOCKED by approval mode (once). Set COMPUTER_USE_APPROVAL=session or none to allow more")
				}
				if approvalMode == approvalOnce || approvalMode == approvalSession {
					if !approvalSeen[input.Action] {
						approvalMu.Unlock()
						fmt.Fprintf(os.Stderr, "\n⚠️  COMPUTER_USE: %s — approve? [y/N/session/always] ", input.Action)
						var resp string
						fmt.Scanln(&resp)
						resp = strings.ToLower(strings.TrimSpace(resp))
						approvalMu.Lock()
						switch resp {
						case "y", "yes":
							// approve once (already tracked by the fact we don't mark session)
						case "session":
							approvalMode = approvalSession
						case "always":
							approvalMode = approvalNone
						default:
							approvalMu.Unlock()
							return nil, fmt.Errorf("DENIED by user")
						}
						approvalSeen[input.Action] = true
						approvalMu.Unlock()
					} else {
						approvalMu.Unlock()
					}
				} else {
					approvalMu.Unlock()
				}
			}

			var actionResult any
			var err error
			switch input.Action {
			case "capture":
				actionResult, err = cuaCapture(ctx, backend, input.App, input.CaptureMode)
			case "info":
				actionResult, err = cuaInfo(ctx, backend)
			case "click", "double_click", "right_click", "middle_click":
				if len(input.Coordinate) < 2 && input.Element <= 0 {
					return nil, fmt.Errorf("coordinate [x, y] or element required for action=%s", input.Action)
				}
				x, y := 0, 0
				if len(input.Coordinate) >= 2 {
					x, y = input.Coordinate[0], input.Coordinate[1]
				}
				msg, e := cuaClick(ctx, backend, input.Action, x, y, input.Element)
				err = e
				if err == nil {
					time.Sleep(time.Duration(clickWait) * time.Millisecond)
					actionResult, _ = result(msg, nil)
				}
			case "drag":
				if len(input.FromCoordinate) < 2 || len(input.ToCoordinate) < 2 {
					return nil, fmt.Errorf("from_coordinate and to_coordinate required for drag")
				}
				msg, e := cuaDrag(ctx, backend, input.FromCoordinate[0], input.FromCoordinate[1], input.ToCoordinate[0], input.ToCoordinate[1])
				err = e
				if err == nil {
					time.Sleep(time.Duration(clickWait) * time.Millisecond)
					actionResult, _ = result(msg, nil)
				}
			case "type":
				if input.Text == "" {
					return nil, fmt.Errorf("text required for action=type")
				}
				msg, e := cuaType(ctx, backend, input.Text)
				err = e
				if err == nil {
					time.Sleep(time.Duration(clickWait) * time.Millisecond)
					actionResult, _ = result(msg, nil)
				}
			case "key":
				if input.Keys == "" {
					return nil, fmt.Errorf("keys required for action=key")
				}
				msg, e := cuaKey(ctx, backend, input.Keys)
				err = e
				if err == nil {
					time.Sleep(time.Duration(clickWait) * time.Millisecond)
					actionResult, _ = result(msg, nil)
				}
			case "scroll":
				if input.Direction == "" {
					return nil, fmt.Errorf("direction required for action=scroll")
				}
				amount := input.Amount
				if amount <= 0 {
					amount = 3
				}
				if amount > 50 {
					amount = 50
				}
				msg, e := cuaScroll(ctx, backend, input.Direction, amount)
				err = e
				if err == nil {
					time.Sleep(time.Duration(clickWait) * time.Millisecond)
					actionResult, _ = result(msg, nil)
				}
			case "set_value":
				msg, e := cuaSetValue(ctx, backend, input.Element, input.Text)
				err = e
				if err == nil {
					time.Sleep(time.Duration(clickWait) * time.Millisecond)
					actionResult, _ = result(msg, nil)
				}
			case "wait":
				seconds := input.Seconds
				if seconds <= 0 {
					seconds = 1
				}
				if seconds > 30 {
					seconds = 30
				}
				time.Sleep(time.Duration(seconds * float64(time.Second)))
				actionResult, _ = result(fmt.Sprintf("Waited %.1f seconds", seconds), nil)
			case "list_apps":
				actionResult, err = cuaListApps(ctx, backend)
			case "focus_app":
				if input.App == "" {
					return nil, fmt.Errorf("app required for action=focus_app")
				}
				msg, e := cuaFocusApp(ctx, backend, input.App, input.RaiseWindow)
				err = e
				if err == nil {
					actionResult, _ = result(msg, nil)
				}
			default:
				return nil, fmt.Errorf("unknown action: %s", input.Action)
			}
			if err != nil {
				return nil, err
			}

			if input.CaptureAfter {
				capResult, capErr := cuaCapture(ctx, backend, input.App, "")
				if capErr == nil {
					if tr, ok := actionResult.(ToolResult); ok {
						if trDet, ok := tr.Details.(map[string]any); ok {
							if capTR, ok := capResult.(ToolResult); ok {
								if capDet, ok := capTR.Details.(map[string]any); ok {
									for k, v := range capDet {
										trDet["capture_after_"+k] = v
									}
								}
							}
						}
					}
				}
			}
			return actionResult, nil
		},
	}
}

// --- Dispatch ---

func cuaCapture(ctx context.Context, backend cuBackend, appName, mode string) (any, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverCapture(ctx, appName, mode)
	case cuBackendPowerShell:
		if mode == "som" {
			return winCaptureSOM(ctx, appName)
		}
		return winCapture(ctx, appName)
	case cuBackendXDoTool, cuBackendYDoTool:
		return xdoCapture(ctx, appName)
	default:
		return fallbackCapture(ctx, backend, appName, mode)
	}
}

func cuaInfo(ctx context.Context, backend cuBackend) (any, error) {
	switch backend {
	case cuBackendPowerShell:
		return winInfo()
	case cuBackendXDoTool:
		return xdoInfo()
	default:
		return fallbackInfo()
	}
}

// --- macOS fallback info ---

func fallbackInfo() (any, error) {
	info, err := osaExec(`tell application "System Events"
		set screenRes to bounds of window of desktop
		set cursorPos to position of mouse
		set frontApp to name of first process whose frontmost is true
		set allApps to name of every process whose background only is false
		set appCount to count of allApps
		return "Screen: " & (item 1 of screenRes as text) & "x" & (item 2 of screenRes as text) & ¬
			" | Cursor: (" & (item 1 of cursorPos as text) & ", " & (item 2 of cursorPos as text) & ")" & ¬
			" | Frontmost: " & frontApp & ¬
			" | Running apps: " & (appCount as text)
	end tell`)
	if err != nil {
		return nil, fmt.Errorf("screen info failed: %w", err)
	}
	return result(info+"\nBackend: osascript", nil)
}

func cuaClick(ctx context.Context, backend cuBackend, action string, x, y, element int) (string, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverClick(ctx, action, x, y, element)
	case cuBackendPowerShell:
		return winClick(action, x, y)
	case cuBackendXDoTool:
		return xdoClick(action, x, y)
	case cuBackendYDoTool:
		return ydoClick(action, x, y)
	default:
		if action == "middle_click" && backend == cuBackendCliclick {
			return fallbackClick(cuBackendOSA, action, x, y)
		}
		return fallbackClick(backend, action, x, y)
	}
}

func cuaDrag(ctx context.Context, backend cuBackend, x1, y1, x2, y2 int) (string, error) {
	switch backend {
	case cuBackendCua:
		return "", fmt.Errorf("drag is not supported by cua-driver backend. Use click + type + key or fallback backend")
	case cuBackendPowerShell:
		return winDrag(x1, y1, x2, y2)
	case cuBackendXDoTool, cuBackendYDoTool:
		return xdoDrag(x1, y1, x2, y2)
	default:
		return fallbackDrag(backend, x1, y1, x2, y2)
	}
}

func cuaType(ctx context.Context, backend cuBackend, text string) (string, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverType(ctx, text)
	case cuBackendPowerShell:
		return winType(text)
	case cuBackendXDoTool:
		return xdoType(text)
	case cuBackendYDoTool:
		return ydoType(text)
	default:
		return fallbackType(backend, text)
	}
}

func cuaKey(ctx context.Context, backend cuBackend, keys string) (string, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverKey(ctx, keys)
	case cuBackendPowerShell:
		return winKey(keys)
	case cuBackendXDoTool:
		return xdoKey(keys)
	case cuBackendYDoTool:
		return ydoKey(keys)
	default:
		return fallbackKey(backend, keys)
	}
}

func cuaScroll(ctx context.Context, backend cuBackend, direction string, amount int) (string, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverScroll(ctx, direction, amount)
	case cuBackendPowerShell:
		return winScroll(direction, amount)
	case cuBackendXDoTool, cuBackendYDoTool:
		return xdoScroll(direction, amount)
	default:
		return fallbackScroll(backend, direction, amount)
	}
}

func cuaSetValue(ctx context.Context, backend cuBackend, element int, value string) (string, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverSetValue(ctx, element, value)
	case cuBackendPowerShell:
		return winSetValue(value)
	case cuBackendXDoTool, cuBackendYDoTool:
		return xdoSetValue(value)
	default:
		return fallbackSetValue(backend, value)
	}
}

func cuaListApps(ctx context.Context, backend cuBackend) (string, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverListApps(ctx)
	case cuBackendPowerShell:
		return winListApps()
	case cuBackendXDoTool, cuBackendYDoTool:
		return xdoListApps()
	default:
		return fallbackListApps()
	}
}

func cuaFocusApp(ctx context.Context, backend cuBackend, app string, raiseWindow bool) (string, error) {
	switch backend {
	case cuBackendCua:
		return cuaDriverFocusApp(ctx, app, raiseWindow)
	case cuBackendPowerShell:
		return winFocusApp(app, raiseWindow)
	case cuBackendXDoTool, cuBackendYDoTool:
		return xdoFocusApp(app, raiseWindow)
	default:
		return fallbackFocusApp(app, raiseWindow)
	}
}

// --- cua-driver implementations ---

func cuaDriverCapture(ctx context.Context, appName, mode string) (any, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return nil, err
	}

	raw, err := client.callTool(ctx, "list_windows", map[string]interface{}{
		"on_screen_only": true,
	})
	if err != nil {
		return nil, fmt.Errorf("list_windows: %w", err)
	}

	windows := parseCuaWindows(raw)
	if len(windows) == 0 {
		return nil, fmt.Errorf("no on-screen windows found")
	}

	var target *CuaWindow
	if appName != "" {
		appLower := strings.ToLower(appName)
		for i, w := range windows {
			if w.OnScreen && strings.Contains(strings.ToLower(w.AppName), appLower) {
				target = &windows[i]
				break
			}
		}
		if target == nil {
			return nil, fmt.Errorf("no window found for app: %s", appName)
		}
	} else {
		var best *CuaWindow
		for i, w := range windows {
			if !w.OnScreen {
				continue
			}
			if best == nil || w.ZIndex < best.ZIndex {
				best = &windows[i]
			}
		}
		if best == nil {
			return nil, fmt.Errorf("no on-screen window available")
		}
		target = best
	}

	if mode == "ax" || mode == "som" {
		if mode == "som" && runtime.GOOS != "darwin" {
			return nil, fmt.Errorf("SOM mode is only supported on macOS with cua-driver. Use mode=vision or mode=ax on other platforms")
		}
		raw, err := client.callTool(ctx, "get_window_state", map[string]interface{}{
			"pid":       target.PID,
			"window_id": target.WindowID,
		})
		if err != nil {
			return nil, fmt.Errorf("get_window_state: %w", err)
		}

		var stateResult struct {
			Content []mcpContentPart `json:"content"`
			IsError bool             `json:"isError"`
		}
		if json.Unmarshal(raw, &stateResult) != nil {
			return nil, fmt.Errorf("parse get_window_state result")
		}

		var axTree, imageData string
		for _, part := range stateResult.Content {
			if part.Type == "text" {
				axTree = part.Text
			} else if strings.HasPrefix(part.Type, "image") || strings.HasPrefix(part.Type, "resource") {
				imageData = part.Data
			}
		}

		details := map[string]any{
			"app":       target.AppName,
			"title":     target.Title,
			"window_id": target.WindowID,
		}
		output := fmt.Sprintf("Window: %s | %s\n", target.AppName, target.Title)

		if mode == "som" && imageData != "" && axTree != "" {
			annotated, elements, err := renderSOMOverlay(imageData, axTree)
			if err == nil {
				details["image_base64"] = annotated
				details["format"] = "jpeg"
				details["size_bytes"] = len(annotated) * 3 / 4
				details["som"] = true
				output += "\nSOM Elements:\n"
				for _, el := range elements {
					label := el.Label
					if label != "" {
						label = " " + label
					}
					output += fmt.Sprintf("  [%d] pos=(%d,%d) size=(%dx%d)%s\n", el.ID, el.X, el.Y, el.W, el.H, label)
				}
				output += "\n(Use element IDs with click/set_value action to interact)"
				return result(output, details)
			}
			// fall through to ax mode if SOM rendering fails
		}

		if imageData != "" {
			details["image_base64"] = imageData
			details["format"] = "jpeg"
			details["size_bytes"] = len(imageData) * 3 / 4
		}

		if axTree != "" {
			output += "\nAX Tree:\n" + axTree
		} else {
			output += "\n(AX tree unavailable)"
		}

		return result(output, details)
	}

	raw, err = client.callTool(ctx, "screenshot", map[string]interface{}{
		"window_id": target.WindowID,
		"format":    "jpeg",
		"quality":   85,
	})
	if err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}

	var ssResult struct {
		Content []mcpContentPart `json:"content"`
	}
	if json.Unmarshal(raw, &ssResult) != nil {
		return nil, fmt.Errorf("parse screenshot result")
	}

	for _, part := range ssResult.Content {
		if strings.HasPrefix(part.Type, "image") || strings.HasPrefix(part.Type, "resource") {
			return result(
				fmt.Sprintf("Captured %s: %s | Backend: cua-driver (background, no focus steal)", target.AppName, target.Title),
				map[string]any{
					"image_base64": part.Data,
					"format":       "jpeg",
					"size_bytes":   len(part.Data) * 3 / 4,
					"app":          target.AppName,
					"title":        target.Title,
					"window_id":    target.WindowID,
				},
			)
		}
	}

	return nil, fmt.Errorf("no image in screenshot response")
}

type somElement struct {
	ID    int
	Label string
	X, Y  int
	W, H  int
}

var somColors = []color.RGBA{
	{255, 50, 50, 200},   // red
	{50, 150, 255, 200},   // blue
	{50, 200, 50, 200},    // green
	{255, 200, 0, 200},    // yellow
	{200, 50, 200, 200},   // purple
	{255, 100, 0, 200},    // orange
	{0, 200, 200, 200},    // cyan
	{200, 100, 50, 200},   // brown
	{100, 200, 100, 200},  // light green
	{200, 150, 200, 200},  // pink
}

func renderSOMOverlay(jpegBase64, axTree string) (string, []somElement, error) {
	raw, err := base64.StdEncoding.DecodeString(jpegBase64)
	if err != nil {
		return "", nil, fmt.Errorf("decode screenshot: %w", err)
	}
	elements := parseAXElements(axTree)
	annotated, err := renderSOMBody(raw, elements)
	if err != nil {
		return "", nil, err
	}
	return annotated, elements, nil
}

func renderSOMOverlayFromB64(jpegBase64 string, elements []somElement) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(jpegBase64)
	if err != nil {
		return "", fmt.Errorf("decode screenshot: %w", err)
	}
	return renderSOMBody(raw, elements)
}

func renderSOMBody(raw []byte, elements []somElement) (string, error) {
	src, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		// fallback: try other formats
		src2, _, err2 := image.Decode(bytes.NewReader(raw))
		if err2 != nil {
			return "", fmt.Errorf("decode image: jpeg: %w, other: %w", err, err2)
		}
		src = src2
	}

	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, src, image.Point{}, draw.Src)

	for i, el := range elements {
		c := somColors[i%len(somColors)]
		r := image.Rect(el.X, el.Y, el.X+el.W, el.Y+el.H)
		r = r.Intersect(bounds)
		if r.Empty() {
			continue
		}

		overlay := image.NewRGBA(r)
		draw.Draw(overlay, overlay.Bounds(), &image.Uniform{color.RGBA{c.R, c.G, c.B, 80}}, image.Point{}, draw.Src)
		draw.Draw(dst, r, overlay, image.Point{}, draw.Over)

		for dx := 0; dx < r.Dx(); dx++ {
			if dx < 2 || dx > r.Dx()-3 {
				for dy := 0; dy < r.Dy(); dy++ {
					dst.Set(r.Min.X+dx, r.Min.Y+dy, c)
				}
			}
		}
		for dy := 0; dy < r.Dy(); dy++ {
			if dy < 2 || dy > r.Dy()-3 {
				for dx := 0; dx < r.Dx(); dx++ {
					dst.Set(r.Min.X+dx, r.Min.Y+dy, c)
				}
			}
		}

		numStr := fmt.Sprintf("%d", el.ID)
		labelX := r.Min.X + 4
		labelY := r.Min.Y + 14
		if labelX < 0 {
			labelX = 0
		}
		if labelY < 0 {
			labelY = 14
		}
		d := font.Drawer{
			Dst:  dst,
			Src:  image.NewUniform(color.RGBA{c.R, c.G, c.B, 255}),
			Face: basicfont.Face7x13,
			Dot:  fixed.P(labelX-1, labelY-1),
		}
		d.DrawString(numStr)
		d.Src = image.NewUniform(color.White)
		d.Dot = fixed.P(labelX, labelY)
		d.DrawString(numStr)

		if el.Label != "" {
			maxLen := (r.Dx() - 8) / 7
			if maxLen > 0 && len(el.Label) > maxLen {
				el.Label = el.Label[:maxLen]
			}
			if maxLen > 0 {
				d.Dot = fixed.P(labelX, labelY+14)
				d.DrawString(el.Label)
			}
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return "", fmt.Errorf("encode annotated jpeg: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func parseAXElements(axTree string) []somElement {
	var elements []somElement
	seen := make(map[int]bool)

	re := regexp.MustCompile(`ax_id[=:](\d+)`)
	posRe := regexp.MustCompile(`(?:pos|position)\s*[=:]\s*\(?\s*(\d+)\s*,?\s*(\d+)`)
	sizeRe := regexp.MustCompile(`size\s*[=:]\s*\(?\s*(\d+)\s*,?\s*(\d+)`)
	labelRe := regexp.MustCompile(`[""]([^""]{1,40})[""]`)

	lines := strings.Split(axTree, "\n")
	for _, line := range lines {
		idMatch := re.FindStringSubmatch(line)
		if idMatch == nil {
			continue
		}
		id := parseInt(idMatch[1])
		if id <= 0 || seen[id] {
			continue
		}

		posMatch := posRe.FindStringSubmatch(line)
		if posMatch == nil {
			continue
		}
		x, y := parseInt(posMatch[1]), parseInt(posMatch[2])

		var w, h int
		if sizeMatch := sizeRe.FindStringSubmatch(line); sizeMatch != nil {
			w, h = parseInt(sizeMatch[1]), parseInt(sizeMatch[2])
		}
		if w <= 0 || h <= 0 {
			w, h = 20, 20
		}

		var label string
		if labelMatch := labelRe.FindStringSubmatch(line); labelMatch != nil {
			label = labelMatch[1]
		}

		seen[id] = true
		elements = append(elements, somElement{
			ID:    id,
			Label: label,
			X:     x,
			Y:     y,
			W:     w,
			H:     h,
		})
	}
	return elements
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func parseCuaWindows(raw json.RawMessage) []CuaWindow {
	var listResult struct {
		Content []struct {
			Type       string           `json:"type"`
			Text       string           `json:"text,omitempty"`
			Structured *json.RawMessage `json:"structuredContent,omitempty"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &listResult) != nil {
		return nil
	}

	for _, part := range listResult.Content {
		if part.Structured != nil {
			var decoded struct {
				Data []CuaWindow `json:"data"`
			}
			if json.Unmarshal(*part.Structured, &decoded) == nil && len(decoded.Data) > 0 {
				return decoded.Data
			}
		}
	}

	for _, part := range listResult.Content {
		if part.Text != "" {
			var ws []CuaWindow
			if json.Unmarshal([]byte(part.Text), &ws) == nil && len(ws) > 0 {
				return ws
			}
		}
	}
	return nil
}

func cuaDriverClick(ctx context.Context, action string, x, y, element int) (string, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return "", err
	}
	var toolName string
	switch action {
	case "double_click":
		toolName = "double_click"
	case "right_click":
		toolName = "right_click"
	case "middle_click":
		toolName = "click"
	default:
		toolName = "click"
	}
	args := map[string]interface{}{}
	if element > 0 {
		args["element"] = element
	} else {
		args["coordinate"] = []int{x, y}
	}
	if action == "middle_click" {
		args["button"] = "middle"
	}
	if _, err := client.callTool(ctx, toolName, args); err != nil {
		return "", fmt.Errorf("%s: %w", toolName, err)
	}
	if element > 0 {
		return fmt.Sprintf("%s on element %d via cua-driver (background)", action, element), nil
	}
	return fmt.Sprintf("%s at (%d, %d) via cua-driver (background)", action, x, y), nil
}

func cuaDriverType(ctx context.Context, text string) (string, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return "", err
	}
	if _, err := client.callTool(ctx, "type_text_chars", map[string]interface{}{
		"text": text,
	}); err != nil {
		return "", fmt.Errorf("type_text_chars: %w", err)
	}
	return fmt.Sprintf("Typed via cua-driver (background): %s", text), nil
}

func cuaDriverKey(ctx context.Context, keys string) (string, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return "", err
	}

	parts := strings.Split(keys, "+")
	var modifiers []string
	var key string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		lower := strings.ToLower(p)
		if mod, ok := cuaModMap[lower]; ok {
			modifiers = append(modifiers, mod)
		} else {
			key = p
		}
	}

	if len(modifiers) > 0 {
		_, err = client.callTool(ctx, "hotkey", map[string]interface{}{
			"keys":      key,
			"modifiers": modifiers,
		})
	} else {
		_, err = client.callTool(ctx, "press_key", map[string]interface{}{
			"key": key,
		})
	}
	if err != nil {
		return "", fmt.Errorf("key: %w", err)
	}
	return fmt.Sprintf("Pressed: %s via cua-driver (background)", keys), nil
}

func cuaDriverScroll(ctx context.Context, direction string, amount int) (string, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return "", err
	}
	if _, err := client.callTool(ctx, "scroll", map[string]interface{}{
		"direction": direction,
		"amount":    amount,
	}); err != nil {
		return "", fmt.Errorf("scroll: %w", err)
	}
	return fmt.Sprintf("Scrolled %s %d via cua-driver (background)", direction, amount), nil
}

func cuaDriverSetValue(ctx context.Context, element int, value string) (string, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return "", err
	}
	args := map[string]interface{}{"value": value}
	if element > 0 {
		args["element"] = element
	}
	if _, err := client.callTool(ctx, "set_value", args); err != nil {
		return "", fmt.Errorf("set_value: %w", err)
	}
	return fmt.Sprintf("Set value via cua-driver (AX): %s", value), nil
}

func cuaDriverListApps(ctx context.Context) (string, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return "", err
	}
	raw, err := client.callTool(ctx, "list_apps", nil)
	if err != nil {
		return "", fmt.Errorf("list_apps: %w", err)
	}
	var result struct {
		Content []mcpContentPart `json:"content"`
	}
	if json.Unmarshal(raw, &result) == nil {
		for _, part := range result.Content {
			if part.Type == "text" && part.Text != "" {
				return part.Text + "\nBackend: cua-driver", nil
			}
		}
	}
	return "Backend: cua-driver", nil
}

func cuaDriverFocusApp(ctx context.Context, app string, raiseWindow bool) (string, error) {
	client, err := getCuaDriverClient(ctx)
	if err != nil {
		return "", err
	}
	raw, err := client.callTool(ctx, "list_windows", map[string]interface{}{
		"on_screen_only": true,
	})
	if err != nil {
		return "", fmt.Errorf("list_windows: %w", err)
	}
	_ = raw
	if raiseWindow {
		// cua-driver can't raise windows; note this in response
		return fmt.Sprintf("Targeting: %s via cua-driver (background, raise_window requested but cua-driver does not support raising windows)", app), nil
	}
	return fmt.Sprintf("Targeting: %s via cua-driver (background, no focus steal)", app), nil
}

// --- Fallback implementations ---

func fallbackCapture(ctx context.Context, backend cuBackend, appName, mode string) (any, error) {
	screenshotPath := fmt.Sprintf("/tmp/covo_cu_%d.png", time.Now().UnixNano())
	var args []string
	if appName != "" {
		bounds, err := getWindowBounds(appName)
		if err == nil {
			args = append(args, "-R", bounds)
		}
	}
	args = append(args, "-x", screenshotPath)
	if err := exec.Command("screencapture", args...).Run(); err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	data, err := os.ReadFile(screenshotPath)
	os.Remove(screenshotPath)
	if err != nil {
		return nil, fmt.Errorf("read screenshot: %w", err)
	}

	if mode == "ax" {
		info, _ := osaExec(`tell app "System Events" to get name of first process whose frontmost is true`)
		return result(
			fmt.Sprintf("Screenshot captured (%d bytes). AX tree not available without cua-driver.", len(data)),
			map[string]any{
				"size_bytes":          len(data),
				"screenshot_available": true,
				"frontmost_app":       info,
			},
		)
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	return result(
		fmt.Sprintf("Screenshot captured (%d bytes) via %s", len(data), backend),
		map[string]any{
			"image_base64": b64,
			"format":       "png",
			"size_bytes":   len(data),
		},
	)
}

func fallbackClick(backend cuBackend, action string, x, y int) (string, error) {
	if backend == cuBackendCliclick {
		// cliclick doesn't support middle_click; fall through to osascript
		if action == "middle_click" {
			return osaClick(action, x, y)
		}
		var cliAction string
		switch action {
		case "double_click":
			cliAction = "dc"
		case "right_click":
			cliAction = "rc"
		default:
			cliAction = "c"
		}
		if _, err := cliclickExec(fmt.Sprintf("%s:%d,%d", cliAction, x, y)); err != nil {
			return "", fmt.Errorf("%s: %w", action, err)
		}
		return fmt.Sprintf("%s at (%d, %d) via cliclick", action, x, y), nil
	}

	return osaClick(action, x, y)
}

func osaClick(action string, x, y int) (string, error) {
	var clickType string
	switch action {
	case "double_click":
		clickType = "double click"
	case "right_click":
		clickType = "click button 2"
	case "middle_click":
		clickType = "click button 3"
	default:
		clickType = "click"
	}
	if _, err := osaExec(fmt.Sprintf(`tell app "System Events" to %s at {%d, %d}`, clickType, x, y)); err != nil {
		return "", fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Sprintf("%s at (%d, %d) via osascript", action, x, y), nil
}

func fallbackDrag(backend cuBackend, x1, y1, x2, y2 int) (string, error) {
	if backend == cuBackendCliclick {
		if _, err := cliclickExec(
			fmt.Sprintf("dd:%d,%d", x1, y1),
			fmt.Sprintf("du:%d,%d", x2, y2),
		); err != nil {
			return "", fmt.Errorf("drag: %w", err)
		}
		return fmt.Sprintf("Dragged from (%d,%d) to (%d,%d) via cliclick", x1, y1, x2, y2), nil
	}

	script := fmt.Sprintf(`tell application "System Events"
		set mouseDown at {%d, %d}
		delay 0.1
		set mouseUp at {%d, %d}
	end tell`, x1, y1, x2, y2)
	if _, err := osaExec(script); err != nil {
		return "", fmt.Errorf("drag: %w", err)
	}
	return fmt.Sprintf("Dragged from (%d,%d) to (%d,%d) via osascript", x1, y1, x2, y2), nil
}

func fallbackType(backend cuBackend, text string) (string, error) {
	escaped := strings.ReplaceAll(text, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	if backend == cuBackendCliclick {
		if _, err := cliclickExec(fmt.Sprintf(`t:"%s"`, escaped)); err != nil {
			return "", fmt.Errorf("type: %w", err)
		}
		return fmt.Sprintf("Typed via cliclick: %s", text), nil
	}
	if _, err := osaExec(fmt.Sprintf(`tell app "System Events" to keystroke "%s"`, escaped)); err != nil {
		return "", fmt.Errorf("type: %w", err)
	}
	return fmt.Sprintf("Typed via osascript: %s", text), nil
}

func fallbackKey(backend cuBackend, keys string) (string, error) {
	if backend == cuBackendCliclick {
		return cliclickKeyImpl(keys)
	}
	return osaKeyImpl(keys)
}

func cliclickKeyImpl(keys string) (string, error) {
	parts := strings.Split(keys, "+")
	var modifiers []string
	var key string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		lower := strings.ToLower(p)
		if mod, ok := cliclickModMap[lower]; ok {
			modifiers = append(modifiers, mod)
		} else if named, ok := cliclickKeyNames[lower]; ok {
			key = named
		} else {
			key = p
		}
	}
	if len(modifiers) > 0 {
		var args []string
		for _, m := range modifiers {
			args = append(args, fmt.Sprintf("kd:%s", m))
		}
		args = append(args, fmt.Sprintf("kp:%s", key))
		for i := len(modifiers) - 1; i >= 0; i-- {
			args = append(args, fmt.Sprintf("ku:%s", modifiers[i]))
		}
		if _, err := cliclickExec(args...); err != nil {
			return "", fmt.Errorf("key combo: %w", err)
		}
	} else {
		if _, err := cliclickExec(fmt.Sprintf("kp:%s", key)); err != nil {
			return "", fmt.Errorf("key press: %w", err)
		}
	}
	return fmt.Sprintf("Pressed via cliclick: %s", keys), nil
}

func osaKeyImpl(keys string) (string, error) {
	parts := strings.Split(keys, "+")
	var modifiers []string
	var key string
	isNamedKey := false
	for _, p := range parts {
		p = strings.TrimSpace(p)
		lower := strings.ToLower(p)
		if mod, ok := osaModMap[lower]; ok {
			modifiers = append(modifiers, mod)
		} else if named, ok := osaKeyNames[lower]; ok {
			key = named
			isNamedKey = true
		} else {
			key = p
		}
	}

	var script string
	if len(modifiers) > 0 {
		modStr := strings.Join(modifiers, ", ")
		tmpl := `tell app "System Events" to keystroke "%s" using {%s}`
		if isNamedKey {
			tmpl = `tell app "System Events" to keystroke %s using {%s}`
		}
		script = fmt.Sprintf(tmpl, key, modStr)
	} else if isNamedKey {
		script = fmt.Sprintf(`tell app "System Events" to keystroke %s`, key)
	} else {
		script = fmt.Sprintf(`tell app "System Events" to keystroke "%s"`, key)
	}
	if _, err := osaExec(script); err != nil {
		return "", fmt.Errorf("key: %w", err)
	}
	return fmt.Sprintf("Pressed via osascript: %s", keys), nil
}

func fallbackScroll(backend cuBackend, direction string, amount int) (string, error) {
	if backend == cuBackendCliclick {
		var flag string
		switch direction {
		case "up":
			flag = "wu"
		case "down":
			flag = "wd"
		case "left":
			flag = "wl"
		case "right":
			flag = "wr"
		}
		if _, err := cliclickExec(fmt.Sprintf("%s:%d", flag, amount)); err != nil {
			return "", fmt.Errorf("scroll: %w", err)
		}
		return fmt.Sprintf("Scrolled %s %d via cliclick", direction, amount), nil
	}

	repeat := max(1, amount/3)
	var keyName string
	switch direction {
	case "up":
		keyName = "page up"
	case "down":
		keyName = "page down"
	case "left":
		keyName = "left"
	case "right":
		keyName = "right"
	}
	if _, err := osaExec(fmt.Sprintf(`tell app "System Events" to repeat %d times
		keystroke %s
	end repeat`, repeat, keyName)); err != nil {
		return "", fmt.Errorf("scroll: %w", err)
	}
	return fmt.Sprintf("Scrolled %s via osascript", direction), nil
}

func fallbackSetValue(backend cuBackend, value string) (string, error) {
	// Fallback: type the value and press enter (works for most input fields)
	if _, err := fallbackType(backend, value); err != nil {
		return "", fmt.Errorf("set_value: %w", err)
	}
	osaKeyImpl("return")
	return fmt.Sprintf("Set value via fallback (type+enter): %s", value), nil
}

func fallbackListApps() (string, error) {
	apps, err := osaExec(`tell app "System Events"
		set appList to name of every process whose background only is false
		set appStr to ""
		repeat with appName in appList
			set appStr to appStr & (appName as text) & return
		end repeat
		return appStr
	end tell`)
	if err != nil {
		return "", fmt.Errorf("list apps: %w", err)
	}
	lines := strings.Split(apps, "\n")
	var visible []string
	for _, a := range lines {
		a = strings.TrimSpace(a)
		if a != "" {
			visible = append(visible, a)
		}
	}
	return strings.Join(visible, "\n") + "\nBackend: osascript", nil
}

func fallbackFocusApp(app string, raiseWindow bool) (string, error) {
	if raiseWindow {
		if _, err := osaExec(fmt.Sprintf(`tell app "%s" to activate`, app)); err != nil {
			return "", fmt.Errorf("focus: %w", err)
		}
		return fmt.Sprintf("Focused via osascript: %s (raised)", app), nil
	}
	// Without raise, just verify app is running without bringing it to front
	if _, err := osaExec(fmt.Sprintf(`tell app "System Events" to exists process "%s"`, app)); err != nil {
		return "", fmt.Errorf("focus: %w", err)
	}
	return fmt.Sprintf("Targeting: %s via osascript (not raised)", app), nil
}

// --- Windows PowerShell implementations ---

func winCapture(ctx context.Context, appName string) (any, error) {
	boundsScript := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing
$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
"$($bounds.X),$($bounds.Y),$($bounds.Width),$($bounds.Height)"`
	if appName != "" {
		boundsScript = fmt.Sprintf(`Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
	[DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
	[StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
}
"@
$proc = Get-Process | Where-Object {$_.MainWindowTitle -ne ""} | Where-Object {$_.ProcessName -like "*%s*"} | Select-Object -First 1
if (-not $proc) { exit 1 }
$rect = New-Object Win32+RECT
[Win32]::GetWindowRect($proc.MainWindowHandle, [ref]$rect)
"$($rect.Left),$($rect.Top),$($rect.Right - $rect.Left),$($rect.Bottom - $rect.Top)"`, appName)
	}

	script := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing
$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$bmp = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($bounds.X, $bounds.Y, 0, 0, $bounds.Size)
$ms = New-Object System.IO.MemoryStream
$bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()
$g.Dispose()
[System.Convert]::ToBase64String($ms.ToArray())`
	out, err := pwshExec(script)
	if err != nil {
		return nil, err
	}
	b64 := strings.TrimSpace(out)

	desc := "powershell"
	raw := make([]byte, base64.StdEncoding.DecodedLen(len(b64)))
	n, _ := base64.StdEncoding.Decode(raw, []byte(b64))
	data := raw[:n]

	if appName != "" {
		boundsOut, err := pwshExec(boundsScript)
		if err == nil {
			parts := strings.Split(strings.TrimSpace(boundsOut), ",")
			if len(parts) == 4 {
				x := parseInt(parts[0])
				y := parseInt(parts[1])
				w := parseInt(parts[2])
				h := parseInt(parts[3])
				if w > 0 && h > 0 {
					if cropped, err := cropImageToBounds(data, x, y, w, h); err == nil {
						data = cropped
						desc = fmt.Sprintf("powershell (cropped to %s)", appName)
					}
				}
			}
		}
	}

	b64 = base64.StdEncoding.EncodeToString(data)
	return result(
		fmt.Sprintf("Screenshot captured (%d bytes) via %s", len(data), desc),
		map[string]any{
			"image_base64": b64,
			"format":       "png",
			"size_bytes":   len(data),
		},
	)
}

func winCaptureSOM(ctx context.Context, appName string) (any, error) {
	// Step 1: capture screenshot
	screenshotResult, err := winCapture(ctx, appName)
	if err != nil {
		return nil, err
	}
	tr, ok := screenshotResult.(ToolResult)
	if !ok {
		return nil, fmt.Errorf("unexpected screenshot result type")
	}
	details, ok := tr.Details.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected screenshot details type")
	}
	b64, ok := details["image_base64"].(string)
	if !ok || b64 == "" {
		return nil, fmt.Errorf("no image in screenshot")
	}

	// Step 2: get UIA accessibility tree with element positions
	uiaScript := `Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
	[DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
}
"@
$hwnd = [Win32]::GetForegroundWindow()
$rootEl = [System.Windows.Automation.AutomationElement]::FromHandle($hwnd)
$walker = [System.Windows.Automation.TreeWalker]::ContentViewWalker
$result = New-Object System.Collections.ArrayList
$nextId = 1
function Walk($el, $depth) {
	if ($depth -gt 6) { return }
	$rect = $el.Current.BoundingRectangle
	if ($rect.Width -gt 0 -and $rect.Height -gt 0) {
		[void]$result.Add(@{
			id = $nextId
			name = $el.Current.Name
			type = $el.Current.ControlType.ProgrammaticName
			x = [int]$rect.X
			y = [int]$rect.Y
			w = [int]$rect.Width
			h = [int]$rect.Height
		})
		$script:nextId++
	}
	$child = $walker.GetFirstChild($el)
	while ($child -ne $null) {
		Walk $child ($depth + 1)
		$child = $walker.GetNextChild($child)
	}
}
$script:nextId = 1
Walk $rootEl 0
$result | ConvertTo-Json -Compress`
	uiaOut, err := pwshExec(uiaScript)
	if err != nil {
		// fallback to regular capture
		return screenshotResult, nil
	}
	uiaOut = strings.TrimSpace(uiaOut)

	var uiaElements []somElement
	if err := json.Unmarshal([]byte(uiaOut), &uiaElements); err != nil {
		return screenshotResult, nil
	}
	if len(uiaElements) == 0 {
		return screenshotResult, nil
	}

	// Step 3: render SOM overlay
	annotated, err := renderSOMOverlayFromB64(b64, uiaElements)
	if err != nil {
		return screenshotResult, nil
	}

	details["image_base64"] = annotated
	details["format"] = "png"
	details["size_bytes"] = len(annotated) * 3 / 4
	details["som"] = true

	output := "SOM Elements:\n"
	for _, el := range uiaElements {
		label := el.Label
		if label != "" {
			label = " " + label
		}
		output += fmt.Sprintf("  [%d] pos=(%d,%d) size=(%dx%d)%s\n", el.ID, el.X, el.Y, el.W, el.H, label)
	}
	output += "\n(Use element IDs with click or set_value)"
	return result(output, details)
}

func winInfo() (any, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$screen = [System.Windows.Forms.Screen]::PrimaryScreen
$cursor = [System.Windows.Forms.Cursor]::Position
$w = $screen.Bounds.Width
$h = $screen.Bounds.Height
"Screen: $w x $h | Cursor: ($($cursor.X), $($cursor.Y)) | Backend: powershell"`
	out, err := pwshExec(script)
	if err != nil {
		return nil, fmt.Errorf("screen info failed: %w", err)
	}
	return result(strings.TrimSpace(out), nil)
}

var winVK = map[string]byte{
	"return": 0x0D, "enter": 0x0D,
	"escape": 0x1B, "esc": 0x1B,
	"tab": 0x09, "space": 0x20,
	"backspace": 0x08, "delete": 0x08,
	"up": 0x26, "down": 0x28,
	"left": 0x25, "right": 0x27,
	"home": 0x24, "end": 0x23,
	"pageup": 0x21, "pagedown": 0x22,
	"pgup": 0x21, "pgdn": 0x22,
}

func winClick(action string, x, y int) (string, error) {
	var downFlag, upFlag string
	switch action {
	case "double_click":
		downFlag, upFlag = "0x0002", "0x0004"
	case "right_click":
		downFlag, upFlag = "0x0008", "0x0010"
	case "middle_click":
		downFlag, upFlag = "0x0020", "0x0040"
	default:
		downFlag, upFlag = "0x0002", "0x0004"
	}
	script := fmt.Sprintf(`Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
	[DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, UIntPtr dwExtraInfo);
	[DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
}
"@
[Win32]::SetCursorPos(%d, %d)
[Win32]::mouse_event(%s, 0, 0, 0, [UIntPtr]::Zero)
[Win32]::mouse_event(%s, 0, 0, 0, [UIntPtr]::Zero)`, x, y, downFlag, upFlag)
	if action == "double_click" {
		script += fmt.Sprintf(`
Start-Sleep -Milliseconds 50
[Win32]::mouse_event(%s, 0, 0, 0, [UIntPtr]::Zero)
[Win32]::mouse_event(%s, 0, 0, 0, [UIntPtr]::Zero)`, downFlag, upFlag)
	}
	if _, err := pwshExec(script); err != nil {
		return "", fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Sprintf("%s at (%d, %d) via powershell", action, x, y), nil
}

func winDrag(x1, y1, x2, y2 int) (string, error) {
	script := fmt.Sprintf(`Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
	[DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, UIntPtr dwExtraInfo);
	[DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
}
"@
[Win32]::SetCursorPos(%d, %d)
[Win32]::mouse_event(0x0002, 0, 0, 0, [UIntPtr]::Zero)
[Win32]::SetCursorPos(%d, %d)
[Win32]::mouse_event(0x0004, 0, 0, 0, [UIntPtr]::Zero)`, x1, y1, x2, y2)
	if _, err := pwshExec(script); err != nil {
		return "", fmt.Errorf("drag: %w", err)
	}
	return fmt.Sprintf("Dragged from (%d,%d) to (%d,%d) via powershell", x1, y1, x2, y2), nil
}

func winType(text string) (string, error) {
	escaped := strings.ReplaceAll(text, `"`, `""`)
	script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait("%s")`, escaped)
	if _, err := pwshExec(script); err != nil {
		return "", fmt.Errorf("type: %w", err)
	}
	return fmt.Sprintf("Typed via powershell: %s", text), nil
}

func winKey(keys string) (string, error) {
	parts := strings.Split(keys, "+")
	var mods []string
	var key string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		lower := strings.ToLower(p)
		switch lower {
		case "cmd", "command", "ctrl", "control":
			mods = append(mods, "^")
		case "alt", "option":
			mods = append(mods, "%")
		case "shift":
			mods = append(mods, "+")
		default:
			if vk, ok := winVK[lower]; ok {
				key = string(rune(vk))
			} else if len(p) == 1 {
				key = p
			} else {
				key = p
			}
		}
	}
	sendKey := key
	if len(mods) > 0 {
		sendKey = strings.Join(mods, "") + "(" + key + ")"
	}
	script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait("%s")`, sendKey)
	if _, err := pwshExec(script); err != nil {
		return "", fmt.Errorf("key: %w", err)
	}
	return fmt.Sprintf("Pressed via powershell: %s", keys), nil
}

func winScroll(direction string, amount int) (string, error) {
	delta := amount * 120
	if direction == "down" || direction == "right" {
		delta = -delta
	}
	var hScrollFlag string
	if direction == "left" || direction == "right" {
		hScrollFlag = ", 0x0001"
	}
	script := fmt.Sprintf(`Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
	[DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, UIntPtr dwExtraInfo);
}
"@
[Win32]::mouse_event(0x0800, 0, 0, %d%s, [UIntPtr]::Zero)`, delta, hScrollFlag)
	if _, err := pwshExec(script); err != nil {
		return "", fmt.Errorf("scroll: %w", err)
	}
	return fmt.Sprintf("Scrolled %s %d via powershell", direction, amount), nil
}

func winSetValue(value string) (string, error) {
	if _, err := winType(value); err != nil {
		return "", fmt.Errorf("set_value: %w", err)
	}
	winKey("return")
	return fmt.Sprintf("Set value via powershell (type+enter): %s", value), nil
}

func winListApps() (string, error) {
	script := `Get-Process | Where-Object {$_.MainWindowHandle -ne 0} | Sort-Object ProcessName | Select-Object -ExpandProperty ProcessName`
	out, err := pwshExec(script)
	if err != nil {
		return "", fmt.Errorf("list apps: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var visible []string
	for _, a := range lines {
		a = strings.TrimSpace(a)
		if a != "" {
			visible = append(visible, a)
		}
	}
	return strings.Join(visible, "\n") + "\nBackend: powershell", nil
}

func winFocusApp(app string, raiseWindow bool) (string, error) {
	script := fmt.Sprintf(`Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Win32 {
	[DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd);
	[DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
	[DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
	[DllImport("user32.dll")] public static extern int GetWindowText(IntPtr hWnd, System.Text.StringBuilder text, int count);
}
"@
$proc = Get-Process | Where-Object {$_.MainWindowTitle -ne ""} | Where-Object {$_.ProcessName -like "*%s*"}
if (-not $proc) { exit 1 }
if (%t) {
	[Win32]::ShowWindow($proc.MainWindowHandle, 9)
	[Win32]::SetForegroundWindow($proc.MainWindowHandle)
}`, app, raiseWindow)
	if _, err := pwshExec(script); err != nil {
		return "", fmt.Errorf("focus: %w", err)
	}
	if raiseWindow {
		return fmt.Sprintf("Focused via powershell: %s (raised)", app), nil
	}
	return fmt.Sprintf("Targeting: %s via powershell (not raised)", app), nil
}

// --- Linux xdotool implementations ---

func xdoCapture(ctx context.Context, appName string) (any, error) {
	screenshotPath := fmt.Sprintf("/tmp/covo_cu_%d.png", time.Now().UnixNano())
	if err := scrotExec(screenshotPath); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(screenshotPath)
	os.Remove(screenshotPath)
	if err != nil {
		return nil, fmt.Errorf("read screenshot: %w", err)
	}

	desc := func() string {
		if isWayland() {
			return "wayland"
		}
		return "xdotool"
	}()
	if appName != "" {
		bounds := ""
		if isWayland() {
			bounds, _ = waylandGetWindowBounds(appName)
		} else {
			bounds, _ = linuxGetWindowBounds(appName)
		}
		if bounds != "" {
			parts := strings.Split(bounds, ",")
			if len(parts) == 4 {
				x := parseInt(parts[0])
				y := parseInt(parts[1])
				w := parseInt(parts[2])
				h := parseInt(parts[3])
				if w > 0 && h > 0 {
					if cropped, err := cropImageToBounds(data, x, y, w, h); err == nil {
						data = cropped
						desc = fmt.Sprintf("xdotool (cropped to %s)", appName)
					}
				}
			}
		}
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	return result(
		fmt.Sprintf("Screenshot captured (%d bytes) via %s", len(data), desc),
		map[string]any{
			"image_base64": b64,
			"format":       "png",
			"size_bytes":   len(data),
		},
	)
}

func linuxGetWindowBounds(app string) (string, error) {
	if isWayland() {
		return "", fmt.Errorf("window bounds not available on Wayland")
	}
	ids, err := xdoExec("search", "--onlyvisible", "--name", "--", app)
	if err != nil || strings.TrimSpace(ids) == "" {
		ids2, err2 := xdoExec("search", "--onlyvisible", "--class", app)
		if err2 != nil || strings.TrimSpace(ids2) == "" {
			return "", fmt.Errorf("window not found: %s", app)
		}
		ids = ids2
	}
	winID := strings.Fields(ids)[0]
	geo, err := xdoExec("getwindowgeometry", winID)
	if err != nil {
		return "", err
	}
	var x, y, w, h int
	fmt.Sscanf(geo, "Position: %d,%d\nGeometry: %dx%d", &x, &y, &w, &h)
	if w == 0 || h == 0 {
		return "", fmt.Errorf("could not parse geometry for window %s", winID)
	}
	return fmt.Sprintf("%d,%d,%d,%d", x, y, w, h), nil
}

func xdoInfo() (any, error) {
	geo, err := xdoExec("getdisplaygeometry")
	if err != nil {
		return nil, fmt.Errorf("screen info failed: %w", err)
	}
	geo = strings.TrimSpace(geo)
	parts := strings.Split(geo, " ")
	screen := "unknown"
	if len(parts) >= 2 {
		screen = parts[0] + " x " + parts[1]
	}
	cursor, _ := xdoExec("getmouselocation", "--shell")
	cursor = strings.TrimSpace(cursor)
	active, _ := xdoExec("getactivewindow")
	activeName, _ := xdoExec("getwindowname", strings.TrimSpace(active))
	return result(fmt.Sprintf("Screen: %s | Backend: xdotool\nCursor: %s\nActive: %s", screen, cursor, strings.TrimSpace(activeName)), nil)
}

func xdoClick(action string, x, y int) (string, error) {
	// Move mouse first
	if _, err := xdoExec("mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)); err != nil {
		return "", fmt.Errorf("mousemove: %w", err)
	}
	var btn string
	switch action {
	case "double_click":
		btn = "1"
	case "right_click":
		btn = "3"
	case "middle_click":
		btn = "2"
	default:
		btn = "1"
	}
	if action == "double_click" {
		if _, err := xdoExec("click", "--repeat", "2", btn); err != nil {
			return "", fmt.Errorf("%s: %w", action, err)
		}
	} else {
		if _, err := xdoExec("click", btn); err != nil {
			return "", fmt.Errorf("%s: %w", action, err)
		}
	}
	return fmt.Sprintf("%s at (%d, %d) via xdotool", action, x, y), nil
}

func xdoDrag(x1, y1, x2, y2 int) (string, error) {
	if _, err := xdoExec("mousemove", fmt.Sprintf("%d", x1), fmt.Sprintf("%d", y1)); err != nil {
		return "", fmt.Errorf("drag mousemove: %w", err)
	}
	if _, err := xdoExec("mousedown", "1"); err != nil {
		return "", fmt.Errorf("drag mousedown: %w", err)
	}
	if _, err := xdoExec("mousemove", fmt.Sprintf("%d", x2), fmt.Sprintf("%d", y2)); err != nil {
		return "", fmt.Errorf("drag mousemove: %w", err)
	}
	if _, err := xdoExec("mouseup", "1"); err != nil {
		return "", fmt.Errorf("drag mouseup: %w", err)
	}
	return fmt.Sprintf("Dragged from (%d,%d) to (%d,%d) via xdotool", x1, y1, x2, y2), nil
}

func xdoType(text string) (string, error) {
	if _, err := xdoExec("type", "--delay", "12", text); err != nil {
		return "", fmt.Errorf("type: %w", err)
	}
	return fmt.Sprintf("Typed via xdotool: %s", text), nil
}

var xdoKeyMap = map[string]string{
	"return": "Return", "enter": "Return",
	"escape": "Escape", "esc": "Escape",
	"tab": "Tab", "space": "space",
	"backspace": "BackSpace", "delete": "BackSpace",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End",
	"pageup": "Page_Up", "pagedown": "Page_Down",
	"pgup": "Page_Up", "pgdn": "Page_Down",
	"f1": "F1", "f2": "F2", "f3": "F3", "f4": "F4",
	"f5": "F5", "f6": "F6", "f7": "F7", "f8": "F8",
	"f9": "F9", "f10": "F10", "f11": "F11", "f12": "F12",
}

func xdoKey(keys string) (string, error) {
	parts := strings.Split(keys, "+")
	var mods []string
	var key string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		lower := strings.ToLower(p)
		switch lower {
		case "cmd", "command", "ctrl", "control":
			mods = append(mods, "ctrl")
		case "alt", "option":
			mods = append(mods, "alt")
		case "shift":
			mods = append(mods, "shift")
		default:
			if mapped, ok := xdoKeyMap[lower]; ok {
				key = mapped
			} else if len(p) == 1 {
				key = p
			} else {
				key = p
			}
		}
	}
	var xdoArgs []string
	if len(mods) > 0 {
		xdoArgs = append(xdoArgs, "key")
		combo := strings.Join(append(mods, key), "+")
		xdoArgs = append(xdoArgs, combo)
	} else {
		xdoArgs = append(xdoArgs, "key", key)
	}
	if _, err := xdoExec(xdoArgs...); err != nil {
		return "", fmt.Errorf("key: %w", err)
	}
	return fmt.Sprintf("Pressed via xdotool: %s", keys), nil
}

func xdoScroll(direction string, amount int) (string, error) {
	var btn string
	switch direction {
	case "up":
		btn = "4"
	case "down":
		btn = "5"
	case "left":
		btn = "6"
	case "right":
		btn = "7"
	}
	for i := 0; i < amount; i++ {
		if _, err := xdoExec("click", btn); err != nil {
			return "", fmt.Errorf("scroll: %w", err)
		}
	}
	return fmt.Sprintf("Scrolled %s %d via xdotool", direction, amount), nil
}

func xdoSetValue(value string) (string, error) {
	if _, err := xdoType(value); err != nil {
		return "", fmt.Errorf("set_value: %w", err)
	}
	xdoKey("return")
	return fmt.Sprintf("Set value via xdotool (type+enter): %s", value), nil
}

func xdoListApps() (string, error) {
	out, err := xdoExec("search", "--onlyvisible", "--name", ".")
	if err != nil {
		return "", fmt.Errorf("list apps: %w", err)
	}
	ids := strings.Fields(out)
	var visible []string
	for _, id := range ids {
		name, err := xdoExec("getwindowname", id)
		if err != nil {
			continue
		}
		name = strings.TrimSpace(name)
		if name != "" {
			visible = append(visible, name)
		}
	}
	return strings.Join(visible, "\n") + "\nBackend: xdotool", nil
}

func xdoFocusApp(app string, raiseWindow bool) (string, error) {
	if isWayland() {
		return waylandFocusApp(app, raiseWindow)
	}
	ids, err := xdoExec("search", "--onlyvisible", "--name", "--", app)
	if err != nil || strings.TrimSpace(ids) == "" {
		ids2, err2 := xdoExec("search", "--onlyvisible", "--class", app)
		if err2 != nil || strings.TrimSpace(ids2) == "" {
			return "", fmt.Errorf("no window found for app: %s", app)
		}
		ids = ids2
	}
	winID := strings.Fields(ids)[0]
	if raiseWindow {
		if _, err := xdoExec("windowactivate", winID); err != nil {
			return "", fmt.Errorf("focus: %w", err)
		}
		return fmt.Sprintf("Focused via xdotool: %s (raised)", app), nil
	}
	return fmt.Sprintf("Targeting: %s via xdotool (not raised)", app), nil
}

func waylandGetWindowBounds(app string) (string, error) {
	// Try Hyprland
	if _, err := exec.LookPath("hyprctl"); err == nil {
		out, err := exec.Command("hyprctl", "clients", "-j").Output()
		if err == nil {
			var clients []struct {
				Title       string `json:"title"`
				InitialTitle string `json:"initialTitle"`
				Class       string `json:"class"`
				At          []int  `json:"at"`
				Size        []int  `json:"size"`
			}
			if json.Unmarshal(out, &clients) == nil {
				appLower := strings.ToLower(app)
				for _, c := range clients {
					if strings.Contains(strings.ToLower(c.Class), appLower) ||
						strings.Contains(strings.ToLower(c.Title), appLower) ||
						strings.Contains(strings.ToLower(c.InitialTitle), appLower) {
						if len(c.At) >= 2 && len(c.Size) >= 2 && c.Size[0] > 0 && c.Size[1] > 0 {
							return fmt.Sprintf("%d,%d,%d,%d", c.At[0], c.At[1], c.Size[0], c.Size[1]), nil
						}
					}
				}
			}
		}
	}

	// Try Sway
	if _, err := exec.LookPath("swaymsg"); err == nil {
		out, err := exec.Command("swaymsg", "-t", "get_tree").Output()
		if err == nil {
			var root struct {
				Nodes []struct {
					Nodes []struct {
						Nodes []struct {
							AppID string `json:"app_id"`
							Name  string `json:"name"`
							Rect  struct {
								X      int `json:"x"`
								Y      int `json:"y"`
								Width  int `json:"width"`
								Height int `json:"height"`
							} `json:"rect"`
						} `json:"nodes"`
					} `json:"nodes"`
				} `json:"nodes"`
			}
			if json.Unmarshal(out, &root) == nil {
				appLower := strings.ToLower(app)
				for _, o1 := range root.Nodes {
					for _, o2 := range o1.Nodes {
						for _, leaf := range o2.Nodes {
							if strings.Contains(strings.ToLower(leaf.AppID), appLower) ||
								strings.Contains(strings.ToLower(leaf.Name), appLower) {
								if leaf.Rect.Width > 0 && leaf.Rect.Height > 0 {
									return fmt.Sprintf("%d,%d,%d,%d", leaf.Rect.X, leaf.Rect.Y, leaf.Rect.Width, leaf.Rect.Height), nil
								}
							}
						}
					}
				}
			}
		}
	}

	return "", fmt.Errorf("no Wayland compositor recognized (supported: Hyprland, Sway)")
}

func waylandFocusApp(app string, raiseWindow bool) (string, error) {
	lower := strings.ToLower(app)

	// Try Hyprland
	if _, err := exec.LookPath("hyprctl"); err == nil {
		out, err := exec.Command("hyprctl", "clients", "-j").Output()
		if err == nil {
			var clients []struct {
				Title       string `json:"title"`
				InitialTitle string `json:"initialTitle"`
				Class       string `json:"class"`
				Address     string `json:"address"`
			}
			if json.Unmarshal(out, &clients) == nil {
				for _, c := range clients {
					if strings.Contains(strings.ToLower(c.Class), lower) ||
						strings.Contains(strings.ToLower(c.Title), lower) ||
						strings.Contains(strings.ToLower(c.InitialTitle), lower) {
						if raiseWindow {
							exec.Command("hyprctl", "dispatch", "focuswindow", "address:"+c.Address).Run()
							return fmt.Sprintf("Focused via Hyprland: %s (raised)", app), nil
						}
						return fmt.Sprintf("Targeting: %s via Hyprland (not raised)", app), nil
					}
				}
			}
		}
	}

	// Try Sway
	if _, err := exec.LookPath("swaymsg"); err == nil {
		if raiseWindow {
			if err := exec.Command("swaymsg", fmt.Sprintf(`[title="(?i)%s"]`, app), "focus").Run(); err == nil {
				return fmt.Sprintf("Focused via Sway: %s (raised)", app), nil
			}
			if err := exec.Command("swaymsg", fmt.Sprintf(`[app_id="(?i)%s"]`, app), "focus").Run(); err == nil {
				return fmt.Sprintf("Focused via Sway: %s (raised)", app), nil
			}
		} else {
			return fmt.Sprintf("Targeting: %s via Sway (not raised)", app), nil
		}
	}

	return "", fmt.Errorf("no Wayland compositor found for focus (supported: Hyprland, Sway)")
}

// --- ydotool (Linux Wayland) implementations ---

func ydoClick(action string, x, y int) (string, error) {
	if _, err := ydoExec("mousemove", "--absolute", "-x", fmt.Sprintf("%d", x), "-y", fmt.Sprintf("%d", y)); err != nil {
		return "", fmt.Errorf("mousemove: %w", err)
	}
	var btnCode string
	switch action {
	case "right_click":
		btnCode = "0x2"
	case "middle_click":
		btnCode = "0x3"
	default:
		btnCode = "0x1"
	}
	if action == "double_click" {
		for i := 0; i < 2; i++ {
			if _, err := ydoExec("click", btnCode); err != nil {
				return "", fmt.Errorf("%s: %w", action, err)
			}
		}
	} else {
		if _, err := ydoExec("click", btnCode); err != nil {
			return "", fmt.Errorf("%s: %w", action, err)
		}
	}
	return fmt.Sprintf("%s at (%d, %d) via ydotool", action, x, y), nil
}

func ydoType(text string) (string, error) {
	if _, err := wtypeExec(text); err != nil {
		return "", fmt.Errorf("type: %w", err)
	}
	return fmt.Sprintf("Typed via wtype: %s", text), nil
}

var ydoKeyCodes = map[string]string{
	"return": "KEY_ENTER", "enter": "KEY_ENTER",
	"escape": "KEY_ESC", "esc": "KEY_ESC",
	"tab": "KEY_TAB", "space": "KEY_SPACE",
	"backspace": "KEY_BACKSPACE", "delete": "KEY_BACKSPACE",
	"up": "KEY_UP", "down": "KEY_DOWN", "left": "KEY_LEFT", "right": "KEY_RIGHT",
	"home": "KEY_HOME", "end": "KEY_END",
	"pageup": "KEY_PAGEUP", "pagedown": "KEY_PAGEDOWN",
	"pgup": "KEY_PAGEUP", "pgdn": "KEY_PAGEDOWN",
}

func ydoKey(keys string) (string, error) {
	parts := strings.Split(keys, "+")
	var mods []string
	var key string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		lower := strings.ToLower(p)
		switch lower {
		case "cmd", "command", "ctrl", "control":
			mods = append(mods, "KEY_LEFTCTRL")
		case "alt", "option":
			mods = append(mods, "KEY_LEFTALT")
		case "shift":
			mods = append(mods, "KEY_LEFTSHIFT")
		case "super", "win", "meta":
			mods = append(mods, "KEY_LEFTMETA")
		default:
			if code, ok := ydoKeyCodes[lower]; ok {
				key = code
			} else if len(p) == 1 {
				key = fmt.Sprintf("KEY_%s", strings.ToUpper(p))
			} else {
				key = p
			}
		}
	}
	allKeys := append(mods, key)
	for _, k := range allKeys {
		if _, err := ydoExec("key", k); err != nil {
			return "", fmt.Errorf("key: %w", err)
		}
	}
	for i := len(mods) - 1; i >= 0; i-- {
		if _, err := ydoExec("key", mods[i]); err != nil {
			return "", fmt.Errorf("key release: %w", err)
		}
	}
	return fmt.Sprintf("Pressed via ydotool: %s", keys), nil
}

// --- Window bounds helper (crop screenshot to app window) ---

func cropImageToBounds(data []byte, x, y, w, h int) ([]byte, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, nil
	}
	bounds := img.Bounds()
	r := image.Rect(x, y, x+w, y+h).Intersect(bounds)
	if r.Empty() {
		return data, nil
	}
	dst := image.NewRGBA(r)
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	var buf bytes.Buffer
	if format == "jpeg" {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return data, nil
		}
	} else {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return data, nil
		}
	}
	return buf.Bytes(), nil
}

func getWindowBounds(app string) (string, error) {
	return osaExec(fmt.Sprintf(`tell app "System Events"
		set appProc to first process whose name contains "%s"
		set appWin to window 1 of appProc
		set {x, y, w, h} to position and size of appWin
		return (x as text) & "," & (y as text) & "," & (w as text) & "," & (h as text)
	end tell`, app))
}
