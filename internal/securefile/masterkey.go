package securefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

func ReadMasterKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("master key must be a regular file")
	}
	if !masterKeyPermissionsAllowed(path, info.Mode().Perm(), os.Getenv("CREDENTIALS_DIRECTORY"), runtime.GOOS) {
		return nil, errors.New("master key permissions are too broad")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read master key")
	}
	if len(key) != 32 {
		clear(key)
		return nil, errors.New("master key must be exactly 32 bytes")
	}
	return key, nil
}

func masterKeyPermissionsAllowed(path string, permissions os.FileMode, credentialDirectory, goos string) bool {
	if goos == "windows" || permissions&0o077 == 0 {
		return true
	}
	if permissions != 0o440 || credentialDirectory == "" {
		return false
	}
	cleanDirectory := filepath.Clean(credentialDirectory)
	cleanPath := filepath.Clean(path)
	return filepath.Dir(cleanPath) == cleanDirectory
}
