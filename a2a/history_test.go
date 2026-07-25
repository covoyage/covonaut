package a2a

import (
	"testing"
	"time"
)

func TestPublishTaskUpdateRecordsOnceForMultipleSubscribers(t *testing.T) {
	server := NewServer(newMockHandler())
	first := server.subscribeToTask("task")
	second := server.subscribeToTask("task")
	defer server.unsubscribeFromTask("task", first)
	defer server.unsubscribeFromTask("task", second)

	server.PublishTaskUpdate("task", &TaskUpdateEvent{Result: &Task{ID: "task", State: TaskStateWorking}})
	firstEvent := <-first.events
	secondEvent := <-second.events
	firstEvent.ID = 1
	secondEvent.ID = 2

	state := server.getTaskState("task")
	state.mu.RLock()
	defer state.mu.RUnlock()
	if len(state.history) != 1 {
		t.Fatalf("history entries = %d, want 1", len(state.history))
	}
	if firstEvent == secondEvent || firstEvent.ID == secondEvent.ID {
		t.Fatal("subscribers must receive independent event copies")
	}
	if firstEvent.sequence != state.history[0].seq || secondEvent.sequence != state.history[0].seq {
		t.Fatal("subscriber sequence does not match recorded history")
	}
}

func TestPublishTaskUpdateQueuesBurstWithoutDropping(t *testing.T) {
	server := NewServer(newMockHandler())
	subscriber := server.subscribeToTask("burst")
	defer server.unsubscribeFromTask("burst", subscriber)

	const count = 100
	for i := 0; i < count; i++ {
		server.PublishTaskUpdate("burst", &TaskUpdateEvent{Result: &Task{ID: "burst", State: TaskStateWorking}, sequence: i + 1})
	}
	for i := 0; i < count; i++ {
		select {
		case event := <-subscriber.events:
			if event.sequence != i+1 {
				t.Fatalf("event %d sequence = %d", i, event.sequence)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d events", i)
		}
	}
}
