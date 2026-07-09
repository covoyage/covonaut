package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// defaultPTCAllowedTools is the conservative default set of tools a
// sandboxed execute_code script may call via Programmatic Tool Calling
// (PTC) when no explicit AllowedTools list is configured. Read-only and
// web tools only — no bash/write/execute_code itself, to avoid recursive
// or destructive access from an unsupervised script.
var defaultPTCAllowedTools = []string{"read", "grep", "glob", "web_search", "web_fetch"}

// ptcIdentifierRe matches strings that are safe to emit as a Python
// identifier (used for generating named wrapper functions in the stub).
var ptcIdentifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type ptcRequest struct {
	ID    int             `json:"id"`
	Token string          `json:"token"`
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args"`
}

type ptcResponse struct {
	ID     int    `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ptcServer is the parent-side RPC listener that lets a sandboxed
// execute_code script call back into a whitelisted subset of agent tools
// without their responses ever entering the LLM's context — only the
// script's own stdout does. Each call is a single, one-shot TCP
// request/response (the client half-closes its write side after sending;
// the server replies and closes). This keeps the Python stub trivial (no
// framing or multiplexing needed).
//
// Calls are dispatched through invoke (typically agentcore.Agent.InvokeTool)
// rather than calling a Tool's Func directly, so PTC calls get the exact
// same audit logging, guardrails, and other hooks as normal model-issued
// tool calls -- the sandboxed script cannot bypass them.
type ptcServer struct {
	listener net.Listener
	port     int
	token    string
	allowed  map[string]bool
	invoke   func(ctx context.Context, name string, args json.RawMessage) (string, error)
	maxCalls int64
	calls    int64
}

// newPTCServer starts listening on a random loopback port. The caller must
// eventually call Close (or cancel the context passed to Serve, which
// closes the listener too).
func newPTCServer(allowedTools []string, invoke func(ctx context.Context, name string, args json.RawMessage) (string, error), maxCalls int) (*ptcServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("PTC: failed to start RPC listener: %w", err)
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		listener.Close()
		return nil, fmt.Errorf("PTC: failed to generate token: %w", err)
	}
	if len(allowedTools) == 0 {
		allowedTools = defaultPTCAllowedTools
	}
	allowed := make(map[string]bool, len(allowedTools))
	for _, n := range allowedTools {
		allowed[n] = true
	}
	if maxCalls <= 0 {
		maxCalls = 50
	}
	return &ptcServer{
		listener: listener,
		port:     listener.Addr().(*net.TCPAddr).Port,
		token:    hex.EncodeToString(tokenBytes),
		allowed:  allowed,
		invoke:   invoke,
		maxCalls: int64(maxCalls),
	}, nil
}

// Serve accepts connections until ctx is cancelled (which closes the
// listener) or the listener is closed some other way. Intended to run in
// its own goroutine; returns once Accept starts failing.
func (s *ptcServer) Serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(ctx, conn)
	}
}

func (s *ptcServer) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	data, err := io.ReadAll(conn)
	if err != nil || len(data) == 0 {
		return
	}
	var req ptcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		s.reply(conn, ptcResponse{Error: "invalid request"})
		return
	}
	if req.Token != s.token {
		s.reply(conn, ptcResponse{ID: req.ID, Error: "unauthorized"})
		return
	}
	if atomic.AddInt64(&s.calls, 1) > s.maxCalls {
		s.reply(conn, ptcResponse{ID: req.ID, Error: fmt.Sprintf("tool call limit exceeded (max %d per execution)", s.maxCalls)})
		return
	}
	if !s.allowed[req.Tool] {
		s.reply(conn, ptcResponse{ID: req.ID, Error: fmt.Sprintf("tool %q is not allowed inside sandboxed scripts", req.Tool)})
		return
	}
	raw, err := s.invoke(ctx, req.Tool, req.Args)
	if err != nil {
		s.reply(conn, ptcResponse{ID: req.ID, Error: err.Error()})
		return
	}
	// Most tools return a JSON-encodable value that the executor marshals to
	// a string; decode it back so the script gets a native dict/list rather
	// than a raw JSON string. Tools that genuinely return plain text fall
	// back to that text unchanged.
	var decoded any
	if json.Unmarshal([]byte(raw), &decoded) == nil {
		s.reply(conn, ptcResponse{ID: req.ID, Result: decoded})
	} else {
		s.reply(conn, ptcResponse{ID: req.ID, Result: raw})
	}
}

func (s *ptcServer) reply(conn net.Conn, resp ptcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	conn.Write(b)
}

func (s *ptcServer) Close() error {
	return s.listener.Close()
}

// allowedToolNames returns the tool names actually in effect (after
// defaults were applied), for stub generation.
func (s *ptcServer) allowedToolNames() []string {
	names := make([]string, 0, len(s.allowed))
	for n := range s.allowed {
		names = append(names, n)
	}
	return names
}

// ptcDescriptionSuffix returns the extra sentence appended to the
// execute_code tool description when PTC is enabled, so the model knows
// the covo_tools stub is available and which tools it can call.
func ptcDescriptionSuffix(cfg *ExecuteCodeToolConfig) string {
	if cfg.ToolInvoker == nil {
		return ""
	}
	allowed := cfg.AllowedTools
	if len(allowed) == 0 {
		allowed = defaultPTCAllowedTools
	}
	return fmt.Sprintf(
		" The script may also `import covo_tools` to call these tools directly by RPC "+
			"(covo_tools.call_tool(name, **kwargs) or the named wrappers, e.g. covo_tools.%s(...)) "+
			"-- results stay out of your context; only what the script prints reaches you. Allowed: %s.",
		firstIdentifier(allowed), strings.Join(allowed, ", "),
	)
}

// firstIdentifier returns the first Python-identifier-safe name in names,
// or "read" as a harmless example fallback.
func firstIdentifier(names []string) string {
	for _, n := range names {
		if ptcIdentifierRe.MatchString(n) {
			return n
		}
	}
	return "read"
}

func generatePTCStub(allowedTools []string) string {
	var b strings.Builder
	b.WriteString(`"""Auto-generated by covo-agent. Lets this script call a whitelisted
subset of agent tools via RPC, without their responses entering the LLM's
context -- only what this script prints to stdout does.
"""
import json
import os
import socket

_PORT = int(os.environ["COVO_TOOLS_PORT"])
_TOKEN = os.environ["COVO_TOOLS_TOKEN"]
_next_id = [0]


def _call(tool, **kwargs):
    _next_id[0] += 1
    req = json.dumps({"id": _next_id[0], "token": _TOKEN, "tool": tool, "args": kwargs}).encode("utf-8")
    with socket.create_connection(("127.0.0.1", _PORT), timeout=30) as sock:
        sock.sendall(req)
        try:
            sock.shutdown(socket.SHUT_WR)
        except OSError:
            pass
        chunks = []
        while True:
            chunk = sock.recv(65536)
            if not chunk:
                break
            chunks.append(chunk)
    resp = json.loads(b"".join(chunks).decode("utf-8"))
    if resp.get("error"):
        raise RuntimeError(f"tool {tool!r} failed: {resp['error']}")
    return resp.get("result")


def call_tool(name, **kwargs):
    """Call any whitelisted tool by name."""
    return _call(name, **kwargs)

`)
	for _, name := range allowedTools {
		if !ptcIdentifierRe.MatchString(name) {
			continue // not a valid Python identifier; still reachable via call_tool()
		}
		fmt.Fprintf(&b, "\ndef %s(**kwargs):\n    return _call(%q, **kwargs)\n", name, name)
	}
	return b.String()
}
