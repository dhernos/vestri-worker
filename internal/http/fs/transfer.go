package fs

import (
	"errors"
	"fmt"
	"io"
	"log"
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

	log.Printf("fs upload start from=%s content_length=%d transfer_encoding=%v content_type=%q", r.RemoteAddr, r.ContentLength, r.TransferEncoding, r.Header.Get("Content-Type"))

	limited := http.MaxBytesReader(w, r.Body, maxUploadBytes())
	countingBody := &countingReader{r: limited}
	r.Body = io.NopCloser(countingBody)

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

	var uploadErr error
	var path string
	var uploadedBytes int64
	uploaded := false

	defer func() {
		log.Printf("fs upload end path=%q bytes_read=%d uploaded=%t uploaded_bytes=%d err=%v from=%s", path, countingBody.n, uploaded, uploadedBytes, uploadErr, r.RemoteAddr)
	}()

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
				uploadErr = err
				return
			}
			path = value
		case "file":
			if path == "" {
				_ = part.Close()
				logPathOpError(r, "upload", "", errors.New("missing path"))
				http.Error(w, "missing path", http.StatusBadRequest)
				uploadErr = errors.New("missing path")
				return
			}
			written, err := saveUploadPart(w, r, path, part)
			if err != nil {
				_ = part.Close()
				uploadErr = err
				return
			}
			uploadedBytes = written
			_ = part.Close()
			uploaded = true
		default:
			_ = part.Close()
		}
	}

	if !uploaded {
		logPathOpError(r, "upload", path, errors.New("missing file"))
		http.Error(w, "missing file", http.StatusBadRequest)
		uploadErr = errors.New("missing file")
		return
	}

	log.Printf("fs upload complete path=%q bytes=%d from=%s", path, uploadedBytes, r.RemoteAddr)
	w.WriteHeader(http.StatusOK)
}

func saveUploadPart(w http.ResponseWriter, r *http.Request, path string, part *multipart.Part) (int64, error) {
	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, path)
	if err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return 0, err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return 0, err
	}

	out, err := os.Create(fullPath)
	if err != nil {
		logPathOpError(r, "upload", path, err)
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		return 0, err
	}
	defer out.Close()

	written, err := io.Copy(out, part)
	if err != nil {
		_ = os.Remove(fullPath)
		logPathOpError(r, "upload", path, fmt.Errorf("copied=%d err=%w", written, err))
		http.Error(w, "cannot write file", http.StatusInternalServerError)
		return written, err
	}

	logPathOp(r, "upload", path)
	return written, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
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
