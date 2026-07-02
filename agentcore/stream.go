package agentcore

import (
	"strings"
	"sync"
)

// StreamReader[T] is a managed stream with explicit lifecycle control.
// Unlike raw channels, it tracks errors, supports Close() for producer cleanup,
// and prevents goroutine leaks by closing the stop channel when the consumer is finished.
type StreamReader[T any] struct {
	ch     chan T
	stop   chan struct{}
	err    error
	mu     sync.Mutex
	closed bool
}

// NewStreamReader creates a stream with the given buffer size.
func NewStreamReader[T any](bufSize int64) *StreamReader[T] {
	if bufSize <= 0 {
		bufSize = 1
	}
	return &StreamReader[T]{
		ch:   make(chan T, bufSize),
		stop: make(chan struct{}),
	}
}

// Send pushes a value into the stream. Returns false if the stream is closed or the consumer cancelled.
func (s *StreamReader[T]) Send(val T) bool {
	select {
	case s.ch <- val:
		return true
	case <-s.stop:
		return false
	}
}

// SetError records a terminal error and closes the stream.
func (s *StreamReader[T]) SetError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.err = err
	s.closed = true
	close(s.stop)
}

// Close signals end-of-stream from the producer side.
func (s *StreamReader[T]) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.stop)
}

// Recv reads the next value. Returns (zero, false) when the stream is exhausted.
func (s *StreamReader[T]) Recv() (T, bool) {
	select {
	case val := <-s.ch:
		return val, true
	default:
	}
	select {
	case val := <-s.ch:
		return val, true
	case <-s.stop:
		var zero T
		return zero, false
	}
}

// Cancel tells the producer the consumer no longer needs data.
// Producers watching the stop channel should stop sending.
func (s *StreamReader[T]) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

// Done returns a channel that is closed when the consumer cancels the stream or the stream is done.
func (s *StreamReader[T]) Done() <-chan struct{} {
	return s.stop
}

// Err returns the terminal error, if any.
func (s *StreamReader[T]) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Collect drains the stream into a slice.
func (s *StreamReader[T]) Collect() ([]T, error) {
	var result []T
	for {
		val, ok := s.Recv()
		if !ok {
			break
		}
		result = append(result, val)
	}
	return result, s.Err()
}

// Pipe connects this reader to another writer: every item received is forwarded.
// Blocks until the source is exhausted, then closes the target.
func (s *StreamReader[T]) Pipe(target *StreamReader[T]) error {
	defer target.Close()
	for {
		val, ok := s.Recv()
		if !ok {
			break
		}
		if !target.Send(val) {
			break
		}
	}
	if err := s.Err(); err != nil {
		target.SetError(err)
		return err
	}
	return nil
}

// Map transforms each element using fn and returns a new StreamReader.
func Map[I, O any](src *StreamReader[I], fn func(I) (O, error)) *StreamReader[O] {
	out := NewStreamReader[O](int64(cap(src.ch)))
	go func() {
		defer out.Close()
		for {
			val, ok := src.Recv()
			if !ok {
				break
			}
			mapped, err := fn(val)
			if err != nil {
				out.SetError(err)
				return
			}
			if !out.Send(mapped) {
				src.Cancel()
				return
			}
		}
		if err := src.Err(); err != nil {
			out.SetError(err)
		}
	}()
	return out
}

// NewStreamFromValue creates a single-element stream containing val.
func NewStreamFromValue[T any](val T) *StreamReader[T] {
	s := NewStreamReader[T](1)
	s.Send(val)
	s.Close()
	return s
}

// CollectString drains a string stream and joins all chunks.
func CollectString(s *StreamReader[string]) (string, error) {
	items, err := s.Collect()
	if err != nil {
		return "", err
	}
	return strings.Join(items, ""), nil
}

// Merge combines multiple StreamReaders into one. Items arrive in non-deterministic order.
func Merge[T any](readers ...*StreamReader[T]) *StreamReader[T] {
	out := NewStreamReader[T](int64(len(readers)))
	var wg sync.WaitGroup
	for _, r := range readers {
		wg.Add(1)
		go func(src *StreamReader[T]) {
			defer wg.Done()
			defer func() {
				if err := src.Err(); err != nil {
					out.SetError(err)
				}
			}()
			for {
				val, ok := src.Recv()
				if !ok {
					return
				}
				if !out.Send(val) {
					src.Cancel()
					return
				}
			}
		}(r)
	}
	go func() {
		wg.Wait()
		out.Close()
	}()
	return out
}
