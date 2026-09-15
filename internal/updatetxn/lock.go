package updatetxn

import "os"

// AcquireFileLock holds a kernel lease on a persistent, private regular file.
// Never unlink it: doing so permits another process to lock a different inode.
// The kernel drops the lease on crash; the caller must then recover its journal.
func AcquireFileLock(path string, trustedUID *uint32) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			file.Close()
		}
	}()
	info, err := file.Stat()
	before, lstatErr := os.Lstat(path)
	if err != nil || lstatErr != nil || !before.Mode().IsRegular() || !os.SameFile(before, info) || insecurePermissions(info) || info.Size() != 0 {
		return nil, ErrUntrustedResult
	}
	uid, links, supported, err := openedFileMetadata(file, info)
	if err != nil || links != 1 || (trustedUID != nil && (!supported || uid != *trustedUID)) {
		return nil, ErrUntrustedResult
	}
	if err := lockFile(file); err != nil {
		return nil, ErrUpdateBusy
	}
	valid = true
	return func() { _ = file.Close() }, nil
}
