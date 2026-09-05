package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/gatewayupdate"
)

func TestDatabaseBusyCheckIgnoresOnlySafeOrphanedEgressOperations(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name  string
		setup func(*testing.T, *sql.DB)
		want  error
	}{
		{
			name: "ignores an old unreferenced operation when its mode is ready",
			setup: func(t *testing.T, database *sql.DB) {
				insertMode(t, database, "agw-main", "ready", "completed-operation")
				insertOperation(t, database, "orphaned-operation", "agw-main", now.Add(-72*time.Hour).UnixMilli())
			},
		},
		{
			name: "blocks a recent unreferenced operation",
			setup: func(t *testing.T, database *sql.DB) {
				insertMode(t, database, "agw-main", "ready", "completed-operation")
				insertOperation(t, database, "recent-operation", "agw-main", now.Add(-14*time.Minute).UnixMilli())
			},
			want: gatewayupdate.ErrOperationBusy,
		},
		{
			name: "blocks an old operation referenced by its mode",
			setup: func(t *testing.T, database *sql.DB) {
				insertMode(t, database, "agw-main", "ready", "current-operation")
				insertOperation(t, database, "current-operation", "agw-main", now.Add(-72*time.Hour).UnixMilli())
			},
			want: gatewayupdate.ErrOperationBusy,
		},
	}
	for _, state := range []string{"switching", "subscription_pending", "rolling_back", "repair_required"} {
		tests = append(tests, struct {
			name  string
			setup func(*testing.T, *sql.DB)
			want  error
		}{
			name: "blocks mode in " + state + " state",
			setup: func(t *testing.T, database *sql.DB) {
				insertMode(t, database, "agw-main", state, "")
			},
			want: gatewayupdate.ErrOperationBusy,
		})
	}
	for _, blocker := range []struct {
		name  string
		setup func(*testing.T, *sql.DB)
	}{
		{
			name: "a running proxy operation",
			setup: func(t *testing.T, database *sql.DB) {
				mustExec(t, database, `INSERT INTO proxy_operations(result) VALUES ('running')`)
			},
		},
		{
			name: "a pending mixed source policy",
			setup: func(t *testing.T, database *sql.DB) {
				mustExec(t, database, `INSERT INTO mixed_source_policy(apply_status) VALUES ('pending')`)
			},
		},
		{
			name: "an applying mixed source policy",
			setup: func(t *testing.T, database *sql.DB) {
				mustExec(t, database, `INSERT INTO mixed_source_policy(apply_status) VALUES ('applying')`)
			},
		},
	} {
		tests = append(tests, struct {
			name  string
			setup func(*testing.T, *sql.DB)
			want  error
		}{
			name:  "blocks " + blocker.name,
			setup: blocker.setup,
			want:  gatewayupdate.ErrOperationBusy,
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "gateway.db")
			database := createBusyCheckDatabase(t, databasePath)
			test.setup(t, database)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			err := databaseBusyCheck(databasePath)(context.Background())
			if !errors.Is(err, test.want) || (test.want == nil && err != nil) {
				t.Fatalf("databaseBusyCheck() error = %v, want %v", err, test.want)
			}
		})
	}
}

func createBusyCheckDatabase(t *testing.T, databasePath string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE egress_protocol_modes (egress_id TEXT PRIMARY KEY, state TEXT NOT NULL, last_operation_id TEXT NOT NULL)`,
		`CREATE TABLE egress_operations (operation_id TEXT PRIMARY KEY, egress_id TEXT NOT NULL, started_at INTEGER NOT NULL, completed_at INTEGER NOT NULL)`,
		`CREATE TABLE proxy_operations (result TEXT NOT NULL)`,
		`CREATE TABLE mixed_source_policy (apply_status TEXT NOT NULL)`,
	} {
		mustExec(t, database, statement)
	}
	return database
}

func insertMode(t *testing.T, database *sql.DB, egressID, state, lastOperationID string) {
	t.Helper()
	mustExec(t, database, `INSERT INTO egress_protocol_modes(egress_id, state, last_operation_id) VALUES (?, ?, ?)`, egressID, state, lastOperationID)
}

func insertOperation(t *testing.T, database *sql.DB, operationID, egressID string, startedAt int64) {
	t.Helper()
	mustExec(t, database, `INSERT INTO egress_operations(operation_id, egress_id, started_at, completed_at) VALUES (?, ?, ?, 0)`, operationID, egressID, startedAt)
}

func mustExec(t *testing.T, database *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := database.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsIncompleteUIInstallArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"ui-install", "--root", t.TempDir()}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunRejectsIncompleteGatewayInstallArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"gateway-install", "--root", t.TempDir()}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunSpoolRequiresConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"spool"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "config is required") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestDirectCLIRejectsArbitraryExecutionInputs(t *testing.T) {
	for _, command := range []string{"gateway-install", "gateway-dry-run", "gateway-rollback"} {
		t.Run(command, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := run([]string{command, "--run-id", strings.Repeat("a", 64), "--staging", t.TempDir(), "--binary", filepath.Join(t.TempDir(), "attacker"), "--previous", filepath.Join(t.TempDir(), "previous"), "--config", filepath.Join(t.TempDir(), "config"), "--database", filepath.Join(t.TempDir(), "database"), "--public-key", "fixture.pub"}, &out, &stderr)
			if code != 2 {
				t.Fatalf("arbitrary execution paths accepted: code=%d error=%s", code, stderr.String())
			}
		})
	}
}

func TestGatewayHealthCheckReportsManifestVersionMismatchSafely(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"version":"other"}`))
	}))
	defer server.Close()
	err := gatewayHealthCheck(server.URL)(context.Background(), strings.Repeat("a", 64))
	if healthDetail(err) != "manifest_version_mismatch" || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("health error=%v detail=%q", err, healthDetail(err))
	}
}

func TestGatewayHealthCheckValidatesManifestIndexAndAssets(t *testing.T) {
	version := strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest.json":
			_, _ = response.Write([]byte(`{"version":"` + version + `"}`))
		case "/":
			_, _ = response.Write([]byte(`<script src="/assets/index.js"></script>`))
		case "/assets/index.js":
			_, _ = response.Write([]byte("ok"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	if err := gatewayHealthCheck(server.URL)(context.Background(), version); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsNonLoopbackHealthURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"ui-install", "--root", t.TempDir(), "--staging", t.TempDir(),
		"--public-key", "fixture.pub", "--health-url", "https://example.invalid",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
