package a2a

import (
	"context"
	"testing"
	"time"
)

func TestServerShutdownIsIdempotent(t *testing.T) {
	server := NewServer(nil, WithTaskTTL(time.Second))
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestHTTPServerHasDefensiveTimeouts(t *testing.T) {
	httpServer := NewServer(nil).newHTTPServer(":0")
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
