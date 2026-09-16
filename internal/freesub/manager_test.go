package freesub

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestFailedReplacementPersistsWaitingManualAfterReservationVersionAdvance(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-one", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupDegraded, Version: 1, CandidateConfig: []byte(`{"type":"vless"}`),
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	if err := connection.BeginAutoReplacement("fs-us-one:probe_failed"); err != nil {
		t.Fatal(err)
	}
	if err := database.PutFreesubBackup(ctx, connection, 1, key); err != nil {
		t.Fatal(err)
	}
	connection.Version++
	manager := &Manager{store: database, masterKey: key}
	got, gotErr := manager.failReplacement(ctx, connection, "candidate_start_failed", errors.New("offline"))
	if gotErr == nil || got.Status != domain.FreesubBackupWaitingManual || got.RepairAttempts != 1 {
		t.Fatalf("result = %#v, err=%v", got, gotErr)
	}
	persisted, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.FreesubBackupWaitingManual || persisted.RepairAttempts != 1 || persisted.Version != 3 {
		t.Fatalf("persisted = %#v", persisted)
	}
}

func TestRecoverDoesNotRetryWaitingManualConnection(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-one", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupWaitingManual, RepairAttempts: 1, Version: 1,
		CandidateConfig: []byte(`{"type":"vless"}`),
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: database, masterKey: key}
	if err := manager.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	persisted, _ := database.GetFreesubBackup(ctx)
	if persisted.Version != 1 || persisted.Status != domain.FreesubBackupWaitingManual {
		t.Fatalf("recover changed waiting-manual state: %#v", persisted)
	}
}

func TestCheckKeepsWaitingManualTerminalState(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-one", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupWaitingManual, RepairAttempts: 1, Version: 1,
		CandidateConfig: []byte(`{"type":"vless"}`),
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: database, masterKey: key}
	if _, err := manager.Check(ctx); err == nil {
		t.Fatal("waiting-manual check unexpectedly succeeded")
	}
	persisted, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.FreesubBackupWaitingManual || persisted.RepairAttempts != 1 || persisted.Version != 1 {
		t.Fatalf("check changed terminal state: %#v", persisted)
	}
}

func TestNormalizeLegacyFreesubClientUUID(t *testing.T) {
	got, err := normalizeUUID([]byte("00112233445566778899aabbccddeeff"))
	if err != nil || string(got) != "00112233-4455-6677-8899-aabbccddeeff" {
		t.Fatalf("uuid=%q err=%v", got, err)
	}
}
