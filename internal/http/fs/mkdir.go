package fs

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"vestri-worker/internal/settings"
)

func MkdirHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveRequestBytes())

	var body struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Exclusive bool   `json:"exclusive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		logPathOpError(r, "mkdir", body.Path, err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.Path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, body.Path)
	if err != nil {
		logPathOpError(r, "mkdir", body.Path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := mkdirPath(fullPath, body.Recursive, body.Exclusive); err != nil {
		logPathOpError(r, "mkdir", body.Path, err)
		if errors.Is(err, os.ErrExist) {
			http.Error(w, "path already exists", http.StatusConflict)
			return
		}
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "parent directory does not exist", http.StatusBadRequest)
			return
		}
		http.Error(w, "cannot create directory", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	logPathOp(r, "mkdir", body.Path)
}

func mkdirPath(fullPath string, recursive, exclusive bool) error {
	if recursive {
		if exclusive {
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return err
			}
			return os.Mkdir(fullPath, 0755)
		}
		return os.MkdirAll(fullPath, 0755)
	}

	if exclusive {
		return os.Mkdir(fullPath, 0755)
	}

	if err := os.Mkdir(fullPath, 0755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() {
			return err
		}
	}
	return nil
}
