package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransferEntry(t *testing.T) {
	for _, operation := range []string{"copy", "move", "rename"} {
		t.Run(operation, func(t *testing.T) {
			base := t.TempDir()
			if err := os.Mkdir(filepath.Join(base, "source"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "source", "file.txt"), []byte("hello"), 0640); err != nil {
				t.Fatal(err)
			}
			if err := transferEntry(base, "source", "destination", operation); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(base, "destination", "file.txt"))
			if err != nil || string(content) != "hello" {
				t.Fatalf("content = %q, err = %v", content, err)
			}
			_, err = os.Stat(filepath.Join(base, "source"))
			if operation == "copy" && err != nil {
				t.Fatal("copy removed source")
			}
			if operation != "copy" && !os.IsNotExist(err) {
				t.Fatal("move retained source")
			}
		})
	}
}

func TestTransferEntryRejectsUnsafeTargets(t *testing.T) {
	for _, operation := range []string{"copy", "move", "rename"} {
		for _, destination := range []string{"source", "source/nested", "existing", "../escape", "."} {
			t.Run(operation+"/"+destination, func(t *testing.T) {
				base := t.TempDir()
				if err := os.Mkdir(filepath.Join(base, "source"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(base, "existing"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := transferEntry(base, "source", destination, operation); err == nil {
					t.Fatal("expected rejection")
				}
				content, err := os.ReadFile(filepath.Join(base, "existing"))
				if err != nil || string(content) != "keep" {
					t.Fatal("existing entry changed")
				}
			})
		}
	}
}

func TestTransferEntryRejectsNestedSymlink(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "source"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(base, "source", "link")); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"copy", "move"} {
		if err := transferEntry(base, "source", "destination", operation); err == nil {
			t.Fatal("expected symlink rejection")
		}
	}
}
