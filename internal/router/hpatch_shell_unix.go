//go:build unix

package router

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Inherited stdin is normally a blocking descriptor without a Go poller.
// Duplicating it after setting O_NONBLOCK gives the transfer an independently
// closeable, pollable reader; closing ordinary os.Stdin cannot unblock its read.
func openHpatchInput(stdin *os.File) (*os.File, func(), error) {
	flags, err := unix.FcntlInt(stdin.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return nil, nil, err
	}
	fd, err := unix.Dup(int(stdin.Fd()))
	if err != nil {
		return nil, nil, err
	}
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, nil, err
	}
	input := os.NewFile(uintptr(fd), "hpatch-source")
	if input == nil {
		_ = unix.Close(fd)
		_ = unix.SetNonblock(int(stdin.Fd()), flags&unix.O_NONBLOCK != 0)
		return nil, nil, fmt.Errorf("open HPATCH source descriptor")
	}
	return input, func() {
		_ = input.Close()
		_ = unix.SetNonblock(int(stdin.Fd()), flags&unix.O_NONBLOCK != 0)
	}, nil
}

func lockHpatchCheckpoint(root *os.Root, name string) (func(), error) {
	// A separate stable inode serializes replacement of the checkpoint itself.
	file, err := root.OpenFile(name+".lock", os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("invalid checkpoint lock")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("checkpoint is busy: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
