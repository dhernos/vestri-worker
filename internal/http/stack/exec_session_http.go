package stack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	execSessionIDBytes        = 16
	execSessionMaxLifetime    = 10 * time.Minute
	execSubscriberBufferSize  = 256
	execMaxJSONRequestBytes   = 1 << 20
	execStreamContentType     = "application/x-ndjson; charset=utf-8"
	execTerminalReadChunkSize = 4096
)

var (
	execSessionIDPattern  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	errExecSessionClosed  = errors.New("interactive session is closed")
	errExecSessionMissing = errors.New("interactive session not found")
	execSessionRegistry   = &execSessionStore{
		sessions: make(map[string]*execSession),
	}
)

type execSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*execSession
}

func (s *execSessionStore) put(session *execSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.id] = session
}

func (s *execSessionStore) get(id string) *execSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *execSessionStore) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

type execSession struct {
	id        string
	stackName string
	service   string

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	pty    *os.File

	writeMu sync.Mutex
	subsMu  sync.RWMutex
	closed  bool
	subs    map[chan execWSMessage]struct{}

	stopOnce sync.Once
}

func newExecSession(
	stackName,
	service string,
	ctx context.Context,
	cancel context.CancelFunc,
	cmd *exec.Cmd,
	pty *os.File,
) (*execSession, error) {
	sessionID, err := generateExecSessionID()
	if err != nil {
		return nil, err
	}
	session := &execSession{
		id:        sessionID,
		stackName: stackName,
		service:   service,
		ctx:       ctx,
		cancel:    cancel,
		cmd:       cmd,
		pty:       pty,
		subs:      make(map[chan execWSMessage]struct{}),
	}
	return session, nil
}

func (s *execSession) start() {
	go s.readPTY()
	go s.waitProcess()
	go s.enforceLifetime()
}

func (s *execSession) enforceLifetime() {
	timer := time.NewTimer(execSessionMaxLifetime)
	defer timer.Stop()

	select {
	case <-timer.C:
		s.broadcast(execWSMessage{
			Type:    "error",
			Message: "interactive session timed out",
		})
		s.stop()
	case <-s.ctx.Done():
	}
}

func (s *execSession) waitProcess() {
	waitErr := s.cmd.Wait()
	if waitErr != nil && s.ctx.Err() == nil {
		s.broadcast(execWSMessage{
			Type:    "error",
			Message: "interactive session exited with an error",
		})
	}

	exitCode := -1
	if s.cmd.ProcessState != nil {
		exitCode = s.cmd.ProcessState.ExitCode()
	}
	s.broadcast(execWSMessage{
		Type: "exit",
		Code: exitCode,
	})
	s.stop()
}

func (s *execSession) readPTY() {
	buf := make([]byte, execTerminalReadChunkSize)
	for {
		n, readErr := s.pty.Read(buf)
		if n > 0 {
			s.broadcast(execWSMessage{
				Type: "output",
				Data: string(buf[:n]),
			})
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && s.ctx.Err() == nil {
				s.broadcast(execWSMessage{
					Type:    "error",
					Message: "terminal stream closed unexpectedly",
				})
			}
			s.stop()
			return
		}
	}
}

func (s *execSession) stop() {
	s.stopOnce.Do(func() {
		execSessionRegistry.remove(s.id)
		s.cancel()
		_ = s.pty.Close()
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}

		s.subsMu.Lock()
		s.closed = true
		for ch := range s.subs {
			close(ch)
			delete(s.subs, ch)
		}
		s.subsMu.Unlock()
	})
}

func (s *execSession) subscribe() (<-chan execWSMessage, error) {
	ch := make(chan execWSMessage, execSubscriberBufferSize)
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if s.closed {
		return nil, errExecSessionClosed
	}
	s.subs[ch] = struct{}{}
	return ch, nil
}

func (s *execSession) unsubscribe(ch <-chan execWSMessage) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	target, ok := findWritableExecSubscriber(ch, s.subs)
	if !ok {
		return
	}
	delete(s.subs, target)
	close(target)
}

func findWritableExecSubscriber(
	target <-chan execWSMessage,
	subs map[chan execWSMessage]struct{},
) (chan execWSMessage, bool) {
	for ch := range subs {
		if (<-chan execWSMessage)(ch) == target {
			return ch, true
		}
	}
	return nil, false
}

func (s *execSession) broadcast(msg execWSMessage) {
	s.subsMu.RLock()
	defer s.subsMu.RUnlock()
	if s.closed {
		return
	}
	for ch := range s.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *execSession) writeInput(data string) error {
	normalized := normalizeExecInputData(data)
	if normalized == "" {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.isClosed() {
		return errExecSessionClosed
	}
	_, err := s.pty.Write([]byte(normalized))
	return err
}

func (s *execSession) resize(cols, rows int) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.isClosed() {
		return errExecSessionClosed
	}
	return applyTerminalResize(s.pty, cols, rows)
}

func (s *execSession) isClosed() bool {
	s.subsMu.RLock()
	defer s.subsMu.RUnlock()
	return s.closed
}

func StackExecSessionHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		stackExecSessionCreateHandler(w, r)
	case http.MethodDelete:
		stackExecSessionDeleteHandler(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func stackExecSessionCreateHandler(w http.ResponseWriter, r *http.Request) {
	if !ptySupported() {
		http.Error(w, "interactive console is not supported on this worker platform", http.StatusNotImplemented)
		return
	}

	stackPath, stackName, err := parseStackTarget(r, false)
	if err != nil {
		logStackOpError(r, "exec session create", "", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	service, err := parseServiceName(r)
	if err != nil {
		logStackOpError(r, "exec session create", stackName, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if service == "" {
		resolveCtx, cancel := context.WithTimeout(r.Context(), execResolveServiceLimit)
		service, err = resolveDefaultComposeService(resolveCtx, stackPath)
		cancel()
		if err != nil {
			logStackOpError(r, "exec session create resolve service", stackName, err)
			http.Error(w, "failed to resolve compose service; pass ?service=<name>", http.StatusBadRequest)
			return
		}
	}

	size := readInitialTerminalSize(r)
	sessionCtx, cancel := context.WithCancel(context.Background())
	cmd, ptyFile, err := startComposeAttachPTY(sessionCtx, stackPath, service, size)
	if err != nil {
		cancel()
		msg := describeExecStartError(err)
		logStackOpError(r, "exec session create", stackName, err)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	session, err := newExecSession(stackName, service, sessionCtx, cancel, cmd, ptyFile)
	if err != nil {
		cancel()
		_ = ptyFile.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		logStackOpError(r, "exec session create", stackName, err)
		http.Error(w, "failed to allocate interactive session", http.StatusInternalServerError)
		return
	}
	execSessionRegistry.put(session)
	session.start()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sessionId": session.id,
	})

	log.Printf(
		"stack %s %s action=exec session start stack=%q service=%q session=%q from=%s",
		r.Method,
		r.URL.Path,
		stackName,
		service,
		session.id,
		r.RemoteAddr,
	)
}

func stackExecSessionDeleteHandler(w http.ResponseWriter, r *http.Request) {
	sessionID, err := parseExecSessionID(r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	session := execSessionRegistry.get(sessionID)
	if session == nil {
		http.Error(w, errExecSessionMissing.Error(), http.StatusNotFound)
		return
	}

	session.stop()
	w.WriteHeader(http.StatusNoContent)
	log.Printf(
		"stack %s %s action=exec session stop stack=%q service=%q session=%q from=%s reason=client_request",
		r.Method,
		r.URL.Path,
		session.stackName,
		session.service,
		session.id,
		r.RemoteAddr,
	)
}

func StackExecStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, err := parseExecSessionID(r.URL.Query().Get("session"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session := execSessionRegistry.get(sessionID)
	if session == nil {
		http.Error(w, errExecSessionMissing.Error(), http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub, err := session.subscribe()
	if err != nil {
		if errors.Is(err, errExecSessionClosed) {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		http.Error(w, "failed to subscribe to interactive session", http.StatusInternalServerError)
		return
	}
	defer session.unsubscribe(sub)

	w.Header().Set("Content-Type", execStreamContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	log.Printf(
		"stack %s %s action=exec stream start stack=%q service=%q session=%q from=%s",
		r.Method,
		r.URL.Path,
		session.stackName,
		session.service,
		session.id,
		r.RemoteAddr,
	)

	for {
		select {
		case <-r.Context().Done():
			log.Printf(
				"stack %s %s action=exec stream stop stack=%q service=%q session=%q from=%s reason=client_disconnect",
				r.Method,
				r.URL.Path,
				session.stackName,
				session.service,
				session.id,
				r.RemoteAddr,
			)
			return
		case msg, ok := <-sub:
			if !ok {
				log.Printf(
					"stack %s %s action=exec stream stop stack=%q service=%q session=%q from=%s reason=session_closed",
					r.Method,
					r.URL.Path,
					session.stackName,
					session.service,
					session.id,
					r.RemoteAddr,
				)
				return
			}
			if err := writeExecNDJSONMessage(w, msg); err != nil {
				logStackOpError(r, "exec stream write", session.stackName, err)
				return
			}
			flusher.Flush()
		}
	}
}

func StackExecInputHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Data      string `json:"data"`
	}
	if err := decodeExecJSONRequest(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sessionID, err := parseExecSessionID(req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session := execSessionRegistry.get(sessionID)
	if session == nil {
		http.Error(w, errExecSessionMissing.Error(), http.StatusNotFound)
		return
	}

	if err := session.writeInput(req.Data); err != nil {
		if errors.Is(err, errExecSessionClosed) {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		logStackOpError(r, "exec input", session.stackName, err)
		http.Error(w, "failed to write interactive input", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func StackExecResizeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
	}
	if err := decodeExecJSONRequest(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sessionID, err := parseExecSessionID(req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session := execSessionRegistry.get(sessionID)
	if session == nil {
		http.Error(w, errExecSessionMissing.Error(), http.StatusNotFound)
		return
	}

	if err := session.resize(req.Cols, req.Rows); err != nil {
		if errors.Is(err, errExecSessionClosed) {
			http.Error(w, err.Error(), http.StatusGone)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func decodeExecJSONRequest(r *http.Request, target interface{}) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, execMaxJSONRequestBytes))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("bad request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("bad request: invalid trailing data")
	}
	return nil
}

func parseExecSessionID(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if !execSessionIDPattern.MatchString(value) {
		return "", fmt.Errorf("invalid session id")
	}
	return value, nil
}

func generateExecSessionID() (string, error) {
	buf := make([]byte, execSessionIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeExecNDJSONMessage(w io.Writer, msg execWSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}
