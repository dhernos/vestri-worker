//go:build linux

package stack

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

func ptySupported() bool {
	return true
}

func startComposeAttachPTY(ctx context.Context, stackPath, service string, size terminalSize) (*exec.Cmd, *os.File, error) {
	containerID, err := resolveComposeServiceContainerID(ctx, stackPath, service)
	if err != nil {
		return nil, nil, err
	}
	if err := validateAttachContainerIO(ctx, containerID); err != nil {
		return nil, nil, err
	}

	cmd, master, slave, err := prepareDockerAttachPTY(ctx, containerID, size)
	if err != nil {
		return nil, nil, err
	}
	defer slave.Close()

	if err := startDockerAttachWithPTY(cmd, slave, true); err == nil {
		return cmd, master, nil
	} else if !shouldRetryExecWithoutControllingTTY(err) {
		_ = master.Close()
		return nil, nil, fmt.Errorf("docker attach launch failed: %w", err)
	}

	_ = master.Close()

	fallbackCmd, fallbackMaster, fallbackSlave, err := prepareDockerAttachPTY(ctx, containerID, size)
	if err != nil {
		return nil, nil, err
	}
	defer fallbackSlave.Close()

	if err := startDockerAttachWithPTY(fallbackCmd, fallbackSlave, false); err != nil {
		_ = fallbackMaster.Close()
		return nil, nil, fmt.Errorf("docker attach launch failed: %w", err)
	}

	return fallbackCmd, fallbackMaster, nil
}

func resizePTY(file *os.File, size terminalSize) error {
	return setPTYSize(file, size)
}

func openPTY() (*os.File, *os.File, error) {
	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}

	unlock := 0
	if err := ioctl(masterFD, syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		_ = syscall.Close(masterFD)
		return nil, nil, err
	}

	var ptyNumber uint32
	if err := ioctl(masterFD, syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptyNumber))); err != nil {
		_ = syscall.Close(masterFD)
		return nil, nil, err
	}

	slaveName := fmt.Sprintf("/dev/pts/%d", ptyNumber)
	slaveFD, err := syscall.Open(slaveName, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		_ = syscall.Close(masterFD)
		return nil, nil, err
	}

	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	slave := os.NewFile(uintptr(slaveFD), slaveName)
	return master, slave, nil
}

func prepareDockerAttachPTY(ctx context.Context, containerID string, size terminalSize) (*exec.Cmd, *os.File, *os.File, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pty open failed: %w", err)
	}
	if err := setPTYSize(master, size); err != nil {
		_ = master.Close()
		_ = slave.Close()
		return nil, nil, nil, fmt.Errorf("pty resize failed: %w", err)
	}

	cmd := exec.CommandContext(ctx, "docker", "attach", "--sig-proxy=false", containerID)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	return cmd, master, slave, nil
}

func startDockerAttachWithPTY(cmd *exec.Cmd, slave *os.File, useControllingTTY bool) error {
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	if useControllingTTY {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid:  true,
			Setctty: true,
			Ctty:    int(slave.Fd()),
		}
	} else {
		cmd.SysProcAttr = nil
	}
	return cmd.Start()
}

func shouldRetryExecWithoutControllingTTY(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "setctty") || strings.Contains(lower, "operation not permitted")
}

func resolveComposeServiceContainerID(ctx context.Context, stackPath, service string) (string, error) {
	cmd, err := composeCommandContext(ctx, stackPath, "ps", "-q", service)
	if err != nil {
		return "", fmt.Errorf("compose command setup failed: %w", err)
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("compose service container lookup failed: %w", err)
	}

	for _, line := range strings.Split(out.String(), "\n") {
		containerID := strings.TrimSpace(line)
		if containerID != "" {
			return containerID, nil
		}
	}

	return "", fmt.Errorf("no running container found for service %q", service)
}

func validateAttachContainerIO(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(
		ctx,
		"docker",
		"inspect",
		"--format",
		"{{.State.Running}} {{.Config.OpenStdin}} {{.Config.Tty}}",
		containerID,
	)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container inspect failed: %w", err)
	}

	fields := strings.Fields(strings.TrimSpace(out.String()))
	if len(fields) < 3 {
		return fmt.Errorf("container inspect returned unexpected output")
	}
	if strings.ToLower(fields[0]) != "true" {
		return fmt.Errorf("container is not running")
	}
	if strings.ToLower(fields[1]) != "true" {
		return fmt.Errorf("container stdin is disabled (stdin_open=false)")
	}
	if strings.ToLower(fields[2]) != "true" {
		return fmt.Errorf("container tty is disabled (tty=false)")
	}
	return nil
}

func setPTYSize(file *os.File, size terminalSize) error {
	ws := &ptyWinsize{
		Col: size.Cols,
		Row: size.Rows,
	}
	return ioctl(int(file.Fd()), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
}

type ptyWinsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func ioctl(fd int, request, arg uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, arg)
	if errno != 0 {
		return errno
	}
	return nil
}
