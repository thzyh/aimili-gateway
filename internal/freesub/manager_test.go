package freesub

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestCheckRetiresCandidateRemovedFromLatestFeed(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-one", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupReady, Version: 1, CandidateConfig: []byte(`{"type":"vless"}`),
		RuntimePID: int64(os.Getpid()), SocksPort: 1,
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	feedPath := filepath.Join(t.TempDir(), "gateway-candidates.json")
	feed := Feed{
		SchemaVersion: 1, GeneratedAt: "2026-09-17T00:00:00Z",
		Candidates: []Candidate{{
			CandidateID: "fs-tr-one", Country: "TR", Protocol: "vless", RiskScore: 20,
			Config: map[string]any{"type": "vless"},
		}},
	}
	data, err := json.Marshal(feed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(feedPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	currentProcess, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		cfg: ManagerConfig{FeedPath: feedPath}, store: database, masterKey: key,
		process: &exec.Cmd{Process: currentProcess},
	}

	got, gotErr := manager.Check(ctx)
	if gotErr == nil || got.Status != domain.FreesubBackupWaitingManual || got.RepairAttempts != 1 {
		t.Fatalf("result = %#v, err=%v", got, gotErr)
	}
	if got.FailureFingerprint != "fs-us-one:candidate_retired" || got.LastErrorCode != "no_same_country_candidate" {
		t.Fatalf("candidate retirement was not persisted: %#v", got)
	}
}

func TestCheckAutomaticallyReservesSingleReplacementAttempt(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_, _ = connection.Write([]byte{5, 0xff})
			_ = connection.Close()
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-one", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupReady, Version: 1, CandidateConfig: []byte(`{"type":"vless"}`),
		RuntimePID: int64(os.Getpid()), SocksPort: listener.Addr().(*net.TCPAddr).Port,
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	currentProcess, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		cfg:       ManagerConfig{FeedPath: filepath.Join(t.TempDir(), "missing.json"), SingBoxPath: executable},
		store:     database,
		masterKey: key,
		process:   &exec.Cmd{Process: currentProcess},
	}
	got, gotErr := manager.Check(ctx)
	if gotErr == nil || got.Status != domain.FreesubBackupWaitingManual || got.RepairAttempts != 1 {
		t.Fatalf("result = %#v, err=%v", got, gotErr)
	}
	persisted, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.FreesubBackupWaitingManual || persisted.RepairAttempts != 1 || persisted.LastErrorCode != "feed_unavailable" {
		t.Fatalf("persisted = %#v", persisted)
	}
	if _, err := manager.Check(ctx); err == nil {
		t.Fatal("second check unexpectedly retried replacement")
	}
	again, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != persisted.Version || again.RepairAttempts != 1 {
		t.Fatalf("second check changed terminal state: before=%#v after=%#v", persisted, again)
	}
}

func TestCheckAutomaticallyReplacesUnavailableRuntime(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-one", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupReady, Version: 1, CandidateConfig: []byte(`{"type":"vless"}`),
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		cfg:       ManagerConfig{FeedPath: filepath.Join(t.TempDir(), "missing.json")},
		store:     database,
		masterKey: key,
	}
	got, gotErr := manager.Check(ctx)
	if gotErr == nil || got.Status != domain.FreesubBackupWaitingManual || got.RepairAttempts != 1 || got.LastErrorCode != "feed_unavailable" {
		t.Fatalf("result = %#v, err=%v", got, gotErr)
	}
	if _, err := manager.Check(ctx); err == nil {
		t.Fatal("second check unexpectedly retried replacement")
	}
	persisted, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.FreesubBackupWaitingManual || persisted.RepairAttempts != 1 {
		t.Fatalf("persisted = %#v", persisted)
	}
}

func TestCheckMovesExhaustedUnavailableRuntimeToWaitingManual(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-two", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupReady, RepairAttempts: 1, Version: 1,
		CandidateConfig: []byte(`{"type":"vless"}`),
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: database, masterKey: key}
	got, gotErr := manager.Check(ctx)
	if gotErr == nil || got.Status != domain.FreesubBackupWaitingManual || got.RepairAttempts != 1 {
		t.Fatalf("result = %#v, err=%v", got, gotErr)
	}
	before := got.Version
	if _, err := manager.Check(ctx); err == nil {
		t.Fatal("waiting-manual check unexpectedly succeeded")
	}
	persisted, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Version != before || persisted.Status != domain.FreesubBackupWaitingManual || persisted.RepairAttempts != 1 {
		t.Fatalf("second check changed terminal state: %#v", persisted)
	}
}

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

func TestRecoverAutomaticallyReservesSingleReplacementAttempt(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	key := []byte("01234567890123456789012345678901")
	connection := domain.FreesubBackupConnection{
		ID: "agw-freesub", CandidateID: "fs-us-one", CountryCode: "US", Protocol: "vless",
		Status: domain.FreesubBackupReady, Version: 1, CandidateConfig: []byte(`{"type":"vless","tag":"node"}`),
	}
	if err := database.PutFreesubBackup(ctx, connection, 0, key); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		cfg: ManagerConfig{
			FeedPath:    filepath.Join(t.TempDir(), "missing-feed.json"),
			SingBoxPath: filepath.Join(t.TempDir(), "missing-sing-box"),
			StateDir:    t.TempDir(),
			SocksPort:   18080,
		},
		store:     database,
		masterKey: key,
	}
	if err := manager.Recover(ctx); err == nil {
		t.Fatal("recovery unexpectedly succeeded")
	}
	persisted, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.FreesubBackupWaitingManual || persisted.RepairAttempts != 1 || persisted.LastErrorCode != "feed_unavailable" {
		t.Fatalf("persisted = %#v", persisted)
	}
	version := persisted.Version
	if err := manager.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != version || again.RepairAttempts != 1 || again.Status != domain.FreesubBackupWaitingManual {
		t.Fatalf("second recovery changed terminal state: before=%#v after=%#v", persisted, again)
	}
}

func TestRecoverAutomaticallyReplacesPersistedDegradedRuntime(t *testing.T) {
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
	manager := &Manager{
		cfg:       ManagerConfig{FeedPath: filepath.Join(t.TempDir(), "missing-feed.json")},
		store:     database,
		masterKey: key,
	}
	if err := manager.Recover(ctx); err == nil {
		t.Fatal("degraded recovery unexpectedly succeeded")
	}
	persisted, err := database.GetFreesubBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.FreesubBackupWaitingManual || persisted.RepairAttempts != 1 || persisted.LastErrorCode != "feed_unavailable" {
		t.Fatalf("persisted = %#v", persisted)
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

func TestNeedsPublicProvision(t *testing.T) {
	tests := []struct {
		name       string
		connection domain.FreesubBackupConnection
		want       bool
	}{
		{name: "missing both", want: true},
		{name: "missing inbound", connection: domain.FreesubBackupConnection{PublicPort: 22000}, want: true},
		{name: "missing public port", connection: domain.FreesubBackupConnection{XUIInboundID: 60}, want: true},
		{name: "existing managed resources", connection: domain.FreesubBackupConnection{XUIInboundID: 60, PublicPort: 22000}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsPublicProvision(tt.connection); got != tt.want {
				t.Fatalf("needsPublicProvision() = %v, want %v", got, tt.want)
			}
		})
	}
}
