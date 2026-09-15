package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// Uses numeric fixture credentials only. It never creates users or changes a
// service/configuration path outside its own temporary directory.
func TestUpdaterDACWithActualServiceUIDs(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root integration runner")
	}
	root, err := os.MkdirTemp("", "aimili-dac-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int{"root": 0, "aimili-gateway": 65001, "aimili-gateway-updater": 65002}
	policy := updaterDirectoryPolicy(t)
	for path, p := range policy {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(target, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(target, ids[p.owner], ids[p.group]); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(p.mode & 0777)
		if p.mode&02000 != 0 {
			mode |= os.ModeSetgid
		}
		if err := os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []struct {
		path         string
		owner, group int
		mode         os.FileMode
	}{
		{"/etc/aimili-gateway/config.json", 0, 65001, 0640},
		{"/etc/aimili-gateway/xui-automation.json", 0, 0, 0600},
		{"/etc/aimili-gateway/updater.json", 0, 65002, 0640},
		{"/etc/aimili-gateway/release-ed25519.pub", 0, 65002, 0640},
		{"/var/lib/aimili-gateway/aimili-gateway.db", 65001, 65001, 0600},
	} {
		target := filepath.Join(root, file.path)
		if err := os.WriteFile(target, []byte("fixture"), file.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(target, file.owner, file.group); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"journal", "install-backup"} {
		if err := os.Mkdir(filepath.Join(root, "var/lib/aimili-gateway-update", dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for role, id := range map[string]uint32{"gateway": 65001, "fetcher": 65002} {
		command := exec.Command(exe, "-test.run=^TestUpdaterDACServiceUIDHelper$", "-test.v")
		command.Env = append(os.Environ(), "AIMILI_DAC_FIXTURE="+root, "AIMILI_DAC_ROLE="+role)
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: id, Gid: id}}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s DAC verification failed: %v %s", role, err, output)
		}
	}
}

func TestUpdaterDACServiceUIDHelper(t *testing.T) {
	root, role := os.Getenv("AIMILI_DAC_FIXTURE"), os.Getenv("AIMILI_DAC_ROLE")
	if root == "" {
		t.Skip("service UID helper")
	}
	read := func(path string, want bool) {
		_, err := os.ReadFile(filepath.Join(root, path))
		if (err == nil) != want {
			t.Errorf("%s read %s allowed=%v want=%v", role, path, err == nil, want)
		}
	}
	if role == "gateway" {
		read("/etc/aimili-gateway/config.json", true)
		return
	}
	read("/etc/aimili-gateway/updater.json", true)
	read("/etc/aimili-gateway/release-ed25519.pub", true)
	for _, secret := range []string{"/etc/aimili-gateway/config.json", "/etc/aimili-gateway/xui-automation.json", "/var/lib/aimili-gateway/aimili-gateway.db"} {
		read(secret, false)
	}
	for _, path := range []string{"/var/lib/aimili-gateway-update", "/var/lib/aimili-gateway-update/journal", "/var/lib/aimili-gateway-update/install-backup", "/var/lib/aimili-gateway/update-spool/results"} {
		if _, err := os.ReadDir(filepath.Join(root, path)); err == nil {
			t.Errorf("fetcher can list %s", path)
		}
	}
	if _, err := os.ReadDir(filepath.Join(root, "/var/lib/aimili-gateway/update-spool/requests")); err != nil {
		t.Fatal("fetcher cannot list requests", err)
	}
	if err := os.WriteFile(filepath.Join(root, "/var/lib/aimili-gateway-update/staging/download.fixture"), []byte("download"), 0600); err != nil {
		t.Fatal("fetcher cannot stage download", err)
	}
}
