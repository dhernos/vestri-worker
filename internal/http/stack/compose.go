package stack

import (
	"bytes"
	"os/exec"
        "path/filepath"
)

func RunCompose(stackDir string, args ...string) (string, error) {
	stackDir, err := filepath.Abs(stackDir)
	if err != nil {
		return "", err
	}

	composeFile := filepath.Join(stackDir, "docker-compose.yml")

        cmdArgs := append([]string{"compose", "-f", composeFile}, args...)
        cmd := exec.Command("docker", cmdArgs...)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	return out.String(), err
}
