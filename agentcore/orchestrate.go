package agentcore

import (
	"context"
	"sync"
)

// MessageBus is a simple fan-out / fan-in message channel for orchestrating
// multiple agents. It is optional; use it when you want explicit routing
// between steps without sharing a single Agent state.
type MessageBus struct {
	mu   sync.Mutex
	subs map[string][]chan Message
}

// NewMessageBus creates an empty bus.
func NewMessageBus() *MessageBus {
	return &MessageBus{subs: make(map[string][]chan Message)}
}

// Publish delivers a copy of m to every subscriber of topic (non-blocking
// per subscriber: drops if channel buffer full).
func (b *MessageBus) Publish(topic string, m Message) {
	b.mu.Lock()
	chs := append([]chan Message(nil), b.subs[topic]...)
	b.mu.Unlock()
	for _, ch := range chs {
		select {
		case ch <- m:
		default:
		}
	}
}

// Subscribe returns a receive-only channel for topic with buffer cap, and
// cancel removes the subscription and closes the channel.
func (b *MessageBus) Subscribe(topic string, cap int) (recv <-chan Message, cancel func()) {
	ch := make(chan Message, cap)
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], ch)
	b.mu.Unlock()
	cancel = func() {
		b.mu.Lock()
		sl := b.subs[topic]
		out := sl[:0]
		for _, c := range sl {
			if c != ch {
				out = append(out, c)
			}
		}
		if len(out) == 0 {
			delete(b.subs, topic)
		} else {
			b.subs[topic] = out
		}
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

// SequentialAgentStep runs one agent after another, passing the previous
// agent's final output as the next agent's user message (unless empty).
func RunSequentialAgents(ctx context.Context, agents []*Agent, user string) (string, error) {
	var last string
	var err error
	for i, ag := range agents {
		input := user
		if i > 0 && last != "" {
			input = last
		}
		last, err = ag.Run(ctx, input)
		if err != nil {
			return "", err
		}
	}
	return last, nil
}
