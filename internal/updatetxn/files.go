package updatetxn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxStateBytes = 32 << 10

func ReadTrustedStateFile(path string, trustedUID *uint32, target any) error {
	return readTrustedJSON(path, trustedUID, target)
}

func ReadRequestFile(path string) (Request, error) {
	var request Request
	if err := readTrustedJSON(path, nil, &request); err != nil {
		return Request{}, err
	}
	if err := ValidateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func WriteResultFile(resultDir string, result Result) error {
	if !hexIdentifier.MatchString(result.RunID) || !result.State.Terminal() || result.FinishedAt == nil {
		return ErrInvalidRequest
	}
	if err := validateResult(result, result.RunID); err != nil {
		return err
	}
	return writeAtomicJSONMode(filepath.Join(resultDir, result.RunID+".json"), result, 0o640)
}

func writeAtomicJSON(path string, value any) error {
	return writeAtomicJSONMode(path, value, 0o600)
}

func writeAtomicJSONMode(path string, value any, mode os.FileMode, owner ...int) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	uid := -1
	if len(owner) > 0 {
		uid = owner[0]
	}
	return publishOwnedFile(path, append(body, '\n'), mode, uid)
}

func publishOwnedFile(path string, body []byte, mode os.FileMode, uid int) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".update-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if uid >= 0 {
		if err := temporary.Chown(uid, -1); err != nil {
			temporary.Close()
			return err
		}
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Publish without replacement and without a transient second hard link.
	// A crash cannot leave a visible result that fails the single-link gate.
	if err := publishExclusive(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func requestDirectoryOwner(directory string) (int, error) {
	if os.Geteuid() != 0 {
		return -1, nil
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return -1, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return -1, ErrUntrustedResult
	}
	file, err := os.Open(directory)
	if err != nil {
		return -1, err
	}
	defer file.Close()
	uid, _, supported, err := openedFileMetadata(file, info)
	if err != nil {
		return -1, err
	}
	if !supported {
		return -1, ErrUntrustedResult
	}
	return int(uid), nil
}

func readTrustedJSON(path string, trustedUID *uint32, target any) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > maxStateBytes || insecurePermissions(before) {
		return ErrUntrustedResult
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return ErrUntrustedResult
	}
	uid, links, ownerSupported, err := openedFileMetadata(file, after)
	if err != nil || links != 1 || (trustedUID != nil && (!ownerSupported || uid != *trustedUID)) {
		return ErrUntrustedResult
	}
	body, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil || len(body) > maxStateBytes {
		return ErrUntrustedResult
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON", ErrUntrustedResult)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON", ErrUntrustedResult)
	}
	return nil
}
