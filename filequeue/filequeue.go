package filequeue

import (
	"os"
	"path/filepath"
	"sync"
)

// FileMutationQueue serializes write operations per real file path.
type FileMutationQueue struct {
	mu     sync.Mutex
	queues map[string]*sync.Mutex
}

func New() *FileMutationQueue {
	return &FileMutationQueue{queues: make(map[string]*sync.Mutex)}
}

// WithFile executes fn while holding the mutex for the resolved real path.
func (fmq *FileMutationQueue) WithFile(path string, fn func() error) error {
	key := resolveKey(path)

	fmq.mu.Lock()
	m, ok := fmq.queues[key]
	if !ok {
		m = &sync.Mutex{}
		fmq.queues[key] = m
	}
	fmq.mu.Unlock()

	m.Lock()
	defer m.Unlock()
	return fn()
}

// WithFileResult is a generic version of WithFile that returns a value.
func WithFileResult[T any](fmq *FileMutationQueue, path string, fn func() (T, error)) (T, error) {
	var result T
	err := fmq.WithFile(path, func() error {
		var fnErr error
		result, fnErr = fn()
		return fnErr
	})
	return result, err
}

func resolveKey(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(realDir, base)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// ReadFileSafe reads a file through the mutation queue.
func (fmq *FileMutationQueue) ReadFileSafe(path string) ([]byte, error) {
	return WithFileResult(fmq, path, func() ([]byte, error) {
		return os.ReadFile(path)
	})
}

// WriteFileSafe writes a file through the mutation queue.
func (fmq *FileMutationQueue) WriteFileSafe(path string, data []byte, perm os.FileMode) error {
	return fmq.WithFile(path, func() error {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, perm)
	})
}
