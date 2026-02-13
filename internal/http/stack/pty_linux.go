//go:build linux

package stack

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func ptySupported() bool {
	return true
}

func startComposeExecPTY(ctx context.Context, stackPath, service, bootstrapShell string, size terminalSize) (*exec.Cmd, *os.File, error) {
	cmd, err := composeCommandContext(ctx, stackPath, "exec", service, "sh", "-lc", bootstrapShell)
	if err != nil {
		return nil, nil, err
	}

	master, slave, err := openPTY()
	if err != nil {
		return nil, nil, err
	}
	defer slave.Close()

	if err := setPTYSize(master, size); err != nil {
		_ = master.Close()
		return nil, nil, err
	}

	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    int(slave.Fd()),
	}

	if err := cmd.Start(); err != nil {
		_ = master.Close()
		return nil, nil, err
	}

	return cmd, master, nil
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
