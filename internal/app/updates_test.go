package app

import (
	"context"
	"encoding/json"
	"github.com/thzyh/aimili-gateway/internal/buildinfo"
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

func TestEnabledUpdaterDoesNotRequireAPreExistingCatalog(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{UpdateEnabled: true, UpdateRequestDir: filepath.Join(root, "requests"), UpdateResultDir: filepath.Join(root, "results"), UpdateCatalogFile: filepath.Join(root, "missing-catalog.json")}
	for _, path := range []string{cfg.UpdateRequestDir, cfg.UpdateResultDir} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manager := newUpdateManager(cfg)
	if manager == nil {
		t.Fatal("enabled public-release updater was disabled by a missing legacy catalog")
	}
	summary, err := manager.List(context.Background())
	if err != nil || !summary.Enabled || len(summary.Available) != 0 {
		t.Fatalf("summary = %+v, err = %v", summary, err)
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

func TestGatewayApplyUsesCanonicalNewerVersionWhileUIStillRequiresCatalog(t *testing.T) {
	originalVersion := buildinfo.Version
	buildinfo.Version = "v1.2.2"
	t.Cleanup(func() { buildinfo.Version = originalVersion })
	cfg := catalogFixture(t)
	m := newUpdateManager(cfg)
	if _, err := m.Submit(context.Background(), httpapi.UpdateRequest{RunID: strings.Repeat("a", 64), Kind: "gateway", Version: "v1.2.3", Action: "apply"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg.UpdateCatalogFile); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(context.Background(), httpapi.UpdateRequest{RunID: strings.Repeat("b", 64), Kind: "ui", Version: strings.Repeat("c", 64), Action: "apply"}); err != httpapi.ErrUpdatesDisabled {
		t.Fatalf("UI apply without signed catalog was accepted: %v", err)
	}
}
