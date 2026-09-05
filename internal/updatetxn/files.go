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

func writeAtomicJSONMode(path string, value any, mode os.FileMode) error {
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
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(value); err != nil {
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
	if _, err := os.Lstat(path); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
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
