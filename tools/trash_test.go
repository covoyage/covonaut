package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrashPathUsesOverrideDir(t *testing.T) {
	srcDir := t.TempDir()
	trashDir := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", trashDir)
	src := filepath.Join(srcDir, "note.txt")
	if err := os.WriteFile(src, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := TrashPath(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatal("source should be moved")
	}
	got, err := os.ReadFile(filepath.Join(trashDir, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("trashed content = %q", got)
	}
}

func TestTrashPathCollisionSuffix(t *testing.T) {
	srcDir := t.TempDir()
	trashDir := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", trashDir)
	if err := os.WriteFile(filepath.Join(trashDir, "note.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "note.txt")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := TrashPath(src); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected collision rename, got %d entries", len(entries))
	}
}

func TestTrashPathRefusesCWD(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", t.TempDir())
	t.Chdir(dir)
	if err := TrashPath(dir); err == nil {
		t.Fatal("expected refusal for cwd")
	}
}
