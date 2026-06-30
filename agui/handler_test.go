package agui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestHandlerCapabilities(t *testing.T) {
	cfg := agentcore.Config{}
	cfg.Name = "test-agent"
	cfg.Streaming = true

	h := NewHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/agui", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var caps AgentCapabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	if caps.Identity.Name != "test-agent" {
		t.Errorf("expected 'test-agent', got %s", caps.Identity.Name)
	}
	if !caps.Transport.Streaming {
		t.Error("expected streaming")
	}
}

func TestHandlerRunNoUserMessage(t *testing.T) {
	cfg := agentcore.Config{}
	h := NewHandler(cfg)

	body := `{"messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "RUN_ERROR") {
		t.Errorf("expected RUN_ERROR event, got %s", string(data))
	}
	if !strings.Contains(string(data), "no user message provided") {
		t.Errorf("expected 'no user message provided' error, got %s", string(data))
	}
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	cfg := agentcore.Config{}
	h := NewHandler(cfg)

	req := httptest.NewRequest(http.MethodDelete, "/agui", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHandlerInvalidBody(t *testing.T) {
	cfg := agentcore.Config{}
	h := NewHandler(cfg)

	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandlerUpdateConfig(t *testing.T) {
	cfg := agentcore.Config{}
	cfg.Name = "original"

	h := NewHandler(cfg)

	cfg2 := agentcore.Config{}
	cfg2.Name = "updated"
	h.UpdateConfig(cfg2)

	snap := h.snapshotConfig()
	if snap.Name != "updated" {
		t.Errorf("expected 'updated', got %s", snap.Name)
	}
}

func TestHandlerSSEFormat(t *testing.T) {
	cfg := agentcore.Config{}
	h := NewHandler(cfg)

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	resp := w.Result()
	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", ct)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "no provider configured") {
		t.Errorf("expected 'no provider configured' error, got %s", string(data))
	}
}
