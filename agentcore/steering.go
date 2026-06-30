package agentcore

import "sync"

// SteeringMode controls how multiple queued messages are drained.
type SteeringMode string

const (
	// SteeringAll drains all pending messages at once.
	SteeringAll SteeringMode = "all"
	// SteeringOneAtATime drains one message per LLM turn.
	SteeringOneAtATime SteeringMode = "one_at_a_time"
)

// messageQueue is a thread-safe queue of messages used for
// steering (interrupt) and follow-up (queue after current turn) injection.
type messageQueue struct {
	mu   sync.Mutex
	msgs []Message
	mode SteeringMode
}

func newMessageQueue(mode SteeringMode) *messageQueue {
	if mode == "" {
		mode = SteeringAll
	}
	return &messageQueue{mode: mode}
}

// Push adds a message to the queue.
func (q *messageQueue) Push(msg Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = append(q.msgs, msg)
}

// Drain returns and removes pending messages according to the configured mode.
func (q *messageQueue) Drain() []Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.msgs) == 0 {
		return nil
	}
	switch q.mode {
	case SteeringOneAtATime:
		msg := q.msgs[0]
		q.msgs = q.msgs[1:]
		return []Message{msg}
	default:
		msgs := q.msgs
		q.msgs = nil
		return msgs
	}
}

// Len returns the number of pending messages.
func (q *messageQueue) Len() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return int64(len(q.msgs))
}
