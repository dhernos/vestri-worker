package http

import (
	"log"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/fs/read", readFileHandler)
	mux.HandleFunc("/fs/write", writeFileHandler)

	log.Printf("listening on %s \n", addr)
	return http.ListenAndServe(addr, mux)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func readFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	fullPath, err := safePath("/tmp/vestri", path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "cannot read file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func writeFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	fullPath, err := safePath("/tmp/vestri", body.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dir := filepath.Dir(fullPath)
	err = os.MkdirAll(dir, 0755)
	if err != nil {
		log.Printf("Error creating directories: %v", err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return
	}

	err = os.WriteFile(fullPath, []byte(body.Content), 0644)
	if err != nil {
		http.Error(w, "cannot write file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func safePath(base, userPath string) (string, error) {
	clean := filepath.Clean("/" + userPath)

	full := filepath.Join(base, clean)

	if !strings.HasPrefix(full, base) {
		return "", errors.New("invalid path")
	}

	return full, nil
}
