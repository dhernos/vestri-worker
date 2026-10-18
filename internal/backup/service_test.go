package backup

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"vestri-worker/internal/settings"
)

func configureTestRoot(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	previous := settings.Get()
	settings.Set(settings.Settings{FsBasePath: base})
	t.Cleanup(func() { settings.Set(previous) })
	root := filepath.Join(base, "sample-server")
	if err := os.MkdirAll(filepath.Join(root, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCreateAndRestoreRoundTrip(t *testing.T) {
	root := configureTestRoot(t)
	original := filepath.Join(root, "data", "world.txt")
	if err := os.WriteFile(original, []byte("original world"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := Create("sample-server")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !validArchiveName(entry.Name) {
		t.Fatalf("Create() returned invalid archive name %q", entry.Name)
	}
	if err := os.WriteFile(original, []byte("changed world"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "extra.txt"), []byte("remove me"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Restore("sample-server", entry.Name, 1<<20, 100); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	got, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original world" {
		t.Fatalf("restored file = %q, want original contents", got)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "extra.txt")); !os.IsNotExist(err) {
		t.Fatalf("extra file still exists after restore, stat error = %v", err)
	}
}

func TestImportRejectsTraversalEntry(t *testing.T) {
	configureTestRoot(t)
	var archiveBuffer bytes.Buffer
	writer := zip.NewWriter(&archiveBuffer)
	file, err := writer.Create("../outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "unsafe"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Import("sample-server", "unsafe.zip", bytes.NewReader(archiveBuffer.Bytes()), 1<<20); err == nil {
		t.Fatal("Import() accepted an archive containing a traversal path")
	}
}

func TestValidatePolicyRejectsInvalidValues(t *testing.T) {
	policy := DefaultPolicy()
	policy.Time = "25:00"
	if err := ValidatePolicy(policy); err == nil {
		t.Fatal("ValidatePolicy() accepted an invalid time")
	}
}