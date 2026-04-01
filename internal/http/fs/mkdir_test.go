package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirPathExclusiveCreatesNewDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "server-a")

	if err := mkdirPath(target, false, true); err != nil {
		t.Fatalf("mkdirPath returned error: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("expected created directory, stat failed: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got mode %s", info.Mode())
	}
}

func TestMkdirPathExclusiveReturnsExistError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "server-a")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("failed to prepare target directory: %v", err)
	}

	err := mkdirPath(target, false, true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}
}

func TestMkdirPathRecursiveExclusiveCreatesParents(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "nested", "server-a")

	if err := mkdirPath(target, true, true); err != nil {
		t.Fatalf("mkdirPath returned error: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("expected created directory, stat failed: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got mode %s", info.Mode())
	}
}

func TestMkdirPathNonExclusiveIsIdempotentForExistingDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "server-a")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("failed to prepare target directory: %v", err)
	}

	if err := mkdirPath(target, false, false); err != nil {
		t.Fatalf("mkdirPath returned error: %v", err)
	}
}
