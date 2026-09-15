//go:build !windows

package updatefetch

import "golang.org/x/sys/unix"

func AvailableBytes(path string) (uint64, error) {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return 0, err
	}
	return status.Bavail * uint64(status.Bsize), nil
}
