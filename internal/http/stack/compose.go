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
	ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
	defer cancel()
	cmd, err := composeCommandContext(ctx, stackDir, args...)
	if err != nil {
		return "", err
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	return out.String(), err
}

func composeCommandContext(ctx context.Context, stackDir string, args ...string) (*exec.Cmd, error) {
	composeFile, err := composeFilePath(stackDir)
	if err != nil {
		return nil, err
	}

	cmdArgs := append([]string{"compose", "-f", composeFile}, args...)
	return exec.CommandContext(ctx, "docker", cmdArgs...), nil
}

func composeFilePath(stackDir string) (string, error) {
	abs, err := filepath.Abs(stackDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, "docker-compose.yml"), nil
}
