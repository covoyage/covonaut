package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

var errClientClosed = errors.New("mcp client closed")

type runtimeEventSink struct {
	mu   sync.RWMutex
	emit func(agentcore.Event)
}

func (s *runtimeEventSink) Set(emit func(agentcore.Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emit = emit
}

func (s *runtimeEventSink) Emit(event agentcore.Event) {
	if event == nil {
		return
	}
	s.mu.RLock()
	emit := s.emit
	s.mu.RUnlock()
	if emit != nil {
		emit(event)
	}
}

func mergeContext(ctx context.Context, shutdown context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if shutdown == nil {
		return context.WithCancel(ctx)
	}
	merged, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(shutdown, cancel)
	return merged, func() {
		stop()
		cancel()
	}
}
