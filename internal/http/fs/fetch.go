package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vestri-worker/internal/settings"
)

const defaultExternalFetchTimeout = 10 * time.Minute

var externalFetchClient = &http.Client{
	Timeout: defaultExternalFetchTimeout,
}

func FetchRemoteFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveRequestBytes())

	var req struct {
		Path     string `json:"path"`
		URL      string `json:"url"`
		MaxBytes int64  `json:"maxBytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	req.Path = strings.TrimSpace(req.Path)
	req.URL = strings.TrimSpace(req.URL)
	if req.Path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	parsedURL, err := url.ParseRequestURI(req.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		logPathOpError(r, "fetch", req.Path, fmt.Errorf("invalid external url"))
		http.Error(w, "invalid external url", http.StatusBadRequest)
		return
	}

	maxAllowedBytes := maxUploadBytes()
	if req.MaxBytes > 0 && req.MaxBytes < maxAllowedBytes {
		maxAllowedBytes = req.MaxBytes
	}

	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, req.Path)
	if err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultExternalFetchTimeout)
	defer cancel()

	externalReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "failed to build fetch request", http.StatusInternalServerError)
		return
	}
	externalReq.Header.Set("User-Agent", "vestri-worker/1")

	resp, err := externalFetchClient.Do(externalReq)
	if err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "failed to download file", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		logPathOpError(r, "fetch", req.Path, fmt.Errorf("status=%d", resp.StatusCode))
		http.Error(w, strings.TrimSpace(string(body)), http.StatusBadGateway)
		return
	}
	if resp.ContentLength > maxAllowedBytes {
		logPathOpError(r, "fetch", req.Path, fmt.Errorf("download exceeds size limit"))
		http.Error(w, "download exceeds size limit", http.StatusRequestEntityTooLarge)
		return
	}

	tempFile, err := os.CreateTemp(filepath.Dir(fullPath), ".fetch-*")
	if err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "cannot create temporary file", http.StatusInternalServerError)
		return
	}
	tempName := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempName)
	}()

	limitedBody := &io.LimitedReader{
		R: resp.Body,
		N: maxAllowedBytes + 1,
	}
	if _, err := io.Copy(tempFile, limitedBody); err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "cannot write downloaded file", http.StatusInternalServerError)
		return
	}
	if limitedBody.N == 0 {
		logPathOpError(r, "fetch", req.Path, fmt.Errorf("download exceeds size limit"))
		http.Error(w, "download exceeds size limit", http.StatusRequestEntityTooLarge)
		return
	}
	if err := tempFile.Close(); err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "cannot finalize downloaded file", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tempName, fullPath); err != nil {
		logPathOpError(r, "fetch", req.Path, err)
		http.Error(w, "cannot move downloaded file", http.StatusInternalServerError)
		return
	}

	_ = os.Remove(tempName)
	w.WriteHeader(http.StatusOK)
	logPathOp(r, "fetch", req.Path)
}
