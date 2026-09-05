package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsIncompleteUIInstallArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"ui-install", "--root", t.TempDir()}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "staging") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunRejectsNonLoopbackHealthURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"ui-install", "--root", t.TempDir(), "--staging", t.TempDir(),
		"--public-key", "fixture.pub", "--health-url", "https://example.invalid",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "loopback") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
