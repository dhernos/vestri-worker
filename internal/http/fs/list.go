package fs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"vestri-worker/internal/settings"
)

type listEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func ListDirHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}

	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, path)
	if err != nil {
		logPathOpError(r, "list", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		logPathOpError(r, "list", path, err)
		if os.IsNotExist(err) {
			http.Error(w, "directory not found", http.StatusNotFound)
			return
		}
		http.Error(w, "cannot access directory", http.StatusInternalServerError)
		return
	}
	if !info.IsDir() {
		logPathOpError(r, "list", path, fmt.Errorf("path is not a directory"))
		http.Error(w, "path is not a directory", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		logPathOpError(r, "list", path, err)
		http.Error(w, "cannot read directory", http.StatusInternalServerError)
		return
	}

	result := make([]listEntry, 0, len(entries))
	for _, entry := range entries {
		entryType := "file"
		if entry.IsDir() {
			entryType = "dir"
		} else if entry.Type()&os.ModeSymlink != 0 {
			entryType = "symlink"
		} else if !entry.Type().IsRegular() {
			entryType = "other"
		}

		size := int64(0)
		if info, err := entry.Info(); err == nil {
			size = info.Size()
		}

		result = append(result, listEntry{
			Name: entry.Name(),
			Type: entryType,
			Size: size,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		logPathOpError(r, "list", path, err)
		return
	}
	logPathOp(r, "list", path)
}
