package securefile

import (
	"io/fs"
	"path/filepath"
	"testing"
)

func TestRestrictedPermissionsAllowSystemdReadOnlyCredentialMount(t *testing.T) {
	credentialsDirectory := filepath.Join(string(filepath.Separator), "run", "credentials", "service")
	credential := filepath.Join(credentialsDirectory, "token")
	if !hasRestrictedPermissions(credential, fs.FileMode(0o440), credentialsDirectory, false) {
		t.Fatal("systemd 0440 credential mount was rejected")
	}
	for _, test := range []struct {
		path string
		mode fs.FileMode
	}{
		{path: credential, mode: 0o460},
		{path: credential, mode: 0o444},
		{path: filepath.Join(filepath.Dir(credentialsDirectory), "other", "token"), mode: 0o440},
		{path: filepath.Join(string(filepath.Separator), "etc", "token"), mode: 0o640},
	} {
		if hasRestrictedPermissions(test.path, test.mode, credentialsDirectory, false) {
			t.Fatalf("unsafe secret mode accepted: path=%q mode=%#o", test.path, test.mode.Perm())
		}
	}
	if !hasRestrictedPermissions(filepath.Join(string(filepath.Separator), "etc", "token"), 0o600, credentialsDirectory, false) {
		t.Fatal("ordinary 0600 secret file was rejected")
	}
}
