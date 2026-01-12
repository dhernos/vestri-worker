package fs

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"vestri-worker/internal/settings"
)

const uploadFormMemory = 32 << 20

func DownloadFileHandler(w http.ResponseWriter, r *http.Request) {
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
		logPathOpError(r, "download", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		logPathOpError(r, "download", path, err)
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		logPathOpError(r, "download", path, fmt.Errorf("path is a directory"))
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(fullPath)))
	http.ServeFile(w, r, fullPath)
	logPathOp(r, "download", path)
}

func UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes())
	if err := r.ParseMultipartForm(uploadFormMemory); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	path := r.FormValue("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, path)
	if err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return
	}

	out, err := os.Create(fullPath)
	if err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		_ = out.Close()
		_ = os.Remove(fullPath)
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot write file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	logPathOp(r, "upload", path)
}
