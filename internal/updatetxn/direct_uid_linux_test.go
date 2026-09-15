package updatetxn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRootSubmissionDoesNotLockOutGatewayUID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root fixture")
	}
	root, err := os.MkdirTemp("", "aimili-direct-uid-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	os.Chmod(root, 0755)
	requests, results := filepath.Join(root, "requests"), filepath.Join(root, "results")
	os.Mkdir(requests, 0750)
	os.Chown(requests, 65001, 65002)
	os.Chmod(requests, 0750|os.ModeSetgid)
	os.Mkdir(results, 0750)
	os.Chown(results, 0, 65001)
	os.Chmod(results, 0750|os.ModeSetgid)
	client := Client{RequestDir: requests, ResultDir: results}
	req := Request{RunID: strings.Repeat("a", 64), Kind: KindGateway, Version: "v1.2.3", Action: ActionApply}
	if _, err := client.Submit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	finished := time.Now()
	if err := WriteResultFile(results, Result{RunID: req.RunID, Kind: req.Kind, Version: req.Version, State: StateSuccess, FinishedAt: &finished}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRequest(requests, req.RunID); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	command := exec.Command(exe, "-test.run=^TestGatewayUIDSubmitHelper$", "-test.v")
	command.Env = append(os.Environ(), "AIMILI_SUBMIT_UID_FIXTURE="+root)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65001, Gid: 65001}}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Gateway UID submit after root terminal: %v %s", err, output)
	}
}

func TestGatewayUIDSubmitHelper(t *testing.T) {
	root := os.Getenv("AIMILI_SUBMIT_UID_FIXTURE")
	if root == "" {
		t.Skip("service UID helper")
	}
	client := Client{RequestDir: filepath.Join(root, "requests"), ResultDir: filepath.Join(root, "results")}
	if _, err := client.Submit(context.Background(), Request{RunID: strings.Repeat("b", 64), Kind: KindGateway, Action: ActionRollback}); err != nil {
		t.Fatal(err)
	}
}
