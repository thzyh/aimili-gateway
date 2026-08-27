package accountsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestMigrateLegacyCredentialsRefreshesUntilFirstSuccessfulSync(t *testing.T) {
	directory := t.TempDir()
	database, err := store.Open(t.Context(), filepath.Join(directory, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	legacyPath := filepath.Join(directory, "xui-automation.json")
	writeLegacyCredentials(t, legacyPath, "initial-password-marker")
	key := []byte("01234567890123456789012345678901")

	if err := MigrateLegacyCredentials(t.Context(), database, legacyPath, key); err != nil {
		t.Fatal(err)
	}
	writeLegacyCredentials(t, legacyPath, "aligned-password-marker")
	if err := MigrateLegacyCredentials(t.Context(), database, legacyPath, key); err != nil {
		t.Fatal(err)
	}
	credentials, err := database.LoadUnifiedCredentials(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Username != "owner" || string(credentials.Password) != "aligned-password-marker" {
		t.Fatal("pending initial credentials did not refresh from the current migration input")
	}
	clear(credentials.Password)

	if err := database.SetAccountSyncState(t.Context(), store.AccountSyncState{
		Status:              store.AccountSyncSynced,
		UsernameFingerprint: "committed-fingerprint",
	}); err != nil {
		t.Fatal(err)
	}
	writeLegacyCredentials(t, legacyPath, "stale-password-marker")
	if err := MigrateLegacyCredentials(t.Context(), database, legacyPath, key); err != nil {
		t.Fatal(err)
	}
	credentials, err = database.LoadUnifiedCredentials(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(credentials.Password)
	if string(credentials.Password) != "aligned-password-marker" {
		t.Fatal("a stale migration input overwrote successfully synchronized credentials")
	}
}

func writeLegacyCredentials(t *testing.T, path, password string) {
	t.Helper()
	content := []byte(`{"username":"owner","password":"` + password + `"}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
