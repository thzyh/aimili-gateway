package securefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMasterKeyPreservesNotExist(t *testing.T) {
	_, err := ReadMasterKey(filepath.Join(t.TempDir(), "missing.key"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestMasterKeyPermissionsAllowSystemdCredentialGroupRead(t *testing.T) {
	credentialDirectory := filepath.FromSlash("/run/credentials/aimili-gateway.service")
	path := filepath.Join(credentialDirectory, "gateway-master-key")
	if !masterKeyPermissionsAllowed(path, 0o440, credentialDirectory, "linux") {
		t.Fatal("systemd credential mode 0440 was rejected")
	}
}

func TestMasterKeyPermissionsRejectGroupReadOutsideSystemdCredentials(t *testing.T) {
	path := filepath.FromSlash("/var/lib/aimili-gateway/master.key")
	if masterKeyPermissionsAllowed(path, 0o440, "", "linux") {
		t.Fatal("ordinary group-readable master key was accepted")
	}
}

func TestMasterKeyPermissionsRejectWorldReadableSystemdCredential(t *testing.T) {
	credentialDirectory := filepath.FromSlash("/run/credentials/aimili-gateway.service")
	path := filepath.Join(credentialDirectory, "gateway-master-key")
	if masterKeyPermissionsAllowed(path, os.FileMode(0o444), credentialDirectory, "linux") {
		t.Fatal("world-readable systemd credential was accepted")
	}
}
