package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestUnconfinedRootCannotExecuteInstallerSpool(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root fixture")
	}
	_, cfg, _ := spoolFixture(t)
	var out, stderr bytes.Buffer
	code := runInstaller(cfg, nil, &out, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "installer_service_required") {
		t.Fatalf("unconfined root spool ran: code=%d stderr=%q", code, stderr.String())
	}
}
