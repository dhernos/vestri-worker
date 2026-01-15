package fs

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"vestri-worker/internal/settings"
)

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
	mr, err := r.MultipartReader()
	if err != nil {
		logPathOpError(r, "upload", "", err)
		if isMaxBytesErr(err) {
			http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	var path string
	uploaded := false

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			logPathOpError(r, "upload", path, err)
			if isMaxBytesErr(err) {
				http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid multipart form", http.StatusBadRequest)
			return
		}

		switch part.FormName() {
		case "path":
			value, err := readFormValue(part, 4096)
			_ = part.Close()
			if err != nil {
				logPathOpError(r, "upload", "", err)
				if isMaxBytesErr(err) {
					http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "invalid multipart form", http.StatusBadRequest)
				return
			}
			path = value
		case "file":
			if path == "" {
				_ = part.Close()
				logPathOpError(r, "upload", "", errors.New("missing path"))
				http.Error(w, "missing path", http.StatusBadRequest)
				return
			}
			if !saveUploadPart(w, r, path, part) {
				_ = part.Close()
				return
			}
			_ = part.Close()
			uploaded = true
		default:
			_ = part.Close()
		}
	}

	if !uploaded {
		logPathOpError(r, "upload", path, errors.New("missing file"))
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func saveUploadPart(w http.ResponseWriter, r *http.Request, path string, part *multipart.Part) bool {
	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, path)
	if err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return false
	}

	out, err := os.Create(fullPath)
	if err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		return false
	}
	defer out.Close()

	if _, err := io.Copy(out, part); err != nil {
		_ = os.Remove(fullPath)
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot write file", http.StatusInternalServerError)
		return false
	}

	logPathOp(r, "upload", path)
	return true
}

func readFormValue(part *multipart.Part, limit int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, limit))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func isMaxBytesErr(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}
