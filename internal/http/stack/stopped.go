package stack

import (
	"fmt"
	"os"
	"strings"
)

// WithStackStopped runs fn with the stack taken down, then starts it again if it was running before.
func WithStackStopped(stackPath string, fn func() error) error {
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
