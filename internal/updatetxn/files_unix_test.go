//go:build !windows

package updatetxn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestUpdateSpoolFilesUseGatewayReadableModes(t *testing.T) {
	client := newTestClient(t)
	runID := strings.Repeat("a", 64)
	request := Request{RunID: runID, Kind: KindGateway, Version: "v1.2.3", Action: ActionApply}
	if _, err := client.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(client.RequestDir, runID+".json"):   0o640,
		filepath.Join(client.RequestDir, ".update.lease"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", path, got, want)
		}
	}
	finished := time.Now().UTC()
	if err := WriteResultFile(client.ResultDir, Result{RunID: runID, Kind: KindGateway, Version: "v1.2.3", State: StateSuccess, FinishedAt: &finished}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(client.ResultDir, runID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("result mode = %04o, want 0640", got)
	}
}
