package app

import (
	"context"
	"encoding/json"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSpoolPathsAloneDoNotEnableUpdates(t *testing.T) {
	if manager := newUpdateManager(config.Config{UpdateRequestDir: "requests", UpdateResultDir: "results"}); manager != nil {
		t.Fatal("spool paths implicitly enabled rollback without capability/catalog")
	}
}

func TestUpdateManagerTrustedResultUIDFollowsPlatformOwnerSemantics(t *testing.T) {
	manager := newUpdateManager(catalogFixture(t)).(*updateManager)
	if runtime.GOOS == "windows" {
		if manager.client.TrustedResultUID != nil {
			t.Fatalf("Windows TrustedResultUID = %v, want nil", *manager.client.TrustedResultUID)
		}
		return
	}
	if manager.client.TrustedResultUID == nil || *manager.client.TrustedResultUID != 0 {
		t.Fatalf("TrustedResultUID = %v, want root UID 0", manager.client.TrustedResultUID)
	}
}

func catalogFixture(t *testing.T) config.Config {
	root := t.TempDir()
	cfg := config.Config{UpdateEnabled: true, UpdateRequestDir: filepath.Join(root, "requests"), UpdateResultDir: filepath.Join(root, "results"), UpdateCatalogFile: filepath.Join(root, "catalog.json")}
	for _, p := range []string{cfg.UpdateRequestDir, cfg.UpdateResultDir} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := json.Marshal(updateCatalog{Capability: true, ExpiresAt: time.Now().Add(time.Hour), Available: []httpapi.UpdateVersion{{Kind: "gateway", Version: "v1.2.3", Compatible: true}}})
	if err := os.WriteFile(cfg.UpdateCatalogFile, body, 0600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestEnabledCatalogRequiredForApplyAndRollback(t *testing.T) {
	for _, action := range []string{"apply", "rollback"} {
		t.Run(action, func(t *testing.T) {
			cfg := catalogFixture(t)
			m := newUpdateManager(cfg)
			if m == nil {
				t.Fatal("trusted enabled catalog rejected")
			}
			request := httpapi.UpdateRequest{RunID: strings.Repeat("a", 64), Kind: "gateway", Action: action}
			if action == "apply" {
				request.Version = "v1.2.3"
			}
			if _, err := m.Submit(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(cfg.UpdateCatalogFile); err != nil {
				t.Fatal(err)
			}
			request.RunID = strings.Repeat("b", 64)
			if _, err := m.Submit(context.Background(), request); err != httpapi.ErrUpdatesDisabled {
				t.Fatalf("missing catalog accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg.UpdateRequestDir, request.RunID+".json")); !os.IsNotExist(err) {
				t.Fatal("disabled request created")
			}
		})
	}
}
