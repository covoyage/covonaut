package filequeue

import (
	"os"
	"path/filepath"
	"sync"
)

// fileLock is a per-path mutex with a reference count so the owning
// FileMutationQueue can remove it from its map once nobody is using it
// anymore, instead of retaining an entry forever for every path ever seen.
type fileLock struct {
	mu   sync.Mutex
	refs int
}

// FileMutationQueue serializes write operations per real file path.
type FileMutationQueue struct {
	mu     sync.Mutex
	queues map[string]*fileLock
}

func New() *FileMutationQueue {
	return &FileMutationQueue{queues: make(map[string]*fileLock)}
}

// WithFile executes fn while holding the mutex for the resolved real path.
// The per-path lock entry is reference-counted and removed from the internal
// map once the last concurrent caller for that path finishes, so the map
// does not grow without bound as new/short-lived paths are processed over
// the lifetime of the queue.
func (fmq *FileMutationQueue) WithFile(path string, fn func() error) error {
	key := resolveKey(path)

	fmq.mu.Lock()
	l, ok := fmq.queues[key]
	if !ok {
		l = &fileLock{}
		fmq.queues[key] = l
	}
	l.refs++
	fmq.mu.Unlock()

	l.mu.Lock()
	err := fn()
	l.mu.Unlock()

	fmq.mu.Lock()
	l.refs--
	if l.refs == 0 {
		delete(fmq.queues, key)
	}
	fmq.mu.Unlock()

	return err
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
