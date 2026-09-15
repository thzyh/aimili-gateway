package deploy

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type dacPath struct {
	owner, group string
	mode         uint32
}

func updaterDirectoryPolicy(t *testing.T) map[string]dacPath {
	t.Helper()
	policy := map[string]dacPath{}
	rule := regexp.MustCompile(`(?m)^install -d -m ([0-7]+) -o ([\w-]+) -g ([\w-]+) (/[^\s]+)$`)
	for _, match := range rule.FindAllStringSubmatch(readAsset(t, "../scripts/deploy-gateway-updater-remote.sh"), -1) {
		mode, _ := strconv.ParseUint(match[1], 8, 32)
		policy[match[4]] = dacPath{match[2], match[3], uint32(mode)}
	}
	return policy
}

func allows(p dacPath, user, group string, access uint32) bool {
	bits := p.mode & 7
	if user == p.owner {
		bits = (p.mode >> 6) & 7
	} else if group == p.group {
		bits = (p.mode >> 3) & 7
	}
	return bits&access == access
}

func TestUpdaterCompleteDACPathsAndSecretIsolation(t *testing.T) {
	policy := updaterDirectoryPolicy(t)
	for _, directory := range []string{"/etc/aimili-gateway", "/var/lib/aimili-gateway", "/var/lib/aimili-gateway/update-spool", "/var/lib/aimili-gateway/update-spool/requests", "/var/lib/aimili-gateway-update", "/var/lib/aimili-gateway-update/staging"} {
		if !allows(policy[directory], "aimili-gateway-updater", "aimili-gateway-updater", 1) {
			t.Errorf("fetcher cannot traverse %s: %+v", directory, policy[directory])
		}
	}
	if !allows(policy["/etc/aimili-gateway"], "aimili-gateway", "aimili-gateway", 1) {
		t.Error("Gateway cannot traverse configuration directory")
	}
	if p := policy["/var/lib/aimili-gateway-update"]; allows(p, "aimili-gateway-updater", "aimili-gateway-updater", 4) || allows(p, "aimili-gateway-updater", "aimili-gateway-updater", 2) {
		t.Error("fetcher can list/write root installer state")
	}
	for _, secret := range []dacPath{{"root", "aimili-gateway", 0640}, {"root", "root", 0600}, {"aimili-gateway", "aimili-gateway", 0600}} {
		if allows(secret, "aimili-gateway-updater", "aimili-gateway-updater", 4) {
			t.Fatal("secret readable by fetcher")
		}
	}
	if strings.Contains(readAsset(t, "systemd/aimili-gateway-update-fetch.service"), "SupplementaryGroups=aimili-gateway") {
		t.Error("fetcher inherits secret-reading Gateway group")
	}
}

func TestInstallerUIWriteAndDirectoryMetadataRecoveryContract(t *testing.T) {
	unit := readAsset(t, "systemd/aimili-gateway-update-install.service")
	if !strings.Contains(unit, "ReadWritePaths=/var/lib/aimili-gateway/ui\n") {
		t.Error("strict mount namespace denies UI apply/rollback")
	}
	script := readAsset(t, "../scripts/deploy-gateway-updater-remote.sh")
	for _, marker := range []string{"directory-metadata", "stat -c", "chown", "chmod", "restore_directory_metadata"} {
		if !strings.Contains(script, marker) {
			t.Errorf("directory metadata recovery missing %s", marker)
		}
	}
}

func TestFetchPathHasNoPersistentTriggerWhileInstallerIsRunning(t *testing.T) {
	unit := readAsset(t, "systemd/aimili-gateway-update-fetch.path")
	if strings.Contains(unit, "PathExistsGlob=") || !strings.Contains(unit, "PathChanged=/var/lib/aimili-gateway/update-spool/requests\n") {
		t.Fatal("existing request continuously retriggers fetch while installer owns the run")
	}
}
