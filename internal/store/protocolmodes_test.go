package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

func TestProtocolMigrationAddsNeutralPublicResourcesAndNoSecrets(t *testing.T) {
	store := openTestStore(t)
	for table, want := range map[string][]string{
		"proxy_groups": {"public_inbound_id", "public_port"},
		"main_egress":  {"candidate_id", "public_inbound_id", "public_port"},
	} {
		columns := tableColumns(t, store.db, table)
		for _, column := range want {
			if !columns[column] {
				t.Fatalf("%s missing %s", table, column)
			}
		}
		if columns["vless_port"] || columns["vless_inbound_id"] {
			t.Fatalf("%s retained a second writable VLESS resource source", table)
		}
	}
	for _, table := range []string{"egress_protocol_modes", "egress_operations"} {
		columns := tableColumns(t, store.db, table)
		if len(columns) == 0 {
			t.Fatalf("missing table %s", table)
		}
		for column := range columns {
			lower := strings.ToLower(column)
			for _, forbidden := range []string{"uuid", "auth", "password", "private", "uri", "cookie"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("%s contains secret-bearing column %s", table, column)
				}
			}
		}
	}
}

func TestProtocolModeStoreUsesOptimisticVersioning(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	record := domain.EgressProtocolMode{
		EgressID:    "agw-main",
		ActiveMode:  domain.ProtocolVLESSTCPRealityVision,
		DesiredMode: domain.ProtocolVLESSTCPRealityVision,
		State:       domain.ProtocolReady,
		Version:     1,
		UpdatedAt:   now,
	}
	if err := store.CreateEgressProtocolMode(ctx, record); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEgressProtocolMode(ctx, "agw-main")
	if err != nil || got.ActiveMode != record.ActiveMode || got.Version != 1 {
		t.Fatalf("record = %#v, err = %v", got, err)
	}
	got.State = domain.ProtocolSwitching
	got.DesiredMode = domain.ProtocolVLESSXHTTPReality
	got.LastOperationID = "operation-safe-1"
	got.LastRequestHash = strings.Repeat("a", 64)
	got.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateEgressProtocolMode(ctx, got, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateEgressProtocolMode(ctx, got, 1); !errors.Is(err, ErrEgressProtocolChanged) {
		t.Fatalf("stale update = %v", err)
	}
}

func TestEgressOperationRoundTripContainsOnlySafeMetadata(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	operation := EgressOperation{
		OperationID:   "operation-safe-1",
		EgressID:      "agw-main",
		Kind:          "main_assign",
		Phase:         "stage",
		RequestHash:   strings.Repeat("b", 64),
		TransactionID: "root-safe-reference",
		StartedAt:     now,
	}
	if err := store.CreateEgressOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetEgressOperationByRequestHash(ctx, operation.EgressID, operation.Kind, operation.RequestHash)
	if err != nil || got.OperationID != operation.OperationID || got.Phase != "stage" {
		t.Fatalf("operation = %#v, err = %v", got, err)
	}
}

func tableColumns(t *testing.T, database *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	return columns
}
