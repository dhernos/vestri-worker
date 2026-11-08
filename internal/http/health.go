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

	client := http.Client{
		Timeout: 2 * time.Second,
	}
	if !externalConnectivity(r, &client) {
		status.ExternalService = "NOT OK"
	}

	w.Header().Set("Content-Type", "application/json")
	if status.Status != "OK" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(status)
}

func externalConnectivity(r *http.Request, client *http.Client) bool {
	for _, endpoint := range []string{"https://www.github.com", "https://www.cloudflare.com"} {
		request, err := http.NewRequestWithContext(r.Context(), http.MethodHead, endpoint, nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			return true
		}
	}
	return false
}
