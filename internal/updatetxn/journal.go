package updatetxn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const InstallerStateRoot = "/var/lib/aimili-gateway-update"

// Journal is private installer state, never readable by the fetcher or API.
type Journal struct {
	Request     Request `json:"request"`
	OldDigest   string  `json:"oldDigest"`
	NewDigest   string  `json:"newDigest"`
	OldPrevious string  `json:"oldPrevious,omitempty"`
	Baseline    string  `json:"baseline"`
	Phase       string  `json:"phase"`
	State       State   `json:"state,omitempty"`
	ErrorCode   string  `json:"errorCode,omitempty"`
}

func Digest(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

func privateStateDirectory(root string) (string, error) {
	if root == "" {
		return "", ErrInvalidRequest
	}
	dir := filepath.Join(root, "journal")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || insecurePermissions(info) {
		return "", ErrUntrustedResult
	}
	return dir, nil
}

func AcquireExecutionLease(root string) (func(), error) {
	dir, err := privateStateDirectory(root)
	if err != nil {
		return nil, err
	}
	return AcquireFileLock(filepath.Join(dir, "execution.lock"), currentUID())
}

func currentUID() *uint32 {
	uid := os.Geteuid()
	if uid < 0 {
		return nil
	}
	value := uint32(uid)
	return &value
}

func ReadJournal(root string) (Journal, error) {
	var journal Journal
	path := filepath.Join(root, "journal", "active.json")
	if err := readTrustedJSON(path, currentUID(), &journal); err != nil {
		return journal, err
	}
	if err := ValidateRequest(journal.Request); err != nil {
		return journal, err
	}
	if !hexIdentifier.MatchString(journal.NewDigest) || (journal.OldDigest != "" && !hexIdentifier.MatchString(journal.OldDigest)) || journal.Baseline == "" || len(journal.Baseline) > 4096 {
		return journal, ErrUntrustedResult
	}
	switch journal.Phase {
	case "prepared", "stopped", "switched", "started", "verified", "terminal":
	default:
		return journal, ErrUntrustedResult
	}
	return journal, nil
}

func WriteJournal(root string, journal Journal) error {
	if ValidateRequest(journal.Request) != nil || !hexIdentifier.MatchString(journal.NewDigest) || journal.Baseline == "" {
		return ErrInvalidRequest
	}
	dir, err := privateStateDirectory(root)
	if err != nil {
		return err
	}
	destination := filepath.Join(dir, "active.json")
	if previous, err := ReadJournal(root); err == nil {
		if !sameRequest(previous.Request, journal.Request) {
			return ErrUpdateBusy
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	body, err := json.Marshal(journal)
	if err != nil || len(body) > maxStateBytes {
		return ErrInvalidRequest
	}
	tmp, err := os.CreateTemp(dir, ".journal-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, destination); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// ClearJournal is called only after a trusted terminal result has been fsynced.
func ClearJournal(root, runID string) error {
	j, err := ReadJournal(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if j.Request.RunID != runID {
		return ErrRunConflict
	}
	path := filepath.Join(root, "journal", "active.json")
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
