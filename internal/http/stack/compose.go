package stack

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"time"
)

const composeTimeout = 5 * time.Minute

func RunCompose(stackDir string, args ...string) (string, error) {
	stackDir, err := filepath.Abs(stackDir)
	if err != nil {
		return "", err
	}

	composeFile := filepath.Join(stackDir, "docker-compose.yml")

	cmdArgs := append([]string{"compose", "-f", composeFile}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	return out.String(), err
}
