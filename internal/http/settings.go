package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"vestri-worker/internal/settings"
)

const settingsPath = "/etc/vestri/settings.json"

func settingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {

	case http.MethodGet:
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			http.Error(w, "cannot read settings", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)

	case http.MethodPost:
		// Body als map decoden
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		// Alte Settings laden
		curr := settings.Get()

		// Felder überschreiben
		if v, ok := updates["http_port"].(string); ok {
			curr.HTTPPort = v
		}
		if v, ok := updates["fs_base_path"].(string); ok {
			curr.FsBasePath = v
		}

		// Datei schreiben
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
			http.Error(w, "cannot create settings dir", http.StatusInternalServerError)
			return
		}
		data, _ := json.MarshalIndent(curr, "", "  ")
		if err := os.WriteFile(settingsPath, data, 0644); err != nil {
			http.Error(w, "cannot write settings", http.StatusInternalServerError)
			return
		}

		settings.Set(curr)

		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
