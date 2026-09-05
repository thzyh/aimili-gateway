package uirelease

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
)

func TestInstallSwitchesCurrentAndPrevious(t *testing.T) {
	root := t.TempDir()
	oldVersion := hex.EncodeToString(bytes.Repeat([]byte{0x11}, sha256.Size))
	if err := os.MkdirAll(filepath.Join(root, "releases", oldVersion), 0o755); err != nil {
		t.Fatal(err)
	}
	pointers := &memoryPointers{values: map[string]string{"current": oldVersion}}
	staging, version, publicKeyFile := makeSignedStaging(t, map[string]string{
		"assets/index-release.js": "ok",
		"index.html":              `<script src="/assets/index-release.js"></script>`,
	})

	result, err := Install(t.Context(), Config{
		StagingDir: staging, Root: root, PublicKeyFile: publicKeyFile, APIVersion: "v1",
		AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil },
		Pointers:       pointers,
		HealthCheck:    func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "success" || result.Version != version || result.PreviousVersion != oldVersion {
		t.Fatalf("install result = %#v", result)
	}
	if pointers.values["current"] != version || pointers.values["previous"] != oldVersion {
		t.Fatalf("pointers = %#v", pointers.values)
	}
	if body, err := os.ReadFile(filepath.Join(root, "releases", version, "index.html")); err != nil || !bytes.Contains(body, []byte("index-release.js")) {
		t.Fatalf("installed index body=%q err=%v", body, err)
	}
}

func TestInstallRejectsArchiveTraversalWithoutProductionWrites(t *testing.T) {
	root := t.TempDir()
	pointers := &memoryPointers{values: map[string]string{}}
	staging, _, publicKeyFile := makeSignedStaging(t, map[string]string{"../escape": "bad", "index.html": "ok"})

	_, err := Install(t.Context(), Config{
		StagingDir: staging, Root: root, PublicKeyFile: publicKeyFile, APIVersion: "v1",
		AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil }, Pointers: pointers,
	})
	if ErrorCode(err) != "invalid_archive" {
		t.Fatalf("traversal error = %v", err)
	}
	if len(pointers.values) != 0 {
		t.Fatalf("pointers changed: %#v", pointers.values)
	}
	if _, statErr := os.Stat(filepath.Join(root, "escape")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("escape file exists: %v", statErr)
	}
}

func TestInstallRejectsExtraStagingAsset(t *testing.T) {
	root := t.TempDir()
	staging, _, publicKeyFile := makeSignedStaging(t, map[string]string{"index.html": "ok"})
	if err := os.WriteFile(filepath.Join(staging, "unexpected"), []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Install(t.Context(), Config{
		StagingDir: staging, Root: root, PublicKeyFile: publicKeyFile, APIVersion: "v1",
		AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil }, Pointers: &memoryPointers{values: map[string]string{}},
	})
	if ErrorCode(err) != "invalid_staging" {
		t.Fatalf("extra staging asset error = %v", err)
	}
}

func TestInstallHealthFailureLeavesCurrentUntouched(t *testing.T) {
	root := t.TempDir()
	oldVersion := hex.EncodeToString(bytes.Repeat([]byte{0x22}, sha256.Size))
	if err := os.MkdirAll(filepath.Join(root, "releases", oldVersion), 0o755); err != nil {
		t.Fatal(err)
	}
	pointers := &memoryPointers{values: map[string]string{"current": oldVersion}}
	staging, version, publicKeyFile := makeSignedStaging(t, map[string]string{"index.html": "new"})

	_, err := Install(t.Context(), Config{
		StagingDir: staging, Root: root, PublicKeyFile: publicKeyFile, APIVersion: "v1",
		AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil }, Pointers: pointers,
		HealthCheck: func(context.Context, string) error { return errors.New("HTTP check failed") },
	})
	if ErrorCode(err) != "health_check_failed" {
		t.Fatalf("health error = %v", err)
	}
	if pointers.values["current"] != oldVersion || pointers.values["previous"] != "" {
		t.Fatalf("pointers changed after failure: %#v", pointers.values)
	}
	if _, statErr := os.Stat(filepath.Join(root, "releases", version)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed release remains: %v", statErr)
	}
}

func TestRollbackSwapsCurrentAndPrevious(t *testing.T) {
	root := t.TempDir()
	current := hex.EncodeToString(bytes.Repeat([]byte{0x33}, sha256.Size))
	previous := hex.EncodeToString(bytes.Repeat([]byte{0x44}, sha256.Size))
	for _, version := range []string{current, previous} {
		if err := os.MkdirAll(filepath.Join(root, "releases", version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pointers := &memoryPointers{values: map[string]string{"current": current, "previous": previous}}
	result, err := Rollback(t.Context(), Config{
		Root: root, Pointers: pointers,
		HealthCheck: func(_ context.Context, version string) error {
			if version != previous {
				t.Fatalf("health checked version %q, want %q", version, previous)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "success" || pointers.values["current"] != previous || pointers.values["previous"] != current {
		t.Fatalf("rollback result=%#v pointers=%#v", result, pointers.values)
	}
}

func TestSuccessfulInstallKeepsOnlyCurrentAndPrevious(t *testing.T) {
	root := t.TempDir()
	oldVersion := hex.EncodeToString(bytes.Repeat([]byte{0x55}, sha256.Size))
	staleVersion := hex.EncodeToString(bytes.Repeat([]byte{0x66}, sha256.Size))
	for _, version := range []string{oldVersion, staleVersion} {
		if err := os.MkdirAll(filepath.Join(root, "releases", version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pointers := &memoryPointers{values: map[string]string{"current": oldVersion, "previous": staleVersion}}
	staging, version, publicKeyFile := makeSignedStaging(t, map[string]string{"index.html": "new"})
	if _, err := Install(t.Context(), Config{
		StagingDir: staging, Root: root, PublicKeyFile: publicKeyFile, APIVersion: "v1", Pointers: pointers,
		AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil },
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "releases"))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{entries[0].Name(), entries[1].Name()}
	sort.Strings(got)
	want := []string{oldVersion, version}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("release directories=%v, want %v", got, want)
	}
}

func TestAvailableBytesReturnsCapacityForExistingDirectory(t *testing.T) {
	available, err := AvailableBytes(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if available == 0 {
		t.Fatal("available byte count is zero")
	}
}

type memoryPointers struct{ values map[string]string }

func (p *memoryPointers) Get(_ string, name string) (string, error) {
	value, ok := p.values[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}
func (p *memoryPointers) Set(_ string, name, version string) error {
	p.values[name] = version
	return nil
}
func (p *memoryPointers) Remove(_ string, name string) error { delete(p.values, name); return nil }

func makeSignedStaging(t *testing.T, files map[string]string) (string, string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	manifestFiles := make([]releaseverify.File, 0, len(names))
	for _, name := range names {
		body := []byte(files[name])
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), ModTime: time.Unix(0, 0)}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		manifestFiles = append(manifestFiles, releaseverify.File{Path: name, SHA256: fmt.Sprintf("%x", digest), Bytes: int64(len(body))})
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	archiveDigest := sha256.Sum256(archive.Bytes())
	version := fmt.Sprintf("%x", archiveDigest)
	manifest, err := json.Marshal(releaseverify.UIManifest{
		SchemaVersion: 1, Kind: "ui", Version: version, Commit: "abc1234", BuiltAt: "2026-09-05T00:00:00Z", APIVersion: "v1",
		Archive: releaseverify.Payload{SHA256: version, Bytes: int64(archive.Len())}, Files: manifestFiles,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"manifest.json": manifest,
		"manifest.sig":  ed25519.Sign(privateKey, manifest),
		"ui.tar.gz":     archive.Bytes(),
	} {
		if err := os.WriteFile(filepath.Join(staging, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	publicKeyFile := filepath.Join(t.TempDir(), "ui.pub")
	if err := os.WriteFile(publicKeyFile, []byte(hex.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	return staging, version, publicKeyFile
}
