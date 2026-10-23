package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"vestri-worker/internal/settings"
)

func ReadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, path)
	if err != nil {
		logPathOpError(r, "read", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Lstat(fullPath)
	if err != nil {
		logPathOpError(r, "read", path, err)
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		err := fmt.Errorf("path is not a regular file")
		logPathOpError(r, "read", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		logPathOpError(r, "read", path, err)
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	info, err = file.Stat()
	if err != nil {
		logPathOpError(r, "read", path, err)
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		err := fmt.Errorf("path is not a regular file")
		logPathOpError(r, "read", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	limit := maxInlineReadBytes()
	if info.Size() > limit {
		err := fmt.Errorf("file exceeds inline read limit")
		logPathOpError(r, "read", path, err)
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		logPathOpError(r, "read", path, err)
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}
	if int64(len(data)) == limit {
		var extra [1]byte
		n, readErr := file.Read(extra[:])
		if n > 0 {
			err := fmt.Errorf("file exceeds inline read limit")
			logPathOpError(r, "read", path, err)
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		if readErr != nil && readErr != io.EOF {
			logPathOpError(r, "read", path, readErr)
			http.Error(w, "cannot read file", http.StatusInternalServerError)
			return
		}
	}

	if _, err := w.Write(data); err != nil {
		logPathOpError(r, "read", path, err)
		return
	}
	logPathOp(r, "read", path)
}

func WriteFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxInlineWriteBytes())

	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, body.Path)
	if err != nil {
		logPathOpError(r, "write", body.Path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		logPathOpError(r, "write", body.Path, err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(fullPath, []byte(body.Content), 0644); err != nil {
		logPathOpError(r, "write", body.Path, err)
		http.Error(w, "cannot write file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	logPathOp(r, "write", body.Path)
}
