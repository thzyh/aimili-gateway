//go:build !linux && !windows

package updatetxn

import "errors"

func publishExclusive(source, destination string) error {
	return errors.New("atomic updater publication is unsupported on this platform")
}
