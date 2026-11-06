package backup

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestCreateKeepsAllRecentManualBackups(t *testing.T) {
	root := configureTestRoot(t)
	if err := os.WriteFile(filepath.Join(root, "data", "world.txt"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}

	first, err := Create("sample-server")
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	second, err := Create("sample-server")
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if first.Name == second.Name {
		t.Fatalf("manual backups reused archive name %q", first.Name)
	}

	state, err := GetState("sample-server")
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if len(state.Backups) != 2 {
		t.Fatalf("recent manual backups = %d, want 2", len(state.Backups))
	}
}

func TestPruneKeepsRecentArchivesAndRollsOlderArchivesWeekly(t *testing.T) {
	root := configureTestRoot(t)
	backupDir := filepath.Join(root, backupDirectory)
	if err := os.Mkdir(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	weeklyDay := now.AddDate(0, 0, -8)
	weeklySecond := time.Date(weeklyDay.Year(), weeklyDay.Month(), weeklyDay.Day(), 12, 0, 0, 0, weeklyDay.Location())
	weeklyFirst := weeklySecond.Add(time.Hour)
	files := []struct {
		name string
		when time.Time
	}{
		{name: "backup-recent-first.zip", when: now.Add(-time.Hour)},
		{name: "backup-recent-second.zip", when: now.Add(-2 * time.Hour)},
		{name: "backup-week-first.zip", when: weeklyFirst},
		{name: "backup-week-second.zip", when: weeklySecond},
		{name: "backup-expired.zip", when: now.AddDate(0, 0, -40)},
	}
	for _, file := range files {
		archivePath := filepath.Join(backupDir, file.name)
		if err := os.WriteFile(archivePath, []byte("backup"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(archivePath, file.when, file.when); err != nil {
			t.Fatal(err)
		}
	}

	policy := Policy{KeepDailyDays: 7, KeepWeeklyWeeks: 4}
	if err := prune(root, policy); err != nil {
		t.Fatalf("prune() error = %v", err)
	}
	for _, name := range []string{"backup-recent-first.zip", "backup-recent-second.zip", "backup-week-first.zip"} {
		if _, err := os.Stat(filepath.Join(backupDir, name)); err != nil {
			t.Errorf("%s was removed unexpectedly: %v", name, err)
		}
	}
	for _, name := range []string{"backup-week-second.zip", "backup-expired.zip"} {
		if _, err := os.Stat(filepath.Join(backupDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s remains after pruning, stat error = %v", name, err)
		}
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
