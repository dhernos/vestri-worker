package backup

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"vestri-worker/internal/settings"
)

const backupDirectory = ".vestri-backups"

var validRootName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type Policy struct {
	Enabled         bool   `json:"enabled"`
	EveryDays       int    `json:"everyDays"`
	Time            string `json:"time"`
	KeepDailyDays   int    `json:"keepDailyDays"`
	KeepWeeklyWeeks int    `json:"keepWeeklyWeeks"`
}

type Entry struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

type State struct {
	Policy  Policy  `json:"policy"`
	Backups []Entry `json:"backups"`
}

var rootLocks sync.Map

func DefaultPolicy() Policy {
	return Policy{Enabled: true, EveryDays: 1, Time: "04:00", KeepDailyDays: 7, KeepWeeklyWeeks: 4}
}

func ServerPath(root string) (string, error) {
	return serverRoot(root)
}

func ValidatePolicy(policy Policy) error {
	if policy.EveryDays < 1 || policy.EveryDays > 365 {
		return errors.New("backup interval must be between 1 and 365 days")
	}
	if _, err := time.Parse("15:04", policy.Time); err != nil {
		return errors.New("backup time must use HH:MM")
	}
	if policy.KeepDailyDays < 1 || policy.KeepDailyDays > 90 {
		return errors.New("daily retention must be between 1 and 90 days")
	}
	if policy.KeepWeeklyWeeks < 0 || policy.KeepWeeklyWeeks > 52 {
		return errors.New("weekly retention must be between 0 and 52 weeks")
	}
	return nil
}

func GetState(root string) (State, error) {
	rootPath, err := serverRoot(root)
	if err != nil {
		return State{}, err
	}
	policy, err := readPolicy(rootPath)
	if err != nil {
		return State{}, err
	}
	entries, err := listEntries(rootPath)
	if err != nil {
		return State{}, err
	}
	return State{Policy: policy, Backups: entries}, nil
}

func SetPolicy(root string, policy Policy) error {
	if err := ValidatePolicy(policy); err != nil {
		return err
	}
	rootPath, err := serverRoot(root)
	if err != nil {
		return err
	}
	lock := lockFor(rootPath)
	lock.Lock()
	defer lock.Unlock()
	if err := writePolicy(rootPath, policy); err != nil {
		return err
	}
	return prune(rootPath, policy)
}

func Create(root string) (Entry, error) {
	rootPath, err := serverRoot(root)
	if err != nil {
		return Entry{}, err
	}
	lock := lockFor(rootPath)
	lock.Lock()
	defer lock.Unlock()
	policy, err := readPolicy(rootPath)
	if err != nil {
		return Entry{}, err
	}
	dataPath := filepath.Join(rootPath, "data")
	info, err := os.Lstat(dataPath)
	if err != nil {
		return Entry{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Entry{}, errors.New("server data path must be a directory")
	}
	archiveDir, err := ensureBackupDir(rootPath, true)
	if err != nil {
		return Entry{}, err
	}
	temp, err := os.CreateTemp(archiveDir, ".backup-*.zip")
	if err != nil {
		return Entry{}, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := writeZip(temp, dataPath); err != nil {
		_ = temp.Close()
		return Entry{}, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return Entry{}, err
	}
	if err := temp.Close(); err != nil {
		return Entry{}, err
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	name := "backup-" + createdAt.Format("20060102-150405") + ".zip"
	dest := filepath.Join(archiveDir, name)
	for suffix := 1; ; suffix++ {
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return Entry{}, err
		}
		name = fmt.Sprintf("backup-%s-%d.zip", createdAt.Format("20060102-150405"), suffix)
		dest = filepath.Join(archiveDir, name)
	}
	if err := os.Rename(tempName, dest); err != nil {
		return Entry{}, err
	}
	if err := prune(rootPath, policy); err != nil {
		return Entry{}, err
	}
	info, err = os.Stat(dest)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: name, Size: info.Size(), CreatedAt: createdAt}, nil
}

func Delete(root, name string) error {
	rootPath, err := serverRoot(root)
	if err != nil {
		return err
	}
	if !validArchiveName(name) {
		return errors.New("invalid backup name")
	}
	lock := lockFor(rootPath)
	lock.Lock()
	defer lock.Unlock()
	directory, err := ensureBackupDir(rootPath, false)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(directory, name))
}

func ArchivePath(root, name string) (string, error) {
	rootPath, err := serverRoot(root)
	if err != nil {
		return "", err
	}
	if !validArchiveName(name) {
		return "", errors.New("invalid backup name")
	}
	directory, err := ensureBackupDir(rootPath, false)
	if err != nil {
		return "", err
	}
	archivePath := filepath.Join(directory, name)
	info, err := os.Lstat(archivePath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup is not a regular file")
	}
	return archivePath, nil
}

func Import(root, filename string, source io.Reader, maxBytes int64) (Entry, error) {
	rootPath, err := serverRoot(root)
	if err != nil {
		return Entry{}, err
	}
	lock := lockFor(rootPath)
	lock.Lock()
	defer lock.Unlock()
	policy, err := readPolicy(rootPath)
	if err != nil {
		return Entry{}, err
	}
	archiveDir, err := ensureBackupDir(rootPath, true)
	if err != nil {
		return Entry{}, err
	}
	temp, err := os.CreateTemp(archiveDir, ".upload-*.zip")
	if err != nil {
		return Entry{}, err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	limited := io.LimitReader(source, maxBytes+1)
	bytesWritten, err := io.Copy(temp, limited)
	if err != nil {
		_ = temp.Close()
		return Entry{}, err
	}
	if bytesWritten == 0 || bytesWritten > maxBytes {
		_ = temp.Close()
		return Entry{}, errors.New("backup archive size is invalid")
	}
	if err := temp.Close(); err != nil {
		return Entry{}, err
	}
	if err := validateArchive(tempName); err != nil {
		return Entry{}, err
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	name := fmt.Sprintf("upload-%s.zip", createdAt.Format("20060102-150405"))
	dest := filepath.Join(archiveDir, name)
	for suffix := 1; ; suffix++ {
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return Entry{}, err
		}
		name = fmt.Sprintf("upload-%s-%d.zip", createdAt.Format("20060102-150405"), suffix)
		dest = filepath.Join(archiveDir, name)
	}
	if err := os.Rename(tempName, dest); err != nil {
		return Entry{}, err
	}
	if err := prune(rootPath, policy); err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: name, Size: info.Size(), CreatedAt: createdAt}, nil
}

func Restore(root, name string, maxBytes int64, maxEntries int) error {
	rootPath, err := serverRoot(root)
	if err != nil {
		return err
	}
	if !validArchiveName(name) {
		return errors.New("invalid backup name")
	}
	lock := lockFor(rootPath)
	lock.Lock()
	defer lock.Unlock()
	archivePath, err := ArchivePath(root, name)
	if err != nil {
		return err
	}
	dataPath := filepath.Join(rootPath, "data")
	stagePath, err := os.MkdirTemp(rootPath, ".vestri-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagePath)
	if err := extractArchive(archivePath, stagePath, maxBytes, maxEntries); err != nil {
		return err
	}
	previousPath := filepath.Join(rootPath, ".vestri-data-previous")
	if err := os.RemoveAll(previousPath); err != nil {
		return err
	}
	if err := os.Rename(dataPath, previousPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(stagePath, dataPath); err != nil {
		_ = os.Rename(previousPath, dataPath)
		return err
	}
	return os.RemoveAll(previousPath)
}

func StartScheduler() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := runDueBackups(); err != nil {
				fmt.Printf("backup scheduler: %v\n", err)
			}
		}
	}()
}

func runDueBackups() error {
	base := settings.Get().FsBasePath
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	var firstErr error
	for _, entry := range entries {
		if !entry.IsDir() || !validRootName.MatchString(entry.Name()) {
			continue
		}
		rootPath := filepath.Join(base, entry.Name())
		dataInfo, err := os.Lstat(filepath.Join(rootPath, "data"))
		if err != nil || !dataInfo.IsDir() || dataInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		policy, err := readPolicy(rootPath)
		if err != nil || !policy.Enabled || ValidatePolicy(policy) != nil {
			continue
		}
		now := time.Now()
		backupTime, err := time.ParseInLocation("15:04", policy.Time, now.Location())
		if err != nil {
			continue
		}
		dueAt := time.Date(now.Year(), now.Month(), now.Day(), backupTime.Hour(), backupTime.Minute(), 0, 0, now.Location())
		if now.Before(dueAt) {
			continue
		}
		last, err := listEntries(rootPath)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(last) > 0 && calendarDaysBetween(last[0].CreatedAt.In(now.Location()), now) < policy.EveryDays {
			continue
		}
		if _, err := Create(entry.Name()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func calendarDaysBetween(older, newer time.Time) int {
	y1, m1, d1 := older.Date()
	y2, m2, d2 := newer.Date()
	start := time.Date(y1, m1, d1, 0, 0, 0, 0, newer.Location())
	end := time.Date(y2, m2, d2, 0, 0, 0, 0, newer.Location())
	return int(end.Sub(start).Hours() / 24)
}

func writeZip(destination *os.File, sourcePath string) error {
	archive := zip.NewWriter(destination)
	walkErr := filepath.WalkDir(sourcePath, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup data contains a symlink: %s", name)
		}
		relative, err := filepath.Rel(sourcePath, name)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if entry.IsDir() {
			header.Name += "/"
			_, err = archive.CreateHeader(header)
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("backup data contains an unsupported file: %s", name)
		}
		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeErr := archive.Close()
	if walkErr != nil {
		return walkErr
	}
	return closeErr
}

func extractArchive(archivePath, destination string, maxBytes int64, maxEntries int) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	if maxEntries > 0 && len(archive.File) > maxEntries {
		return errors.New("backup archive has too many entries")
	}
	var total uint64
	for _, entry := range archive.File {
		clean := filepath.Clean(filepath.FromSlash(entry.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(entry.Name, "\\") {
			return errors.New("backup archive contains an unsafe path")
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() && !entry.FileInfo().IsDir() {
			return errors.New("backup archive contains an unsupported entry")
		}
		if entry.UncompressedSize64 > ^uint64(0)-total {
			return errors.New("backup archive size overflows")
		}
		total += entry.UncompressedSize64
		if maxBytes > 0 && total > uint64(maxBytes) {
			return errors.New("backup archive expands beyond the configured limit")
		}
		target := filepath.Join(destination, clean)
		if !within(destination, target) {
			return errors.New("backup archive contains an unsafe path")
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, entry.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		outputCloseErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		if outputCloseErr != nil {
			return outputCloseErr
		}
	}
	return nil
}

func validateArchive(name string) error {
	archive, err := zip.OpenReader(name)
	if err != nil {
		return errors.New("uploaded file is not a valid zip archive")
	}
	defer archive.Close()
	for _, entry := range archive.File {
		clean := filepath.Clean(filepath.FromSlash(entry.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(entry.Name, "\\") || entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("uploaded archive contains an unsafe entry")
		}
	}
	return nil
}

func within(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func serverRoot(root string) (string, error) {
	if !validRootName.MatchString(root) {
		return "", errors.New("invalid server root")
	}
	base, err := filepath.Abs(settings.Get().FsBasePath)
	if err != nil {
		return "", err
	}
	rootPath := filepath.Join(base, root)
	info, err := os.Lstat(rootPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("server root must be a directory")
	}
	return rootPath, nil
}

func readPolicy(rootPath string) (Policy, error) {
	directory, err := ensureBackupDir(rootPath, false)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, err
	}
	policyPath := filepath.Join(directory, "settings.json")
	data, err := os.ReadFile(policyPath)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func writePolicy(rootPath string, policy Policy) error {
	directory, err := ensureBackupDir(rootPath, true)
	if err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filepath.Join(directory, "settings.json"))
}

func listEntries(rootPath string) ([]Entry, error) {
	directory, err := ensureBackupDir(rootPath, false)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	backups := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if !validArchiveName(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		backups = append(backups, Entry{Name: entry.Name(), Size: info.Size(), CreatedAt: info.ModTime().UTC()})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].CreatedAt.Equal(backups[j].CreatedAt) {
			return backups[i].Name > backups[j].Name
		}
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	return backups, nil
}

func prune(rootPath string, policy Policy) error {
	entries, err := listEntries(rootPath)
	if err != nil {
		return err
	}
	now := time.Now()
	dailyCutoff := now.AddDate(0, 0, -policy.KeepDailyDays)
	weeklyCutoff := dailyCutoff.AddDate(0, 0, -policy.KeepWeeklyWeeks*7)
	seenWeeks := make(map[string]bool)
	for _, entry := range entries {
		created := entry.CreatedAt.In(now.Location())
		remove := false
		switch {
		case !created.Before(dailyCutoff):
			// Keep every recent archive. A user can create several manual backups
			// on the same day, and each one must remain restorable for the full
			// daily retention period.
		case !created.Before(weeklyCutoff):
			year, week := created.ISOWeek()
			key := fmt.Sprintf("%04d-%02d", year, week)
			remove = seenWeeks[key]
			seenWeeks[key] = true
		default:
			remove = true
		}
		if remove {
			if err := os.Remove(filepath.Join(rootPath, backupDirectory, entry.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func validArchiveName(name string) bool {
	return filepath.Base(name) == name && strings.HasSuffix(strings.ToLower(name), ".zip") && !strings.HasPrefix(name, ".")
}

func ensureBackupDir(rootPath string, create bool) (string, error) {
	directory := filepath.Join(rootPath, backupDirectory)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup directory must be a real directory")
	}
	return directory, nil
}

func lockFor(rootPath string) *sync.Mutex {
	lock, _ := rootLocks.LoadOrStore(rootPath, &sync.Mutex{})
	return lock.(*sync.Mutex)
}
