package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	databasePath    string
	xuiDatabasePath string
	healthURL       string
	serviceState    func(context.Context, string) ([]byte, error)
	countProcesses  func() ([2]int, error)
}

func (p *productionProbe) Capture(ctx context.Context) (gatewayupdate.Snapshot, error) {
	fingerprint, err := p.fingerprint(ctx)
	return gatewayupdate.Snapshot{Fingerprint: fingerprint}, err
}

func (p *productionProbe) Verify(ctx context.Context, before gatewayupdate.Snapshot) error {
	after, err := p.fingerprint(ctx)
	if err != nil {
		return err
	}
	if after != before.Fingerprint {
		return errors.New("data-plane invariant changed")
	}
	return p.WaitReady(ctx)
}

func (p *productionProbe) WaitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := p.healthOnce(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *productionProbe) healthOnce(ctx context.Context) error {
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
	return (&productionProbe{databasePath: databasePath}).fingerprint(ctx)
}

func (p *productionProbe) fingerprint(ctx context.Context) (string, error) {
	readService := p.serviceState
	if readService == nil {
		readService = func(ctx context.Context, service string) ([]byte, error) {
			return exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--property=ActiveState", service).Output()
		}
	}
	hash := sha256.New()
	for _, service := range []string{"aimili-gateway.service", "aimilivpn.service", "x-ui.service", "caddy.service"} {
		output, err := readService(ctx, service)
		if err != nil {
			return "", err
		}
		fields := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				fields[key] = value
			}
		}
		pid, parseErr := strconv.ParseInt(fields["MainPID"], 10, 64)
		if fields["ActiveState"] != "active" || parseErr != nil || pid <= 0 {
			return "", errors.New("required service is not active with a valid PID")
		}
		if service != "aimili-gateway.service" {
			fmt.Fprintf(hash, "service=%s,pid=%d\n", service, pid)
		}
	}
	count := p.countProcesses
	if count == nil {
		count = processCounts
	}
	counts, err := count()
	if err != nil {
		return "", err
	}
	if counts[0] < 1 || counts[1] != 1 {
		return "", errors.New("required process inventory is unavailable")
	}
	fmt.Fprintf(hash, "openvpn=%d,xray=%d\n", counts[0], counts[1])
	database, err := openReadOnlyDatabase(p.databasePath)
	if err != nil {
		return "", err
	}
	defer database.Close()
	var quickCheck string
	if err := database.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&quickCheck); err != nil || quickCheck != "ok" {
		return "", errors.New("Gateway database quick check failed")
	}
	var modeCount, readyModeCount, groupCount, validGroupCount, distinctPortCount, matchedModeCount, mainCount int
	for _, gate := range []struct {
		query string
		value *int
	}{
		{`SELECT count(*) FROM egress_protocol_modes`, &modeCount},
		{`SELECT count(*) FROM egress_protocol_modes WHERE state='ready' AND active_mode=desired_mode`, &readyModeCount},
		{`SELECT count(*) FROM proxy_groups`, &groupCount},
		{`SELECT count(*) FROM proxy_groups WHERE public_port BETWEEN 20000 AND 20099 AND mixed_port=public_port+10000`, &validGroupCount},
		{`SELECT count(DISTINCT public_port) FROM proxy_groups`, &distinctPortCount},
		{`SELECT count(*) FROM egress_protocol_modes AS mode WHERE mode.egress_id='agw-main' OR EXISTS(SELECT 1 FROM proxy_groups AS groups WHERE groups.id=mode.egress_id)`, &matchedModeCount},
		{`SELECT count(*) FROM main_egress WHERE resource_name='agw-main' AND enabled=1 AND public_port=8443 AND mixed_port=31000`, &mainCount},
	} {
		if err := database.QueryRowContext(ctx, gate.query).Scan(gate.value); err != nil {
			return "", errors.New("Gateway logical inventory is unavailable")
		}
	}
	if groupCount < 1 || modeCount != groupCount+1 || readyModeCount != modeCount || validGroupCount != groupCount || distinctPortCount != groupCount || matchedModeCount != modeCount || mainCount != 1 {
		return "", errors.New("Gateway logical inventory is not ready")
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
	if p.xuiDatabasePath == "" {
		return "", errors.New("x-ui database path is required")
	}
	xui, err := openReadOnlyDatabase(p.xuiDatabasePath)
	if err != nil {
		return "", err
	}
	defer xui.Close()
	if err := xui.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&quickCheck); err != nil || quickCheck != "ok" {
		return "", errors.New("x-ui database quick check failed")
	}
	// Hash configuration only, excluding live traffic/last-seen counters. The
	// entire ordered inbound set covers managed and unmanaged resources alike.
	for _, query := range []string{
		`SELECT id,user_id,remark,sub_sort_index,enable,expiry_time,listen,port,protocol,settings,stream_settings,tag,sniffing,node_id,share_addr_strategy,share_addr,origin_node_guid,disable_flow FROM inbounds ORDER BY id`,
		`SELECT key,value FROM settings ORDER BY key`,
		`SELECT client_id,inbound_id,flow_override,alias_override FROM client_inbounds ORDER BY client_id,inbound_id`,
		`SELECT count(*) FROM client_inbounds WHERE alias_override <> ''`,
	} {
		if err := hashRows(ctx, xui, hash, query); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashRows(ctx context.Context, db *sql.DB, output io.Writer, query string) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		if err := encoder.Encode(values); err != nil {
			return err
		}
	}
	return rows.Err()
}

func databaseBusyCheck(databasePath string) func(context.Context) error {
	return func(ctx context.Context) error {
		database, err := openReadOnlyDatabase(databasePath)
		if err != nil {
			return err
		}
		defer database.Close()
		queries := []struct {
			statement string
			args      []any
		}{
			{
				statement: `SELECT EXISTS(
					SELECT 1 FROM egress_protocol_modes WHERE state <> 'ready'
					UNION ALL
					SELECT 1 FROM egress_operations AS operation
					WHERE operation.completed_at = 0
					AND (
						operation.started_at >= ?
						OR NOT EXISTS(
							SELECT 1 FROM egress_protocol_modes AS mode
							WHERE mode.egress_id = operation.egress_id
							AND mode.state = 'ready'
							AND mode.last_operation_id <> operation.operation_id
						)
					))`,
				args: []any{time.Now().UTC().Add(-15 * time.Minute).UnixMilli()},
			},
			{statement: `SELECT EXISTS(SELECT 1 FROM proxy_operations WHERE result = 'running')`},
			{statement: `SELECT EXISTS(SELECT 1 FROM mixed_source_policy WHERE apply_status IN ('pending', 'applying'))`},
		}
		for _, query := range queries {
			var busy bool
			if err := database.QueryRowContext(ctx, query.statement, query.args...).Scan(&busy); err != nil {
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
