package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"vestri-worker/internal/database"
	"vestri-worker/internal/settings"
)

func databaseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	manager, err := database.New(settings.Get().FsBasePath)
	if err != nil {
		http.Error(w, "database storage is unavailable", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	var state database.State
	if r.Method == http.MethodGet {
		state, err = manager.List(ctx, r.URL.Query().Get("owner"), r.URL.Query().Get("stack"))
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var request struct {
			Owner   string `json:"owner"`
			Stack   string `json:"stack"`
			Name    string `json:"name"`
			Engine  string `json:"engine"`
			ID      string `json:"id"`
			Action  string `json:"action"`
			Confirm string `json:"confirm"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid database request", http.StatusBadRequest)
			return
		}
		if request.Action == "" {
			state, err = manager.Create(ctx, request.Owner, request.Stack, request.Name, request.Engine)
		} else {
			state, err = manager.Action(ctx, request.Owner, request.Stack, request.ID, request.Action, request.Confirm)
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
