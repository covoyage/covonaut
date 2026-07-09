package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := resolvePython(""); err != nil {
		t.Skip("no python interpreter available: " + err.Error())
	}
}

// echoInvoker is a minimal test double standing in for
// agentcore.Agent.InvokeTool: it reflects the "msg" argument of an "echo"
// tool call back, prefixed, as a JSON-encoded string (matching what the
// real InvokeTool returns -- Executor.coreExecute marshals non-string tool
// results to JSON), so tests can verify the RPC round-trip end to end,
// including the JSON-decode-back-to-dict step on the Go side.
func echoInvoker() func(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return func(ctx context.Context, name string, args json.RawMessage) (string, error) {
		if name != "echo" {
			return "", fmt.Errorf("tool %q not found", name)
		}
		var in struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"echoed": "got:" + in.Msg})
		return string(b), nil
	}
}

func runCode(t *testing.T, cfg *ExecuteCodeToolConfig, code string) map[string]any {
	t.Helper()
	tool := NewExecuteCodeTool(cfg)
	argsJSON, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := tool.Func(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("execute_code returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", out)
	}
	return result
}

func TestExecuteCodePTCCallsWhitelistedTool(t *testing.T) {
	requirePython(t)

	cfg := &ExecuteCodeToolConfig{
		AllowedTools: []string{"echo"},
		ToolInvoker:  echoInvoker(),
	}

	result := runCode(t, cfg, `
import covo_tools
r = covo_tools.echo(msg="hi")
print(r["echoed"])
`)

	if result["status"] != "success" {
		t.Fatalf("expected success, got %+v", result)
	}
	output, _ := result["output"].(string)
	if !strings.Contains(output, "got:hi") {
		t.Fatalf("expected output to contain %q, got %q (stderr=%v)", "got:hi", output, result["stderr"])
	}
}

func TestExecuteCodePTCDisallowedToolRejected(t *testing.T) {
	requirePython(t)

	cfg := &ExecuteCodeToolConfig{
		AllowedTools: []string{"some_other_tool"}, // "echo" is NOT allowed
		ToolInvoker:  echoInvoker(),
	}

	result := runCode(t, cfg, `
import covo_tools
try:
    covo_tools.call_tool("echo", msg="hi")
    print("UNEXPECTED_SUCCESS")
except RuntimeError as e:
    print("blocked:", e)
`)

	output, _ := result["output"].(string)
	if strings.Contains(output, "UNEXPECTED_SUCCESS") {
		t.Fatalf("expected disallowed tool call to be rejected, got %q", output)
	}
	if !strings.Contains(output, "not allowed") {
		t.Fatalf("expected rejection message mentioning 'not allowed', got %q", output)
	}
}

func TestExecuteCodePTCDisabledWithoutInvoker(t *testing.T) {
	requirePython(t)

	// No ToolInvoker set (nil) -- PTC must be fully disabled, matching
	// pre-PTC behavior: no covo_tools module should exist for the script.
	cfg := &ExecuteCodeToolConfig{}

	result := runCode(t, cfg, `
try:
    import covo_tools
    print("UNEXPECTED_IMPORT_SUCCESS")
except ImportError:
    print("no covo_tools, as expected")
`)

	output, _ := result["output"].(string)
	if !strings.Contains(output, "no covo_tools, as expected") {
		t.Fatalf("expected covo_tools to be unavailable when ToolInvoker is nil, got %q (stderr=%v)", output, result["stderr"])
	}
}

func TestExecuteCodePTCMaxToolCallsEnforced(t *testing.T) {
	requirePython(t)

	cfg := &ExecuteCodeToolConfig{
		AllowedTools: []string{"echo"},
		MaxToolCalls: 1,
		ToolInvoker:  echoInvoker(),
	}

	result := runCode(t, cfg, `
import covo_tools
print(covo_tools.echo(msg="one")["echoed"])
try:
    covo_tools.echo(msg="two")
    print("UNEXPECTED_SECOND_CALL_SUCCESS")
except RuntimeError as e:
    print("limited:", e)
`)

	output, _ := result["output"].(string)
	if strings.Contains(output, "UNEXPECTED_SECOND_CALL_SUCCESS") {
		t.Fatalf("expected second call to be rejected by MaxToolCalls, got %q", output)
	}
	if !strings.Contains(output, "limit exceeded") {
		t.Fatalf("expected a call-limit error, got %q", output)
	}
}

func TestExecuteCodePTCInvokerErrorPropagates(t *testing.T) {
	requirePython(t)

	cfg := &ExecuteCodeToolConfig{
		AllowedTools: []string{"echo"},
		ToolInvoker: func(ctx context.Context, name string, args json.RawMessage) (string, error) {
			return "", fmt.Errorf("boom: simulated hook rejection")
		},
	}

	result := runCode(t, cfg, `
import covo_tools
try:
    covo_tools.echo(msg="hi")
    print("UNEXPECTED_SUCCESS")
except RuntimeError as e:
    print("propagated:", e)
`)

	output, _ := result["output"].(string)
	if !strings.Contains(output, "boom: simulated hook rejection") {
		t.Fatalf("expected invoker error to propagate to the script, got %q", output)
	}
}

func TestGeneratePTCStubSkipsInvalidIdentifiers(t *testing.T) {
	stub := generatePTCStub([]string{"read", "web-search", "call_tool"})
	if !strings.Contains(stub, "def read(**kwargs):") {
		t.Fatalf("expected named wrapper for 'read', got:\n%s", stub)
	}
	if strings.Contains(stub, "def web-search") {
		t.Fatalf("must not emit an invalid Python identifier as a function name:\n%s", stub)
	}
}

// Ensure resolvePython still resolves normally (sanity check that requirePython's
// skip logic works as intended in this environment).
func TestResolvePythonAvailable(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	got, err := resolvePython("")
	if err != nil {
		t.Fatalf("resolvePython: %v", err)
	}
	if got == "" {
		t.Fatalf("expected non-empty python command")
	}
}
