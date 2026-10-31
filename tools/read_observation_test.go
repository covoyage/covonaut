package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type observedReadOps struct {
	data     []byte
	err      error
	observed []byte
	partial  bool
	calls    int
	pinned   ReadOperations
}

func (o *observedReadOps) ReadFile(string) ([]byte, error)     { return o.data, o.err }
func (*observedReadOps) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (o *observedReadOps) SnapshotReadOperations() ReadOperations {
	if o.pinned != nil {
		return o.pinned
	}
	return o
}
func (o *observedReadOps) ObserveTextRead(_ string, data []byte, partial bool) error {
	o.calls++
	o.observed = append([]byte(nil), data...)
	o.partial = partial
	return nil
}

func TestTextReadObserverUsesPinnedOriginalBytesBeforeWindowAndTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("disk bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	pinned := &observedReadOps{data: []byte("first line\nsecond line\nthird line")}
	root := &observedReadOps{data: []byte("wrong backend bytes"), pinned: pinned}
	tool := NewReadTool(dir, &ReadToolConfig{Operations: root, MaxBytes: 4})
	if _, err := tool.Func(context.Background(), []byte(`{"path":"file.txt","offset":2,"limit":1}`)); err != nil {
		t.Fatal(err)
	}
	if pinned.calls != 1 || root.calls != 0 || !pinned.partial || !reflect.DeepEqual(pinned.observed, pinned.data) {
		t.Fatal("observation used truncated/projected bytes or the wrong backend")
	}
}

func TestTextReadObserverNeverObservesFailureDirectoryMediaOrCancellation(t *testing.T) {
	for _, kind := range []string{"failure", "directory", "binary", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "file.txt")
			if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
				t.Fatal(err)
			}
			ops := &observedReadOps{data: []byte("actual data")}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "failure":
				ops.err = errors.New("read failed")
			case "directory":
				path = dir
			case "binary":
				ops.data = []byte{0, 1, 2, 3}
			case "cancelled":
				cancel()
			}
			tool := NewReadTool(dir, &ReadToolConfig{Operations: ops})
			args := []byte(`{"path":"` + filepath.ToSlash(path) + `"}`)
			tool.Func(ctx, args)
			if ops.calls != 0 {
				t.Fatal("unsuccessful/non-text read minted observation")
			}
		})
	}
}
