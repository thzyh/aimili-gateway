//go:build !windows

package updatetxn

import (
	"errors"
	"os"
	"syscall"
)

func openedFileMetadata(_ *os.File, info os.FileInfo) (uint32, uint64, bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false, errors.New("unsupported file metadata")
	}
	return stat.Uid, uint64(stat.Nlink), true, nil
}

func insecurePermissions(info os.FileInfo) bool {
	return info.Mode().Perm()&0o022 != 0
}
