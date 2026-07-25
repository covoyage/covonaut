package server

import (
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestHTTPServerHasDefensiveTimeouts(t *testing.T) {
	httpServer := New(agentcore.Config{}).newHTTPServer(":0")
	if httpServer.ReadHeaderTimeout <= 0 || httpServer.ReadTimeout <= 0 || httpServer.IdleTimeout <= 0 {
		t.Fatalf("missing defensive timeout: %+v", httpServer)
	}
	if httpServer.MaxHeaderBytes <= 0 {
		t.Fatal("MaxHeaderBytes must be bounded")
	}
	if httpServer.WriteTimeout != 0 {
		t.Fatal("WriteTimeout must remain unset for SSE responses")
	}
}
