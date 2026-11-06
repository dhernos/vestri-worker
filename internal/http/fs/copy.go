package fs

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"vestri-worker/internal/settings"
)

func CopyFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveRequestBytes())
	var body struct {
		Source string `json:"source"`
		Dest   string `json:"dest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Source == "" || body.Dest == "" {
		http.Error(w, "source and destination are required", http.StatusBadRequest)
		return
	}

	base := settings.Get().FsBasePath
	sourcePath, err := safePath(base, body.Source)
	if err != nil {
		logPathOpError(r, "copy", body.Source, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	destPath, err := safePath(base, body.Dest)
	if err != nil {
		logPathOpError(r, "copy", body.Dest, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "source file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "cannot access source file", http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "source must be a regular file", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		http.Error(w, "cannot create destination directory", http.StatusInternalServerError)
		return
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		http.Error(w, "cannot open source file", http.StatusInternalServerError)
		return
	}
	defer source.Close()

	temp, err := os.CreateTemp(filepath.Dir(destPath), ".copy-*")
	if err != nil {
		http.Error(w, "cannot create temporary file", http.StatusInternalServerError)
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, source); err != nil {
		_ = temp.Close()
		http.Error(w, "cannot copy source file", http.StatusInternalServerError)
		return
	}
	if err := temp.Close(); err != nil {
		http.Error(w, "cannot finalize copied file", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		http.Error(w, "cannot place copied file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	logPathOp(r, "copy", body.Dest)
}