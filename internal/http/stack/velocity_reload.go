package stack

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func velocityReloadSucceeded(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "vestri_dynamic_sync_ok")
}

func StackVelocityReloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	stackPath, stackName, err := parseStackTarget(r)
	if err != nil {
		http.Error(w, "invalid stack", http.StatusBadRequest)
		return
	}
	defer LockStack(stackPath)()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cmd, terminal, err := startComposeAttachPTY(ctx, stackPath, "minecraft", terminalSize{Cols: 120, Rows: 30})
	if err != nil {
		logStackOpError(r, "velocity reload", stackName, err)
		http.Error(w, "Velocity console is unavailable; the proxy must be running", http.StatusBadGateway)
		return
	}
	defer func() { cancel(); _ = terminal.Close(); _ = cmd.Wait() }()
	if _, err = terminal.Write([]byte("vestridynamic sync\n")); err != nil {
		http.Error(w, "Failed to send Velocity synchronization command", http.StatusBadGateway)
		return
	}
	result := make(chan bool, 1)
	go func() {
		buffer := make([]byte, 4096)
		output := ""
		for {
			count, readErr := terminal.Read(buffer)
			output += string(buffer[:count])
			if velocityReloadSucceeded(output) {
				result <- true
				return
			}
			if strings.Contains(output, "VESTRI_DYNAMIC_SYNC_FAILED") || strings.Contains(strings.ToLower(output), "unknown command") {
				result <- false
				return
			}
			if len(output) > 65536 {
				output = output[len(output)-32768:]
			}
			if readErr != nil {
				result <- false
				return
			}
		}
	}()
	select {
	case success := <-result:
		if !success {
			http.Error(w, "Velocity bridge sync failed; install the Vestri Dynamic Servers plugin", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		logStackOp(r, "velocity reload", stackName)
	case <-ctx.Done():
		http.Error(w, "Velocity bridge sync timed out; install the Vestri Dynamic Servers plugin", http.StatusGatewayTimeout)
	}
}
