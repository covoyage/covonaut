package agentcore

import (
	"strconv"
	"sync"
	"testing"
)

func TestMessageQueuePushAndDrain(t *testing.T) {
	q := newMessageQueue(SteeringAll)
	q.Push(Message{Role: RoleUser, Content: "hi"})
	q.Push(Message{Role: RoleUser, Content: "there"})
	msgs := q.Drain()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "hi" || msgs[1].Content != "there" {
		t.Fatalf("unexpected content: %v", msgs)
	}
}

func TestMessageQueueDrainEmpty(t *testing.T) {
	q := newMessageQueue(SteeringAll)
	msgs := q.Drain()
	if msgs != nil {
		t.Fatalf("expected nil, got %v", msgs)
	}
}

func TestMessageQueueOneAtATime(t *testing.T) {
	q := newMessageQueue(SteeringOneAtATime)
	q.Push(Message{Role: RoleUser, Content: "a"})
	q.Push(Message{Role: RoleUser, Content: "b"})

	msgs1 := q.Drain()
	if len(msgs1) != 1 || msgs1[0].Content != "a" {
		t.Fatalf("expected [a], got %v", msgs1)
	}

	msgs2 := q.Drain()
	if len(msgs2) != 1 || msgs2[0].Content != "b" {
		t.Fatalf("expected [b], got %v", msgs2)
	}

	msgs3 := q.Drain()
	if msgs3 != nil {
		t.Fatalf("expected nil after draining all, got %v", msgs3)
	}
}

func TestMessageQueueDefaultMode(t *testing.T) {
	q := newMessageQueue("") // empty should default to SteeringAll
	q.Push(Message{Role: RoleUser, Content: "x"})
	q.Push(Message{Role: RoleUser, Content: "y"})
	msgs := q.Drain()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages with default mode, got %d", len(msgs))
	}
}

func TestMessageQueueLen(t *testing.T) {
	q := newMessageQueue(SteeringAll)
	if q.Len() != 0 {
		t.Fatalf("expected 0, got %d", q.Len())
	}
	q.Push(Message{Role: RoleUser, Content: "a"})
	q.Push(Message{Role: RoleUser, Content: "b"})
	if q.Len() != 2 {
		t.Fatalf("expected 2, got %d", q.Len())
	}
	q.Drain()
	if q.Len() != 0 {
		t.Fatalf("expected 0 after drain, got %d", q.Len())
	}
}

func TestMessageQueueConcurrentSafety(t *testing.T) {
	q := newMessageQueue(SteeringAll)
	var wg sync.WaitGroup
	n := 100

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q.Push(Message{Role: RoleUser, Content: strconv.Itoa(i)})
		}(i)
	}
	wg.Wait()

	if q.Len() != int64(n) {
		t.Fatalf("expected %d messages, got %d", n, q.Len())
	}

	msgs := q.Drain()
	if len(msgs) != n {
		t.Fatalf("expected %d drained, got %d", n, len(msgs))
	}
}
