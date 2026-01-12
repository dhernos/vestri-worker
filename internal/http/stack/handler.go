package stack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"vestri-worker/internal/http/fs"
	"vestri-worker/internal/settings"
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
		logStackOpError(r, "up", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stackName := filepath.Base(stackPath)

	out, err := RunCompose(stackPath, "up", "-d")
	if err != nil {
		logStackOpError(r, "up", stackName, err)
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
	logStackOp(r, "up", stackName)
}

func StackDownHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, err := parseStackName(r)
	if err != nil {
		logStackOpError(r, "down", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stackName := filepath.Base(stackPath)

	out, err := RunCompose(stackPath, "down")
	if err != nil {
		logStackOpError(r, "down", stackName, err)
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
	logStackOp(r, "down", stackName)
}

func StackRestartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, err := parseStackName(r)
	if err != nil {
		logStackOpError(r, "restart", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stackName := filepath.Base(stackPath)

	if out, err := RunCompose(stackPath, "down"); err != nil {
		logStackOpError(r, "restart down", stackName, err)
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	out, err := RunCompose(stackPath, "up", "-d")
	if err != nil {
		logStackOpError(r, "restart up", stackName, err)
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
	logStackOp(r, "restart", stackName)
}

func StackStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, err := parseStackName(r)
	if err != nil {
		logStackOpError(r, "status", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stackName := filepath.Base(stackPath)

	out, err := RunCompose(stackPath, "ps")
	if err != nil {
		logStackOpError(r, "status", stackName, err)
		http.Error(w, out, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(out))
	logStackOp(r, "status", stackName)
}
