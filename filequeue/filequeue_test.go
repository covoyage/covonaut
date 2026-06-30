package filequeue

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	fmq := New()

	if err := fmq.WriteFileSafe(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFileSafe: %v", err)
	}

	data, err := fmq.ReadFileSafe(path)
	if err != nil {
		t.Fatalf("ReadFileSafe: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", string(data), "hello")
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.txt")

	fmq := New()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := fmq.WriteFileSafe(path, []byte("data"), 0644); err != nil {
				t.Errorf("WriteFileSafe: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	fmq := New()

	paths := []string{
		filepath.Join(dir, "a.txt"),
		filepath.Join(dir, "b.txt"),
	}
	var wg sync.WaitGroup

	for _, p := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if err := fmq.WriteFileSafe(path, []byte(path), 0644); err != nil {
				t.Errorf("WriteFileSafe: %v", err)
			}
		}(p)
	}
	wg.Wait()

	for _, p := range paths {
		data, err := fmq.ReadFileSafe(p)
		if err != nil {
			t.Fatalf("ReadFileSafe(%s): %v", p, err)
		}
		if string(data) != p {
			t.Errorf("%s: got %q", p, string(data))
		}
	}
}

func TestWithFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	fmq := New()
	called := false

	err := fmq.WithFile(path, func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithFile: %v", err)
	}
	if !called {
		t.Error("fn was not called")
	}
}

func TestWithFileCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", "file.txt")

	fmq := New()
	if err := fmq.WriteFileSafe(path, []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFileSafe nested: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestResolveKeySymlink(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	symlink := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(realFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", symlink); err != nil {
		t.Fatal(err)
	}

	key1 := resolveKey(realFile)
	key2 := resolveKey(symlink)
	if key1 != key2 {
		t.Errorf("resolveKey: symlink %q != real %q", key2, key1)
	}
}
