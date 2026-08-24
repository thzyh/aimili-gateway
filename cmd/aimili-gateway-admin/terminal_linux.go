//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func isTerminal(file *os.File) bool {
	var attributes syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&attributes)), 0, 0, 0)
	return errno == 0
}

func disableTerminalEcho(file *os.File) (func() error, error) {
	var original syscall.Termios
	if err := ioctlTermios(file.Fd(), syscall.TCGETS, &original); err != nil {
		return nil, err
	}
	updated := original
	updated.Lflag &^= syscall.ECHO
	if err := ioctlTermios(file.Fd(), syscall.TCSETS, &updated); err != nil {
		return nil, err
	}
	return func() error { return ioctlTermios(file.Fd(), syscall.TCSETS, &original) }, nil
}

func ioctlTermios(fd, request uintptr, attributes *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(attributes)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
