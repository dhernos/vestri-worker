//go:build !linux

package stack

import (
	"context"
	"os"
	"os/exec"
)

func ptySupported() bool {
	return false
}

func startComposeAttachPTY(_ context.Context, _ string, _ string, _ terminalSize) (*exec.Cmd, *os.File, error) {
	return nil, nil, errPTYUnsupported
}

func resizePTY(_ *os.File, _ terminalSize) error {
	return errPTYUnsupported
}
