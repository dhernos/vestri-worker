package http

import (
	"encoding/json"
	"net/http"
	"time"
)

type HealthStatus struct {
	Status          string `json:"status"`
	ExternalService string `json:"external_service"`
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	status := HealthStatus{
		Status:          "OK",
		ExternalService: "OK",
	}

	// Einfacher Ping zu Google
	client := http.Client{
		Timeout: 2 * time.Second,
	}
	resp, err := client.Head("https://www.google.com")
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil || resp.StatusCode != http.StatusOK {
		status.ExternalService = "NOT OK"
		status.Status = "NOT OK"
	}

	w.Header().Set("Content-Type", "application/json")
	if status.Status != "OK" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(status)
}
