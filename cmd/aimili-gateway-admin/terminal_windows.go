//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func isTerminal(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

func disableTerminalEcho(file *os.File) (func() error, error) {
	handle := windows.Handle(file.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return nil, err
	}
	return func() error { return windows.SetConsoleMode(handle, mode) }, nil
}
