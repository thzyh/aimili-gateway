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

func TestSubmitAcceptsOnlySignedVersionIdentifier(t *testing.T) {
	for name, version := range map[string]string{
		"empty":      "",
		"url":        "https://updates.example.test/v1.2.3",
		"path":       "../v1.2.3",
		"uppercase":  "V1.2.3",
		"whitespace": " v1.2.3",
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t)
			_, err := client.Submit(context.Background(), Request{
				RunID: strings.Repeat("a", 64), Kind: KindGateway, Version: version, Action: ActionApply,
			})
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("version %q error = %v", version, err)
			}
		})
	}
}

func TestSubmitSameRunIsIdempotentAndDifferentRequestConflicts(t *testing.T) {
	client := newTestClient(t)
	request := Request{RunID: strings.Repeat("a", 64), Kind: KindGateway, Version: "v1.2.3", Action: ActionApply}
	first, err := client.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var storedLease leaseRecord
	leasePath := filepath.Join(client.RequestDir, ".update.lease")
	if err := readTrustedJSON(leasePath, nil, &storedLease); err != nil {
		t.Fatalf("read stored lease: %v", err)
	}
	var storedRequest Request
	if err := readTrustedJSON(filepath.Join(client.RequestDir, request.RunID+".json"), nil, &storedRequest); err != nil {
		t.Fatalf("read stored request: %v", err)
	}
	second, err := client.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID != request.RunID || second != first || first.State != StatePending {
		t.Fatalf("idempotent results = %#v %#v", first, second)
	}

	request.Version = "v1.2.4"
	if _, err := client.Submit(context.Background(), request); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("conflicting run error = %v", err)
	}
}

func TestLeaseRejectsConcurrentUIAndGatewayTransactions(t *testing.T) {
	client := newTestClient(t)
	if _, err := client.Submit(context.Background(), Request{
		RunID: strings.Repeat("a", 64), Kind: KindUI, Version: strings.Repeat("b", 64), Action: ActionApply,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Submit(context.Background(), Request{
		RunID: strings.Repeat("c", 64), Kind: KindGateway, Version: "v1.2.3", Action: ActionApply,
	}); !errors.Is(err, ErrUpdateBusy) {
		t.Fatalf("concurrent gateway error = %v", err)
	}
}

func TestGetRejectsSymlinkAndHardLinkedResult(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, target, result string){
		"symlink": func(t *testing.T, target, result string) {
			if err := os.Symlink(target, result); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		},
		"hardlink": func(t *testing.T, target, result string) {
			if err := os.Link(target, result); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t)
			runID := strings.Repeat("a", 64)
			target := filepath.Join(filepath.Dir(client.ResultDir), "untrusted.json")
			if err := os.WriteFile(target, []byte(`{"runId":"`+runID+`","kind":"gateway","version":"v1.2.3","state":"success"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			result := filepath.Join(client.ResultDir, runID+".json")
			arrange(t, target, result)
			if _, err := client.Get(context.Background(), runID); !errors.Is(err, ErrUntrustedResult) {
				t.Fatalf("untrusted result error = %v", err)
			}
		})
	}
}

func TestWriteTerminalResultIsAtomicAndReadable(t *testing.T) {
	client := newTestClient(t)
	runID := strings.Repeat("a", 64)
	request := Request{RunID: runID, Kind: KindGateway, Version: "v1.2.3", Action: ActionApply}
	if _, err := client.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, 9, 5, 0, 1, 0, 0, time.UTC)
	want := Result{RunID: runID, Kind: KindGateway, Version: "v1.2.3", State: StateSuccess, FinishedAt: &finished}
	if err := WriteResultFile(client.ResultDir, want); err != nil {
		t.Fatal(err)
	}
	got, err := client.Get(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != want.RunID || got.Kind != want.Kind || got.Version != want.Version || got.State != want.State || got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("result=%#v want=%#v", got, want)
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	root := t.TempDir()
	requestDir := filepath.Join(root, "requests")
	resultDir := filepath.Join(root, "results")
	if err := os.Mkdir(requestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(resultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &Client{
		RequestDir: requestDir,
		ResultDir:  resultDir,
		Now:        func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) },
	}
}
