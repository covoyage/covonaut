package a2a

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJSONRPCNotificationHasNoResponse(t *testing.T) {
	server := NewServer(newMockHandler())
	body := []byte(`{"jsonrpc":"2.0","method":"tasks/query","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("notification response = status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestJSONRPCNotificationOnlyBatchHasNoResponse(t *testing.T) {
	server := NewServer(newMockHandler())
	body := []byte(`[{"jsonrpc":"2.0","method":"tasks/query","params":{}},{"jsonrpc":"2.0","method":"tasks/query","params":{}}]`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("notification batch response = status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestJSONRPCStreamingNotificationInBatchHasNoResponse(t *testing.T) {
	server := NewServer(newMockHandler())
	body := []byte(`[{"jsonrpc":"2.0","method":"SubscribeToTask","params":{"id":"missing"}}]`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("streaming notification batch response = status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestJSONRPCMixedBatchOmitsNotifications(t *testing.T) {
	server := NewServer(newMockHandler())
	body := []byte(`[{"jsonrpc":"2.0","method":"tasks/query","params":{}},{"jsonrpc":"2.0","id":1,"method":"tasks/query","params":{}}]`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if bytes.Count(rec.Body.Bytes(), []byte(`"jsonrpc"`)) != 1 {
		t.Fatalf("mixed batch should contain one response: %s", rec.Body.String())
	}
}
