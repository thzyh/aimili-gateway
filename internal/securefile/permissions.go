package securefile

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func RestrictedPermissions(path string, mode fs.FileMode) bool {
	return hasRestrictedPermissions(path, mode, os.Getenv("CREDENTIALS_DIRECTORY"), runtime.GOOS == "windows")
}

func hasRestrictedPermissions(path string, mode fs.FileMode, credentialsDirectory string, windows bool) bool {
	if windows {
		return true
	}
	permissions := mode.Perm()
	if permissions&0o077 == 0 {
		return true
	}
	if permissions != 0o440 || strings.TrimSpace(credentialsDirectory) == "" {
		return false
	}
	absolutePath, pathErr := filepath.Abs(path)
	absoluteDirectory, directoryErr := filepath.Abs(credentialsDirectory)
	if pathErr != nil || directoryErr != nil {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(absoluteDirectory), filepath.Clean(absolutePath))
	return err == nil && relative != "." && relative != "" && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
