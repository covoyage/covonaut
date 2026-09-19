package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaybeTrashShellCommandMovesFile(t *testing.T) {
	cwd := t.TempDir()
	trashDir := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", trashDir)
	path := filepath.Join(cwd, "gone.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := maybeTrashShellCommand("rm -- gone.txt", cwd); err != nil {
		t.Fatalf("trash rm: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("source should be gone")
	}
	if _, err := os.Lstat(filepath.Join(trashDir, "gone.txt")); err != nil {
		t.Fatalf("expected trash copy: %v", err)
	}
}

func TestMaybeTrashShellCommandRecursiveDir(t *testing.T) {
	cwd := t.TempDir()
	trashDir := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", trashDir)
	dir := filepath.Join(cwd, "tree")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := maybeTrashShellCommand("rm -rf tree", cwd); err != nil {
		t.Fatalf("trash rm -rf: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("directory should be gone")
	}
	if _, err := os.Lstat(filepath.Join(trashDir, "tree", "nested")); err != nil {
		t.Fatalf("expected trashed tree: %v", err)
	}
}

func TestMaybeTrashShellCommandForceMissing(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", t.TempDir())
	if err := maybeTrashShellCommand("rm -f missing.txt", cwd); err != nil {
		t.Fatalf("rm -f missing should succeed: %v", err)
	}
}

func TestMaybeTrashShellCommandMissingWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", t.TempDir())
	err := maybeTrashShellCommand("rm missing.txt", cwd)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestMaybeTrashShellCommandFallsThroughCompound(t *testing.T) {
	if err := maybeTrashShellCommand("rm a && echo hi", t.TempDir()); err != errNotRecoverableDelete {
		t.Fatalf("compound command should fall through, got %v", err)
	}
}

func TestMaybeTrashShellCommandBlocksShred(t *testing.T) {
	err := maybeTrashShellCommand("shred secret.txt", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "irrecoverable") {
		t.Fatalf("expected shred block, got %v", err)
	}
}

func TestDefaultBashOperationsTrashIntercept(t *testing.T) {
	cwd := t.TempDir()
	trashDir := t.TempDir()
	t.Setenv("COVO_TRASH_DIR", trashDir)
	path := filepath.Join(cwd, "file.txt")
	if err := os.WriteFile(path, []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, err := DefaultBashOperations{}.Exec("rm file.txt", cwd, nil, nil, func([]byte) {})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("file should have been trashed")
	}
}
