//go:build !windows

package uirelease

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestInstallMakesReleaseDirectoryTraversableByGateway(t *testing.T) {
	previousMask := syscall.Umask(0o077)
	defer syscall.Umask(previousMask)
	root := t.TempDir()
	staging, version, publicKeyFile := makeSignedStaging(t, map[string]string{
		"index.html":           `<script src="/assets/chunks/app.js"></script>`,
		"assets/chunks/app.js": "ok",
	})
	if _, err := Install(t.Context(), Config{
		StagingDir: staging, Root: root, PublicKeyFile: publicKeyFile, APIVersion: "v1",
		AvailableBytes: func(string) (uint64, error) { return 1 << 30, nil },
		Pointers:       &memoryPointers{values: map[string]string{}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"", "assets", "assets/chunks"} {
		info, err := os.Stat(filepath.Join(root, "releases", version, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("directory %q mode=%#o, want 0755", relative, info.Mode().Perm())
		}
	}
}
