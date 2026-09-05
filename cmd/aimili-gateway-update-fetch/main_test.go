package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsMissingFetcherInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fetch"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "config") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunSpoolRequiresConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"spool"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "config is required") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
