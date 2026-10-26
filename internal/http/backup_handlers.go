package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"vestri-worker/internal/backup"
	"vestri-worker/internal/http/stack"
	"vestri-worker/internal/settings"
)

var startBackupScheduler sync.Once

func init() {
	backup.WithServerStopped = stack.WithStackStopped
}

func startScheduler() {
	startBackupScheduler.Do(backup.StartScheduler)
}

func backupStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state, err := backup.GetState(r.URL.Query().Get("root"))
	if err != nil {
		backupError(w, err)
		return
	}
	writeBackupJSON(w, state)
}

func backupSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Root   string        `json:"root"`
		Policy backup.Policy `json:"policy"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := backup.SetPolicy(req.Root, req.Policy); err != nil {
		backupError(w, err)
		return
	}
	state, err := backup.GetState(req.Root)
	if err != nil {
		backupError(w, err)
		return
	}
	writeBackupJSON(w, state)
}

func backupCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Root string `json:"root"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	entry, err := backup.Create(req.Root)
	if err != nil {
		backupError(w, err)
		return
	}
	writeBackupJSON(w, entry)
}

func backupDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Root string `json:"root"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := backup.Delete(req.Root, req.Name); err != nil {
		backupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func backupRestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Root string `json:"root"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	stackPath, err := backup.ServerPath(req.Root)
	if err != nil {
		backupError(w, err)
		return
	}
	output, err := stack.RunCompose(stackPath, "down")
	if err != nil {
		http.Error(w, fmt.Sprintf("could not stop server: %s", strings.TrimSpace(output)), http.StatusConflict)
		return
	}
	limits := settings.Get()
	maxUnzipBytes := limits.MaxUnzipBytes
	if maxUnzipBytes <= 0 {
		maxUnzipBytes = 10 << 30
	}
	maxZipEntries := limits.MaxZipEntries
	if maxZipEntries <= 0 {
		maxZipEntries = 100000
	}
	if err := backup.Restore(req.Root, req.Name, maxUnzipBytes, maxZipEntries); err != nil {
		backupError(w, err)
		return
	}
	writeBackupJSON(w, map[string]string{"status": "down"})
}

func backupDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Query().Get("name")
	filePath, err := backup.ArchivePath(r.URL.Query().Get("root"), name)
	if err != nil {
		backupError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(name)))
	http.ServeFile(w, r, filePath)
}

func backupUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := settings.Get().MaxUploadBytes
	if limit <= 0 {
		limit = 1 << 30
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "invalid multipart upload", http.StatusBadRequest)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	root := r.FormValue("path")
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "backup file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	entry, err := backup.Import(root, header.Filename, file, limit)
	if err != nil {
		backupError(w, err)
		return
	}
	writeBackupJSON(w, entry)
}

func backupError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
	} else if !errors.Is(err, os.ErrInvalid) && !strings.Contains(strings.ToLower(err.Error()), "invalid") && !strings.Contains(strings.ToLower(err.Error()), "unsafe") && !strings.Contains(strings.ToLower(err.Error()), "must be") && !strings.Contains(strings.ToLower(err.Error()), "between") && !strings.Contains(strings.ToLower(err.Error()), "unsupported") && !strings.Contains(strings.ToLower(err.Error()), "too many") && !strings.Contains(strings.ToLower(err.Error()), "expands beyond") {
		status = http.StatusInternalServerError
	}
	http.Error(w, err.Error(), status)
}

func writeBackupJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}
