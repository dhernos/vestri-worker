package stack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"vestri-worker/internal/settings"
	"vestri-worker/internal/http/fs"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func parseStackName(r *http.Request) (string, error) {
	type Req struct {
		Stack string `json:"stack"`
	}
	var req Req

	if r.Method == http.MethodGet {
		req.Stack = r.URL.Query().Get("stack")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return "", fmt.Errorf("bad request: %w", err)
		}
	}

	if req.Stack == "" || !validName.MatchString(req.Stack) {
		return "", fmt.Errorf("invalid stack name")
	}

	settings := settings.Get()
	stackPath, err := fs.SafeSubPath(settings.FsBasePath, req.Stack)
	if err != nil {
		return "", fmt.Errorf("invalid stack path: %w", err)
	}

	if err := os.MkdirAll(stackPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create stack directory: %w", err)
	}

	return stackPath, nil
}

func StackUpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, err := parseStackName(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	out, err := RunCompose(stackPath, "up", "-d")
	if err != nil {
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
}

func StackDownHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, err := parseStackName(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	out, err := RunCompose(stackPath, "down")
	if err != nil {
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
}

func StackRestartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, err := parseStackName(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if out, err := RunCompose(stackPath, "down"); err != nil {
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	out, err := RunCompose(stackPath, "up", "-d")
	if err != nil {
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
}

func StackStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, err := parseStackName(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	out, err := RunCompose(stackPath, "ps")
	if err != nil {
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
}
