package uirelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func uiJournalFixture(t *testing.T) (Config, *memoryPointers) {
	staging, version, key := makeSignedStaging(t, map[string]string{"index.html": "new UI"})
	root := t.TempDir()
	old, previous := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, v := range []string{old, previous} {
		if err := os.MkdirAll(filepath.Join(root, "releases", v), 0755); err != nil {
			t.Fatal(err)
		}
	}
	p := &memoryPointers{values: map[string]string{"current": old, "previous": previous}}
	return Config{Root: root, StagingDir: staging, PublicKeyFile: key, APIVersion: "v1", StateDir: t.TempDir(), Request: updatetxn.Request{RunID: strings.Repeat("c", 64), Kind: updatetxn.KindUI, Version: version, Action: updatetxn.ActionApply}, Pointers: p, AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil }, HealthCheck: func(context.Context, string) error { return nil }}, p
}

func TestUIRootBindsRequestAndSignedVersion(t *testing.T) {
	cfg, p := uiJournalFixture(t)
	cfg.Request.Version = strings.Repeat("f", 64)
	_, err := Install(t.Context(), cfg)
	if ErrorCode(err) != "request_manifest_mismatch" || p.values["current"] != strings.Repeat("a", 64) {
		t.Fatalf("mismatch accepted: %v", err)
	}
}

func TestUIRollbackReturnsRolledBack(t *testing.T) {
	cfg, _ := uiJournalFixture(t)
	cfg.Request.Action = updatetxn.ActionRollback
	cfg.Request.Version = ""
	result, err := Rollback(t.Context(), cfg)
	if err != nil || result.State != "rolled_back" {
		t.Fatalf("rollback result=%+v err=%v", result, err)
	}
}

func TestUIInterruptedSwitchNeverReapplies(t *testing.T) {
	for _, action := range []updatetxn.Action{updatetxn.ActionApply, updatetxn.ActionRollback} {
		for _, phase := range []string{"before_switch", "after_previous", "after_current", "before_result"} {
			t.Run(string(action)+"/"+phase, func(t *testing.T) {
				cfg, p := uiJournalFixture(t)
				cfg.Request.Action = action
				if action == updatetxn.ActionRollback {
					cfg.Request.Version = ""
				}
				crash := errors.New("injected UI interruption")
				cfg.Fault = func(actual string) error {
					if actual == phase {
						return crash
					}
					return nil
				}
				operation := Install
				if action == updatetxn.ActionRollback {
					operation = Rollback
				}
				if _, err := operation(t.Context(), cfg); !errors.Is(err, crash) {
					t.Fatalf("interruption not reached: %v", err)
				}
				cfg.Fault = nil
				cfg.StagingDir = filepath.Join(t.TempDir(), "unavailable")
				result, err := operation(t.Context(), cfg)
				if err != nil || (result.State != "success" && result.State != "rolled_back") {
					t.Fatalf("recovery replayed apply: %+v %v", result, err)
				}
				if phase != "before_switch" && p.values["previous"] != strings.Repeat("a", 64) {
					t.Fatal("previous overwritten during recovery")
				}
			})
		}
	}
}
