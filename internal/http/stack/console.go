package stack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	logStreamTailLines      = "200"
	execResolveServiceLimit = 20 * time.Second
	defaultPTYCols          = 120
	defaultPTYRows          = 32
)

type execWSMessage struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Code    int    `json:"code,omitempty"`
}

type flushStreamWriter struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

func (w *flushStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.w.Write(p)
	if n > 0 && w.flusher != nil {
		w.flusher.Flush()
	}
	return n, err
}

func StackLogsStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stackPath, stackName, err := parseStackTarget(r, false)
	if err != nil {
		logStackOpError(r, "logs stream", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	service, err := parseServiceName(r)
	if err != nil {
		logStackOpError(r, "logs stream", stackName, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	args := []string{"logs", "-f", "--no-color", "--tail=" + logStreamTailLines}
	if service != "" {
		args = append(args, service)
	}

	cmd, err := composeCommandContext(r.Context(), stackPath, args...)
	if err != nil {
		logStackOpError(r, "logs stream", stackName, err)
		http.Error(w, "failed to prepare compose logs command", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	streamWriter := &flushStreamWriter{
		w:       w,
		flusher: flusher,
	}
	cmd.Stdout = streamWriter
	cmd.Stderr = streamWriter

	if err := cmd.Start(); err != nil {
		logStackOpError(r, "logs stream", stackName, err)
		http.Error(w, "failed to start compose logs command", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	serviceLabel := service
	if serviceLabel == "" {
		serviceLabel = "all"
	}
	_, _ = io.WriteString(streamWriter, fmt.Sprintf("[vestri] live log stream connected (service=%s)\n", serviceLabel))

	log.Printf(
		"stack %s %s action=logs stream start stack=%q service=%q from=%s",
		r.Method,
		r.URL.Path,
		stackName,
		service,
		r.RemoteAddr,
	)

	err = cmd.Wait()
	if err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) || errors.Is(r.Context().Err(), context.DeadlineExceeded) {
			log.Printf(
				"stack %s %s action=logs stream stop stack=%q service=%q from=%s reason=client_disconnect",
				r.Method,
				r.URL.Path,
				stackName,
				service,
				r.RemoteAddr,
			)
			return
		}
		logStackOpError(r, "logs stream", stackName, err)
		return
	}

	log.Printf(
		"stack %s %s action=logs stream stop stack=%q service=%q from=%s reason=command_exit",
		r.Method,
		r.URL.Path,
		stackName,
		service,
		r.RemoteAddr,
	)
}

func StackExecWebSocketHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !ptySupported() {
		http.Error(w, "interactive console is not supported on this worker platform", http.StatusNotImplemented)
		return
	}

	stackPath, stackName, err := parseStackTarget(r, false)
	if err != nil {
		logStackOpError(r, "exec ws", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	service, err := parseServiceName(r)
	if err != nil {
		logStackOpError(r, "exec ws", stackName, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if service == "" {
		resolveCtx, cancel := context.WithTimeout(r.Context(), execResolveServiceLimit)
		service, err = resolveDefaultComposeService(resolveCtx, stackPath)
		cancel()
		if err != nil {
			logStackOpError(r, "exec ws resolve service", stackName, err)
			http.Error(w, "failed to resolve compose service; pass ?service=<name>", http.StatusBadRequest)
			return
		}
	}

	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		var upgradeErr *wsUpgradeError
		if errors.As(err, &upgradeErr) {
			http.Error(w, upgradeErr.Message, upgradeErr.StatusCode)
			return
		}
		logStackOpError(r, "exec ws upgrade", stackName, err)
		http.Error(w, "failed to upgrade websocket", http.StatusBadRequest)
		return
	}
	defer ws.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	cmd, ptyFile, err := startComposeAttachPTY(ctx, stackPath, service, readInitialTerminalSize(r))
	if err != nil {
		msg := describeExecStartError(err)
		_ = writeExecWSMessage(ws, execWSMessage{
			Type:    "error",
			Message: msg,
		})
		logStackOpError(r, "exec ws start", stackName, err)
		return
	}
	defer ptyFile.Close()

	log.Printf(
		"stack %s %s action=exec ws start stack=%q service=%q from=%s",
		r.Method,
		r.URL.Path,
		stackName,
		service,
		r.RemoteAddr,
	)

	done := make(chan struct{})
	stopOnce := sync.Once{}
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			_ = ws.Close()
			close(done)
		})
	}

	var (
		waitErr error
		waitMu  sync.Mutex
		waitWg  sync.WaitGroup
	)
	waitWg.Add(1)
	go func() {
		defer waitWg.Done()
		err := cmd.Wait()
		waitMu.Lock()
		waitErr = err
		waitMu.Unlock()

		if ctx.Err() != nil {
			return
		}

		if err != nil {
			_ = writeExecWSMessage(ws, execWSMessage{
				Type:    "error",
				Message: "interactive session exited with an error",
			})
		}
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		_ = writeExecWSMessage(ws, execWSMessage{
			Type: "exit",
			Code: exitCode,
		})
		stop()
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptyFile.Read(buf)
			if n > 0 {
				if err := writeExecWSMessage(ws, execWSMessage{
					Type: "output",
					Data: string(buf[:n]),
				}); err != nil {
					stop()
					return
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) && ctx.Err() == nil {
					_ = writeExecWSMessage(ws, execWSMessage{
						Type:    "error",
						Message: "terminal stream closed unexpectedly",
					})
				}
				stop()
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			waitWg.Wait()
			finishExecSessionLogs(r, stackName, service, &waitMu, &waitErr)
			return
		default:
		}

		_ = ws.SetReadDeadline(time.Now().Add(2 * time.Minute))
		opcode, payload, readErr := ws.ReadFrame()
		if readErr != nil {
			stop()
			_ = ptyFile.Close()
			waitWg.Wait()
			finishExecSessionLogs(r, stackName, service, &waitMu, &waitErr)
			return
		}

		switch opcode {
		case wsOpcodeText, wsOpcodeBinary:
			var msg execWSMessage
			if jsonErr := json.Unmarshal(payload, &msg); jsonErr == nil && msg.Type != "" {
				switch msg.Type {
				case "input":
					if msg.Data == "" {
						continue
					}
					if _, err := ptyFile.Write([]byte(msg.Data)); err != nil {
						stop()
					}
				case "resize":
					if resizeErr := applyTerminalResize(ptyFile, msg.Cols, msg.Rows); resizeErr != nil {
						_ = writeExecWSMessage(ws, execWSMessage{
							Type:    "error",
							Message: resizeErr.Error(),
						})
					}
				}
				continue
			}

			if len(payload) > 0 {
				if _, err := ptyFile.Write(payload); err != nil {
					stop()
				}
			}
		case wsOpcodePing:
			_ = ws.WriteFrame(wsOpcodePong, payload)
		case wsOpcodePong:
			continue
		case wsOpcodeClose:
			_ = ws.WriteFrame(wsOpcodeClose, payload)
			stop()
		}
	}
}

func finishExecSessionLogs(r *http.Request, stackName, service string, waitMu *sync.Mutex, waitErr *error) {
	waitMu.Lock()
	finalErr := *waitErr
	waitMu.Unlock()

	if errors.Is(finalErr, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
		log.Printf(
			"stack %s %s action=exec ws stop stack=%q service=%q from=%s reason=client_disconnect",
			r.Method,
			r.URL.Path,
			stackName,
			service,
			r.RemoteAddr,
		)
		return
	}
	if finalErr != nil {
		logStackOpError(r, "exec ws", stackName, finalErr)
		return
	}

	log.Printf(
		"stack %s %s action=exec ws stop stack=%q service=%q from=%s reason=command_exit",
		r.Method,
		r.URL.Path,
		stackName,
		service,
		r.RemoteAddr,
	)
}

func resolveDefaultComposeService(ctx context.Context, stackPath string) (string, error) {
	cmd, err := composeCommandContext(ctx, stackPath, "config", "--services")
	if err != nil {
		return "", err
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return "", err
	}

	for _, line := range strings.Split(out.String(), "\n") {
		service := strings.TrimSpace(line)
		if service != "" {
			return service, nil
		}
	}

	return "", fmt.Errorf("no services found")
}

func readInitialTerminalSize(r *http.Request) terminalSize {
	cols := defaultPTYCols
	rows := defaultPTYRows

	if raw := strings.TrimSpace(r.URL.Query().Get("cols")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			cols = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("rows")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			rows = parsed
		}
	}

	return terminalSize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	}
}

func applyTerminalResize(file *os.File, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("invalid terminal size")
	}
	if cols > 500 || rows > 200 {
		return fmt.Errorf("terminal size exceeds limits")
	}
	return resizePTY(file, terminalSize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

func writeExecWSMessage(ws *wsConn, msg execWSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return ws.WriteFrame(wsOpcodeText, data)
}

func describeExecStartError(err error) string {
	if err == nil {
		return "failed to start interactive console session"
	}
	if errors.Is(err, errPTYUnsupported) {
		return "interactive console PTY is not available on this worker"
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "pty open failed"):
		return "interactive console PTY could not be opened on this worker runtime"
	case strings.Contains(lower, "pty resize failed"):
		return "interactive console PTY resize failed on this worker runtime"
	case strings.Contains(lower, "docker attach launch failed"):
		return "failed to launch docker attach for interactive console"
	case strings.Contains(lower, "no running container found"), strings.Contains(lower, "is not running"):
		return "interactive console is unavailable because the game server container is not running"
	default:
		return "failed to start interactive console session"
	}
}
