package fs

import (
	"encoding/json"
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}

	w.Write(data)
}

func WriteFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	base := settings.Get().FsBasePath
	fullPath, err := safePath(base, body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(fullPath, []byte(body.Content), 0644); err != nil {
		http.Error(w, "cannot write file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
