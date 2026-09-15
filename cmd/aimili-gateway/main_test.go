package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/buildinfo"
	"github.com/thzyh/aimili-gateway/internal/config"
	_ "modernc.org/sqlite"
)

func TestRunVersionJSON(t *testing.T) {
	originalVersion, originalCommit, originalBuiltAt := buildinfo.Version, buildinfo.Commit, buildinfo.BuiltAt
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuiltAt = originalVersion, originalCommit, originalBuiltAt
	})
	buildinfo.Version, buildinfo.Commit, buildinfo.BuiltAt = "v1.2.3", "abc1234", "2026-09-05T00:00:00Z"

	var stdout, stderr bytes.Buffer
	if code := run([]string{"version", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got["version"] != "v1.2.3" || got["apiVersion"] != "v1" {
		t.Fatalf("version output = %#v", got)
	}
}

func TestRunConfigValidateDoesNotStartService(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "gateway.json")
	body := `{"publicOrigin":"https://gateway.example.test","databasePath":"gateway.db","masterKeyFile":"master.key","aimiliControlTokenFile":"aimili.token","xuiCredentialsFile":"xui.json"}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_CONFIG", configPath)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"config", "validate"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "ok" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunDatabaseCompatibilityCheckIsReadOnly(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "gateway.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL); INSERT INTO schema_migrations VALUES(11, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "gateway.json")
	body := `{"publicOrigin":"https://gateway.example.test","databasePath":` + strconvQuote(databasePath) + `,"masterKeyFile":"master.key","aimiliControlTokenFile":"aimili.token","xuiCredentialsFile":"xui.json"}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_CONFIG", configPath)
	before, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest := sha256.Sum256(before)
	beforeInfo, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"database", "check-compatible", "--min", "11", "--max", "11"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	after, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	afterDigest := sha256.Sum256(after)
	afterInfo, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if beforeDigest != afterDigest || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("compatibility check changed the database")
	}
	if strings.TrimSpace(stdout.String()) != `{"schemaVersion":11,"compatible":true}` {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func strconvQuote(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}

func TestNewHTTPServerUsesApplicationHandler(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called = true
		response.WriteHeader(http.StatusAccepted)
	})
	server := newHTTPServer(config.Config{ListenAddress: "127.0.0.1:9080"}, handler)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusAccepted {
		t.Fatal("application handler was not used")
	}
	if server.Addr != "127.0.0.1:9080" {
		t.Fatalf("server address = %q", server.Addr)
	}
}

func TestNewHTTPServerAllowsLongProxyProvisioning(t *testing.T) {
	server := newHTTPServer(config.Config{ListenAddress: "127.0.0.1:9080"}, http.NotFoundHandler())
	if server.WriteTimeout < 2*time.Minute {
		t.Fatalf("write timeout %s cannot cover Aimili provisioning and protocol validation", server.WriteTimeout)
	}
}
