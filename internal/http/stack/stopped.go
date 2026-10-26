package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var stackLocks sync.Map

// LockStack serializes state-changing compose commands per stack; concurrent up/down calls make compose fail.
func LockStack(stackPath string) (unlock func()) {
	key := stackPath
	if abs, err := filepath.Abs(stackPath); err == nil {
		key = abs
	}
	value, _ := stackLocks.LoadOrStore(filepath.Clean(key), &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// WithStackStopped runs fn with the stack taken down, then starts it again if it was running before.
func WithStackStopped(stackPath string, fn func() error) error {
	defer LockStack(stackPath)()

	composeFile, err := composeFilePath(stackPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(composeFile); err != nil {
		return fn()
	}

	out, err := RunCompose(stackPath, "ps", "-q")
	if err != nil {
		return fmt.Errorf("could not check server state: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return fn()
	}

	if out, err := RunCompose(stackPath, "down"); err != nil {
		return fmt.Errorf("could not stop server: %s", strings.TrimSpace(out))
	}

	fnErr := fn()
	if out, err := RunCompose(stackPath, "up", "-d"); err != nil {
		startErr := fmt.Errorf("could not start server: %s", strings.TrimSpace(out))
		if fnErr != nil {
			return fmt.Errorf("%w; %v", fnErr, startErr)
		}
		return startErr
	}
	return fnErr
}
