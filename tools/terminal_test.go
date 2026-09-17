//go:build !windows

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testTerminalRegistry(t *testing.T) *terminalRegistry {
	t.Helper()
	reg := newTerminalRegistry(&TerminalToolConfig{
		DefaultReadTimeout: 5 * time.Second,
	})
	return reg
}

func callTerminal(t *testing.T, fn func(context.Context, json.RawMessage) (any, error), args string) map[string]any {
	t.Helper()
	raw := json.RawMessage(args)
	if args == "" {
		raw = json.RawMessage(`{}`)
	}
	v, err := fn(context.Background(), raw)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", v)
	}
	return m
}

// readUntil keeps calling readTool until needle appears or time runs out.
func readUntil(t *testing.T, reg *terminalRegistry, id, needle string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out string
	for time.Now().Before(deadline) {
		res := callTerminal(t, reg.readTool, `{"session_id":"`+id+`","timeout_seconds":2}`)
		out += res["output"].(string)
		if strings.Contains(out, needle) {
			return out
		}
	}
	t.Fatalf("timed out waiting for %q; got output: %q", needle, out)
	return ""
}

func TestTerminalSessionPersists(t *testing.T) {
	reg := testTerminalRegistry(t)

	// Open a session that immediately exports a variable and cds.
	open := callTerminal(t, reg.openTool, `{"command":"export COVO_MARKER=hello-42; cd /tmp"}`)
	id, ok := open["session_id"].(string)
	if !ok || id == "" {
		t.Fatalf("no session id in open result: %v", open)
	}

	// Wait for the executed expansion, not the echoed command text.
	callTerminal(t, reg.sendTool, `{"session_id":"`+id+`","input":"echo \"READY:$COVO_MARKER:$(pwd)\""}`)
	readUntil(t, reg, id, "READY:hello-42:/tmp", 10*time.Second)

	// A second round trip on the same session proves state survived: the
	// variable is still exported and the cwd is still /tmp.
	callTerminal(t, reg.sendTool, `{"session_id":"`+id+`","input":"echo \"PERSIST:$COVO_MARKER:$(pwd)\""}`)
	readUntil(t, reg, id, "PERSIST:hello-42:/tmp", 10*time.Second)

	// list shows the live session.
	list := callTerminal(t, reg.listTool, "")
	sessions, ok := list["sessions"].([]map[string]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("expected one live session, got: %v", list["sessions"])
	}

	// Close and confirm cleanup.
	callTerminal(t, reg.closeTool, `{"session_id":"`+id+`","force":true}`)
	list = callTerminal(t, reg.listTool, "")
	if sessions, _ := list["sessions"].([]map[string]any); len(sessions) != 0 {
		t.Fatalf("session not removed from registry: %v", list["sessions"])
	}
}

func TestTerminalSignalInterrupts(t *testing.T) {
	reg := testTerminalRegistry(t)

	open := callTerminal(t, reg.openTool, `{"command":"sleep 300"}`)
	id := open["session_id"].(string)
	// Wait until the sleep is actually the foreground job.
	time.Sleep(500 * time.Millisecond)

	callTerminal(t, reg.signalTool, `{"session_id":"`+id+`","signal":"SIGINT"}`)

	// After SIGINT the sleep is interrupted and the shell must still be
	// interactive: a follow-up command produces output and the session
	// reports itself as running.
	callTerminal(t, reg.sendTool, `{"session_id":"`+id+`","input":"echo after-sigint-$((1+1))"}`)
	out := readUntil(t, reg, id, "after-sigint-2", 10*time.Second)
	_ = out

	res := callTerminal(t, reg.readTool, `{"session_id":"`+id+`","timeout_seconds":1}`)
	if res["status"] != "running" {
		t.Fatalf("session did not survive SIGINT: %v", res)
	}

	callTerminal(t, reg.closeTool, `{"session_id":"`+id+`","force":true}`)
}

func TestTerminalUnknownSession(t *testing.T) {
	reg := testTerminalRegistry(t)
	if _, err := reg.readTool(context.Background(), json.RawMessage(`{"session_id":"nope"}`)); err == nil {
		t.Fatal("expected error for unknown session")
	}
}
