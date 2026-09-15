package updatefetch

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func TestSignedManifestMustMatchRequestedVersion(t *testing.T) {
	for _, kind := range []updatetxn.Kind{updatetxn.KindGateway, updatetxn.KindUI} {
		t.Run(string(kind), func(t *testing.T) {
			public, private, _ := ed25519.GenerateKey(rand.Reader)
			payload := []byte("signed payload")
			digest := sha256.Sum256(payload)
			hash := hex.EncodeToString(digest[:])
			var manifest []byte
			request := gatewayRequest()
			request.Kind = kind
			if kind == updatetxn.KindGateway {
				manifest, _ = json.Marshal(releaseverify.GatewayManifest{SchemaVersion: 1, Kind: "gateway", Version: "v1.2.4", Commit: "abc1234", BuiltAt: "2026-09-05T00:00:00Z", Platform: "linux-amd64", APIVersion: "v1", ImpactClass: "control-plane-only", Binary: releaseverify.File{Path: "aimili-gateway", SHA256: hash, Bytes: int64(len(payload))}, MinDatabaseSchema: 11, MaxDatabaseSchema: 11})
			} else {
				request.Version = strings.Repeat("a", 64)
				manifest, _ = json.Marshal(releaseverify.UIManifest{SchemaVersion: 1, Kind: "ui", Version: hash, Commit: "abc1234", BuiltAt: "2026-09-05T00:00:00Z", APIVersion: "v1", Archive: releaseverify.Payload{SHA256: hash, Bytes: int64(len(payload))}, Files: []releaseverify.File{{Path: "index.html", SHA256: hash, Bytes: int64(len(payload))}}})
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch filepath.Base(r.URL.Path) {
				case "manifest.json":
					w.Write(manifest)
				case "manifest.sig":
					w.Write(ed25519.Sign(private, manifest))
				default:
					w.Write(payload)
				}
			}))
			defer server.Close()
			fetcher := newTestFetcher(t, server)
			fetcher.Verify = nil
			fetcher.Config.PublicKeyFile = filepath.Join(t.TempDir(), "release.pub")
			if err := os.WriteFile(fetcher.Config.PublicKeyFile, []byte(hex.EncodeToString(public)), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := fetcher.Fetch(context.Background(), request); ErrorCode(err) != "request_manifest_mismatch" {
				t.Fatalf("signed different version accepted: %v", err)
			}
		})
	}
}

func TestInstallerSpoolConfigRejectsNonFixedTargets(t *testing.T) {
	body, err := os.ReadFile("../../deploy/config/updater.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateSpool(); err != nil {
		t.Fatal("example rejected", err)
	}
	for _, field := range []string{"binary", "staging", "config", "ui", "health"} {
		t.Run(field, func(t *testing.T) {
			bad := cfg
			switch field {
			case "binary":
				bad.BinaryPath = "/tmp/arbitrary"
			case "staging":
				bad.StagingRoot = "/tmp/staging"
			case "config":
				bad.GatewayConfigPath = "/tmp/config"
			case "ui":
				bad.UIRoot = "/tmp/ui"
			case "health":
				bad.HealthURL = "http://evil.invalid/healthz"
			}
			if err := bad.ValidateSpool(); err == nil {
				t.Fatal("non-fixed root execution target accepted")
			}
		})
	}
}
