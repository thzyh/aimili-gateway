package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/updatefetch"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func spoolFixture(t *testing.T) (string, updatefetch.Config, updatetxn.Request) {
	root := t.TempDir()
	cfg := updatefetch.Config{RequestDir: filepath.Join(root, "requests"), ResultDir: filepath.Join(root, "results"), StagingRoot: filepath.Join(root, "staging")}
	for _, p := range []string{cfg.RequestDir, cfg.ResultDir, cfg.StagingRoot} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	request := updatetxn.Request{RunID: strings.Repeat("a", 64), Kind: updatetxn.KindGateway, Version: "v1.2.3", Action: updatetxn.ActionApply}
	client := updatetxn.Client{RequestDir: cfg.RequestDir, ResultDir: cfg.ResultDir}
	if _, err := client.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cfg.StagingRoot, request.RunID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.StagingRoot, request.RunID, "download.complete"), []byte(`{"errorCode":"download_failed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return root, cfg, request
}

func TestEveryInstallerEntrySharesRootExecutionLease(t *testing.T) {
	root, cfg, request := spoolFixture(t)
	release, err := updatetxn.AcquireExecutionLease(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"spool", "gateway-apply", "gateway-rollback", "ui-apply", "ui-rollback"} {
		var direct *updatetxn.Request
		copy := request
		if name != "spool" {
			direct = &copy
			if strings.HasPrefix(name, "ui") {
				copy.Kind = updatetxn.KindUI
			}
			if strings.HasSuffix(name, "rollback") {
				copy.Action = updatetxn.ActionRollback
				copy.Version = ""
			}
		}
		var out, stderr bytes.Buffer
		if code := runInstallerAt(root, cfg, direct, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "update_busy") {
			t.Fatalf("%s escaped root lease: %d %s", name, code, stderr.String())
		}
	}
	release()
	var out, stderr bytes.Buffer
	if code := runInstallerAt(root, cfg, nil, &out, &stderr); code != 0 {
		t.Fatalf("released lease did not permit safe consume: %s", stderr.String())
	}
}

func TestTerminalResultPrecedesPreciseConsumptionAndLeaseSurvives(t *testing.T) {
	root, cfg, request := spoolFixture(t)
	var out, stderr bytes.Buffer
	if code := runInstallerAt(root, cfg, nil, &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	if paths, _ := filepath.Glob(filepath.Join(cfg.RequestDir, "*.json")); len(paths) != 0 {
		t.Fatalf("path unit still triggered: %v", paths)
	}
	if _, err := os.Stat(filepath.Join(cfg.StagingRoot, request.RunID)); !os.IsNotExist(err) {
		t.Fatal("staging not consumed")
	}
	if _, err := os.Stat(filepath.Join(cfg.RequestDir, ".update.lease")); err != nil {
		t.Fatal("lease removed before client observed result")
	}
	client := updatetxn.Client{RequestDir: cfg.RequestDir, ResultDir: cfg.ResultDir}
	result, err := client.Get(context.Background(), request.RunID)
	if err != nil || result.State != updatetxn.StateFailed {
		t.Fatalf("durable terminal unavailable: %+v %v", result, err)
	}
}

func TestResultRetentionProtectsLeaseAndCurrentRun(t *testing.T) {
	_, cfg, request := spoolFixture(t)
	now := time.Now()
	for i := 1; i < 75; i++ {
		id := fmt.Sprintf("%064x", i)
		if i == 1 {
			id = request.RunID
		}
		if err := updatetxn.WriteResultFile(cfg.ResultDir, updatetxn.Result{RunID: id, Kind: request.Kind, State: updatetxn.StateSuccess, FinishedAt: &now}); err != nil {
			t.Fatal(err)
		}
	}
	current := fmt.Sprintf("%064x", 74)
	if err := updatetxn.PruneResults(cfg.ResultDir, cfg.RequestDir, current, 64); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cfg.ResultDir)
	if len(entries) != 64 {
		t.Fatalf("unbounded results: %d", len(entries))
	}
	for _, id := range []string{request.RunID, current} {
		if _, err := os.Stat(filepath.Join(cfg.ResultDir, id+".json")); err != nil {
			t.Fatal("required result pruned")
		}
	}
}

func TestMalformedDownloadedMarkerFailsClosed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "download.complete")
	if err := os.WriteFile(file, []byte(`{bad json`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := stagedFetchError(file); got != "fetch_failed" {
		t.Fatalf("malformed marker accepted: %q", got)
	}
}
