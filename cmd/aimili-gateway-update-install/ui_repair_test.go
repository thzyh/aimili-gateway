package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func TestUIUncertainRecoveryRetainsJournalAndStagingThroughFinish(t *testing.T) {
	for _, failure := range []string{"current_read", "previous_read", "checkpoint_write"} {
		t.Run(failure, func(t *testing.T) {
			root, cfg, request := spoolFixture(t)
			request.Kind, request.Action, request.Version = updatetxn.KindUI, updatetxn.ActionRollback, ""
			cfg.UIRoot = filepath.Join(root, "ui")
			current, previous := strings.Repeat("b", 64), strings.Repeat("c", 64)
			for _, version := range []string{current, previous} {
				if err := os.MkdirAll(filepath.Join(cfg.UIRoot, "releases", version), 0755); err != nil {
					t.Fatal(err)
				}
			}
			for name, version := range map[string]string{"current": current, "previous": previous} {
				if err := os.Symlink(filepath.Join("releases", version), filepath.Join(cfg.UIRoot, name)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			journalDir := filepath.Join(root, "journal")
			savedJournalDir := filepath.Join(root, "journal.saved")
			if failure != "checkpoint_write" {
				journal := updatetxn.Journal{Request: request, OldDigest: current, OldPrevious: previous, NewDigest: previous, Baseline: "ui-pointers-v1", Phase: "prepared"}
				if err := updatetxn.WriteJournal(root, journal); err != nil {
					t.Fatal(err)
				}
				name := strings.TrimSuffix(failure, "_read")
				pointer := filepath.Join(cfg.UIRoot, name)
				if err := os.Remove(pointer); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pointer, []byte("not a symlink"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var injected atomic.Bool
			injectionResult := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure == "checkpoint_write" && r.URL.Path == "/manifest.json" && injected.CompareAndSwap(false, true) {
					// The new current is already visible. Block the next real
					// journal checkpoint without replacing any production code.
					err := os.Rename(journalDir, savedJournalDir)
					if err == nil {
						err = os.WriteFile(journalDir, []byte("journal I/O obstruction"), 0600)
					}
					injectionResult <- err
				}
				if r.URL.Path == "/manifest.json" {
					fmt.Fprintf(w, `{"version":"%s"}`, previous)
				} else {
					fmt.Fprint(w, "ok")
				}
			}))
			defer server.Close()
			cfg.HealthURL = server.URL + "/healthz"
			staging := filepath.Join(cfg.StagingRoot, request.RunID)
			base := updatetxn.Result{RunID: request.RunID, Kind: request.Kind}
			result := processUIRequestAt(context.Background(), root, base, request, staging, cfg)
			if failure == "checkpoint_write" {
				if !injected.Load() {
					t.Fatal("journal failure injection was not reached")
				}
				if err := <-injectionResult; err != nil {
					t.Fatal(err)
				}
				// Repair only the injected filesystem obstruction so finishRequest
				// can expose whether it wrongly deletes an intact nonterminal journal.
				if err := os.Remove(journalDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(savedJournalDir, journalDir); err != nil {
					t.Fatal(err)
				}
			}
			if result.State != updatetxn.StateRepairRequired || result.ErrorCode != "repair_required" {
				t.Errorf("uncertain UI state downgraded to cleanable result: %+v", result)
			}
			var out, stderr bytes.Buffer
			if code := finishRequest(root, cfg, request, result, &out, &stderr); code != 0 {
				t.Fatalf("finish=%d stderr=%s", code, stderr.String())
			}
			for _, path := range []string{staging, filepath.Join(journalDir, "active.json")} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("uncertain recovery diagnostic deleted: %s: %v", filepath.Base(path), err)
				}
			}
		})
	}
}

func TestUIFinishRequiresConfirmedJournalBeforeCleanup(t *testing.T) {
	root, cfg, request := spoolFixture(t)
	request.Kind = updatetxn.KindUI
	request.Action = updatetxn.ActionRollback
	request.Version = ""
	journal := updatetxn.Journal{Request: request, OldDigest: strings.Repeat("b", 64), OldPrevious: strings.Repeat("c", 64), NewDigest: strings.Repeat("c", 64), Baseline: "ui-pointers-v1", Phase: "switched"}
	if err := updatetxn.WriteJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	result := updatetxn.Result{RunID: request.RunID, Kind: request.Kind, State: updatetxn.StateFailed, ErrorCode: "operation_failed"}
	var out, stderr bytes.Buffer
	if code := finishRequest(root, cfg, request, result, &out, &stderr); code != 0 {
		t.Fatalf("finish=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), `"state":"repair_required"`) {
		t.Fatalf("unverified journal not quarantined: %s", out.String())
	}
	for _, path := range []string{filepath.Join(root, "journal", "active.json"), filepath.Join(cfg.StagingRoot, request.RunID)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unverified diagnostic removed: %v", err)
		}
	}
}
