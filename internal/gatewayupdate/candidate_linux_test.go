package gatewayupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCandidateRemainsExecutableByGatewayUIDUnderPrivateUmask(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root fixture for non-root exec")
	}
	root, err := os.MkdirTemp("", "aimili-candidate-mode-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	previousUmask := syscall.Umask(0077)
	defer syscall.Umask(previousUmask)
	candidate := filepath.Join(root, "candidate")
	if err := writeCandidate(candidate, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("candidate mode=%04o under umask0077; want0755", info.Mode().Perm())
	}
	command := exec.Command(candidate)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65001, Gid: 65001}}
	if output, err := command.CombinedOutput(); err != nil {
		t.Errorf("Gateway UID could not execute candidate: %v %s", err, output)
	}
}
