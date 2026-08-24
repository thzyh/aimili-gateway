//go:build !linux && !windows

package main

import "os"

func isTerminal(_ *os.File) bool {
	return false
}

func disableTerminalEcho(_ *os.File) (func() error, error) {
	return func() error { return nil }, nil
}
