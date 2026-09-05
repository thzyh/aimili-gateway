package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/gatewayupdate"
	_ "modernc.org/sqlite"
)

func runGatewayCommand(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	runID := flags.String("run-id", "", "update run identifier")
	staging := flags.String("staging", "", "release staging directory")
	binary := flags.String("binary", "", "Gateway binary")
	previous := flags.String("previous", "", "single previous Gateway binary")
	configPath := flags.String("config", "", "Gateway configuration")
	database := flags.String("database", "", "Gateway database")
	publicKey := flags.String("public-key", "", "Ed25519 public key")
	healthURL := flags.String("health-url", "http://127.0.0.1:9080/healthz", "Gateway loopback health URL")
	allowInstall := flags.Bool("allow-install", false, "allow a control-plane-only replacement")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	if *runID == "" || *binary == "" || *previous == "" || *configPath == "" || *database == "" {
		fmt.Fprintln(stderr, "run-id, binary, previous, config and database are required")
		return 2
	}
	if command != "gateway-rollback" && (*staging == "" || *publicKey == "") {
		fmt.Fprintln(stderr, "staging and public-key are required")
		return 2
	}
	if command != "gateway-dry-run" && command != "gateway-install" && command != "gateway-rollback" {
		fmt.Fprintln(stderr, "unknown command")
		return 2
	}
	if err := validateGatewayHealthURL(*healthURL); err != nil {
		fmt.Fprintln(stderr, "health-url must be a fixed loopback URL")
		return 2
	}
	probe := &productionProbe{databasePath: *database, healthURL: *healthURL}
	config := gatewayupdate.Config{
		RunID: *runID, StagingDir: *staging, BinaryPath: *binary, PreviousPath: *previous,
		ConfigPath: *configPath, DatabasePath: *database, PublicKeyFile: *publicKey,
		APIVersion: "v1", Platform: "linux-amd64", AllowInstall: *allowInstall,
		Runner: systemdRunner{}, Probe: probe, BusyCheck: databaseBusyCheck(*database),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var result gatewayupdate.Result
	var err error
	switch command {
	case "gateway-dry-run":
		result, err = gatewayupdate.DryRun(ctx, config)
	case "gateway-install":
		result, err = gatewayupdate.Install(ctx, config)
	case "gateway-rollback":
		result, err = gatewayupdate.Rollback(ctx, config)
	}
	if encodeErr := json.NewEncoder(stdout).Encode(result); encodeErr != nil {
		fmt.Fprintln(stderr, "encode_result_failed")
		return 1
	}
	if err != nil {
		code := gatewayupdate.ErrorCode(err)
		if code == "" {
			code = "operation_failed"
		}
		fmt.Fprintln(stderr, code)
		return 1
	}
	return 0
}

type systemdRunner struct{}

func (systemdRunner) Stop(ctx context.Context, service string) error {
	return fixedSystemctl(ctx, "stop", service)
}

func (systemdRunner) Start(ctx context.Context, service string) error {
	return fixedSystemctl(ctx, "start", service)
}

func (systemdRunner) IsActive(ctx context.Context, service string) (bool, error) {
	command := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", service)
	if err := command.Run(); err != nil {
		return false, err
	}
	return true, nil
}

func fixedSystemctl(ctx context.Context, action, service string) error {
	if service != "aimili-gateway.service" || (action != "stop" && action != "start") {
		return errors.New("unsupported systemd operation")
	}
	return exec.CommandContext(ctx, "systemctl", action, service).Run()
}

type productionProbe struct {
	databasePath string
	healthURL    string
}

func (p *productionProbe) Capture(ctx context.Context) (gatewayupdate.Snapshot, error) {
	fingerprint, err := dataPlaneFingerprint(ctx, p.databasePath)
	return gatewayupdate.Snapshot{Fingerprint: fingerprint}, err
}

func (p *productionProbe) Verify(ctx context.Context, before gatewayupdate.Snapshot) error {
	after, err := dataPlaneFingerprint(ctx, p.databasePath)
	if err != nil {
		return err
	}
	if after != before.Fingerprint {
		return errors.New("data-plane invariant changed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.healthURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Gateway health check failed")
	}
	return nil
}

func dataPlaneFingerprint(ctx context.Context, databasePath string) (string, error) {
	hash := sha256.New()
	for _, service := range []string{"aimilivpn.service", "x-ui.service", "caddy.service"} {
		output, err := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", service).Output()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "service=%s,pid=%s\n", service, strings.TrimSpace(string(output)))
	}
	counts, err := processCounts()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(hash, "openvpn=%d,xray=%d\n", counts[0], counts[1])
	database, err := openReadOnlyDatabase(databasePath)
	if err != nil {
		return "", err
	}
	defer database.Close()
	var quickCheck string
	if err := database.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&quickCheck); err != nil || quickCheck != "ok" {
		return "", errors.New("Gateway database quick check failed")
	}
	queries := []string{
		`SELECT egress_id, active_mode, desired_mode, state FROM egress_protocol_modes ORDER BY egress_id`,
		`SELECT id, status, aimili_slot, public_port, mixed_port, config_fingerprint FROM proxy_groups ORDER BY id`,
		`SELECT resource_name, enabled, public_port, mixed_port FROM main_egress ORDER BY id`,
		`SELECT enabled, apply_status FROM mixed_source_policy ORDER BY id`,
	}
	for _, query := range queries {
		rows, err := database.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return "", err
			}
			for _, value := range values {
				fmt.Fprintf(hash, "%v|", value)
			}
			hash.Write([]byte{'\n'})
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func databaseBusyCheck(databasePath string) func(context.Context) error {
	return func(ctx context.Context) error {
		database, err := openReadOnlyDatabase(databasePath)
		if err != nil {
			return err
		}
		defer database.Close()
		queries := []string{
			`SELECT EXISTS(SELECT 1 FROM egress_operations WHERE completed_at = 0)`,
			`SELECT EXISTS(SELECT 1 FROM proxy_operations WHERE result = 'running')`,
			`SELECT EXISTS(SELECT 1 FROM mixed_source_policy WHERE apply_status IN ('pending', 'applying'))`,
		}
		for _, query := range queries {
			var busy bool
			if err := database.QueryRowContext(ctx, query).Scan(&busy); err != nil {
				return err
			}
			if busy {
				return gatewayupdate.ErrOperationBusy
			}
		}
		return nil
	}
}

func openReadOnlyDatabase(filename string) (*sql.DB, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	query := make(url.Values)
	query.Set("mode", "ro")
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(absolute)+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	return database, database.Ping()
}

func processCounts() ([2]int, error) {
	var counts [2]int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return counts, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(body))
		if name == "openvpn" {
			counts[0]++
		}
		if strings.HasPrefix(name, "xray") {
			counts[1]++
		}
	}
	return counts, nil
}

func validateGatewayHealthURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/healthz" {
		return errors.New("invalid health URL")
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return errors.New("health URL is not loopback")
	}
	return nil
}
