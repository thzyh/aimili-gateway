//go:build !windows

package updatetxn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetRejectsWrongResultOwner(t *testing.T) {
	client := newTestClient(t)
	wrongUID := uint32(os.Getuid() + 1)
	client.TrustedResultUID = &wrongUID
	runID := strings.Repeat("a", 64)
	result := filepath.Join(client.ResultDir, runID+".json")
	if err := os.WriteFile(result, []byte(`{"runId":"`+runID+`","kind":"gateway","version":"v1.2.3","state":"success"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), runID); !errors.Is(err, ErrUntrustedResult) {
		t.Fatalf("wrong owner error = %v", err)
	}
}
