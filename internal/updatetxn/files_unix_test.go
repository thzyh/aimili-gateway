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

func TestJournalRejectsNonPrivateMetadata(t *testing.T) {
	for _, target := range []string{"file", "directory"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			j := Journal{Request: Request{RunID: strings.Repeat("a", 64), Kind: KindGateway, Action: ActionApply, Version: "v1.2.3"}, NewDigest: strings.Repeat("b", 64), OldDigest: strings.Repeat("c", 64), Baseline: "baseline", Phase: "prepared"}
			if err := WriteJournal(root, j); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "journal", "active.json")
			mode := os.FileMode(0644)
			if target == "directory" {
				path = filepath.Join(root, "journal")
				mode = 0755
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadJournal(root); !errors.Is(err, ErrUntrustedResult) {
				t.Fatalf("non-private %s accepted: %v", target, err)
			}
		})
	}
}
