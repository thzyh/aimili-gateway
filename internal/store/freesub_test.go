package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

func TestFreesubBackupConfigIsEncryptedAndVersioned(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gateway.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	plaintext := []byte(`{"type":"vless","uuid":"secret"}`)
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-1", CountryCode: "IN", Protocol: "vless",
		Status: domain.FreesubBackupReady, Version: 1, CandidateConfig: plaintext,
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == string(plaintext) || string(raw) == "" {
		t.Fatal("candidate configuration was stored in plaintext")
	}
	stored, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.CandidateConfig) == string(plaintext) {
		t.Fatal("stored candidate configuration was not ciphertext")
	}
	decrypted, err := database.GetFreesubBackupConfig(ctx, key)
	if err != nil || string(decrypted.CandidateConfig) != string(plaintext) {
		t.Fatalf("decrypt candidate config: err=%v config=%q", err, decrypted.CandidateConfig)
	}
	if err := database.PutFreesubBackup(ctx, connection, stored.Version, key); err != nil {
		t.Fatal(err)
	}
}
