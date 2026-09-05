package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestProductionReadinessPollsUntilHealthyOrDeadline(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(fmt.Sprint(permanent), func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if permanent || hits < 3 {
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			probe := &productionProbe{healthURL: server.URL}
			waiter, ok := any(probe).(interface{ WaitReady(context.Context) error })
			if !ok {
				t.Fatal("production probe has no bounded readiness poll")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
			defer cancel()
			err := waiter.WaitReady(ctx)
			if (err != nil) != permanent || hits < 3 {
				t.Fatalf("permanent=%v hits=%d err=%v", permanent, hits, err)
			}
		})
	}
}

func invariantProbeFixture(t *testing.T) (*productionProbe, *sql.DB, *sql.DB) {
	dir := t.TempDir()
	gp, xp := filepath.Join(dir, "gateway.db"), filepath.Join(dir, "xui.db")
	g, _ := sql.Open("sqlite", gp)
	x, _ := sql.Open("sqlite", xp)
	t.Cleanup(func() { g.Close(); x.Close() })
	for _, q := range []string{
		`CREATE TABLE egress_protocol_modes(egress_id TEXT, active_mode TEXT, desired_mode TEXT,state TEXT)`,
		`CREATE TABLE proxy_groups(id TEXT,status TEXT,aimili_slot INTEGER,public_port INTEGER,mixed_port INTEGER,config_fingerprint TEXT)`,
		`CREATE TABLE main_egress(id INTEGER,resource_name TEXT,enabled INTEGER,public_port INTEGER,mixed_port INTEGER)`,
		`CREATE TABLE mixed_source_policy(id INTEGER,enabled INTEGER,apply_status TEXT)`,
		`INSERT INTO main_egress VALUES(1,'agw-main',1,8443,31000)`,
		`INSERT INTO mixed_source_policy VALUES(1,0,'applied')`,
		`INSERT INTO egress_protocol_modes VALUES('agw-main','vless_tcp_reality_vision','vless_tcp_reality_vision','ready')`,
	} {
		mustExec(t, g, q)
	}
	for i := 0; i < 3; i++ {
		mustExec(t, g, `INSERT INTO proxy_groups VALUES(?, 'ready', ?, ?, ?, 'stable')`, fmt.Sprintf("agw-slot%d", i+1), i, 20000+i, 30000+i)
		mustExec(t, g, `INSERT INTO egress_protocol_modes VALUES(?, 'vless_tcp_reality_vision','vless_tcp_reality_vision','ready')`, fmt.Sprintf("agw-slot%d", i+1))
	}
	for _, q := range []string{`CREATE TABLE inbounds(id INTEGER,tag TEXT,remark TEXT,protocol TEXT,port INTEGER,enable INTEGER,settings TEXT,stream_settings TEXT,sniffing TEXT)`, `CREATE TABLE settings(key TEXT,value TEXT)`, `CREATE TABLE client_inbounds(client_id INTEGER,inbound_id INTEGER,alias_override TEXT)`, `INSERT INTO inbounds VALUES(1,'agw-main','main','vless',8443,1,'{}','{}','{}'),(2,'unmanaged','other','vless',443,1,'{}','{}','{}')`, `INSERT INTO settings VALUES('xrayTemplateConfig','{}')`, `INSERT INTO client_inbounds VALUES(1,1,'主连接_日本')`} {
		mustExec(t, x, q)
	}
	return &productionProbe{databasePath: gp, xuiDatabasePath: xp, serviceState: func(context.Context, string) ([]byte, error) { return []byte("ActiveState=active\nMainPID=123\n"), nil }, countProcesses: func() ([2]int, error) { return [2]int{4, 1}, nil }}, g, x
}

func TestProbeRejectsUnsafeBaselineAndDetectsResourceDrift(t *testing.T) {
	for _, scenario := range []string{"inactive", "pid_zero", "process_zero", "protocol_not_ready", "slots_missing", "managed_drift", "unmanaged_drift", "alias_drift"} {
		t.Run(scenario, func(t *testing.T) {
			p, g, x := invariantProbeFixture(t)
			before, err := p.Capture(context.Background())
			if err != nil {
				t.Fatal("valid fixture rejected", err)
			}
			switch scenario {
			case "inactive":
				p.serviceState = func(context.Context, string) ([]byte, error) {
					return []byte("ActiveState=inactive\nMainPID=123\n"), nil
				}
			case "pid_zero":
				p.serviceState = func(context.Context, string) ([]byte, error) { return []byte("ActiveState=active\nMainPID=0\n"), nil }
			case "process_zero":
				p.countProcesses = func() ([2]int, error) { return [2]int{}, nil }
			case "protocol_not_ready":
				mustExec(t, g, `UPDATE egress_protocol_modes SET state='switching' WHERE egress_id='agw-main'`)
			case "slots_missing":
				mustExec(t, g, `DELETE FROM proxy_groups`)
			case "managed_drift":
				mustExec(t, x, `UPDATE inbounds SET settings='changed' WHERE tag='agw-main'`)
			case "unmanaged_drift":
				mustExec(t, x, `UPDATE inbounds SET settings='changed' WHERE tag='unmanaged'`)
			case "alias_drift":
				mustExec(t, x, `DELETE FROM client_inbounds`)
			}
			after, err := p.Capture(context.Background())
			if scenario == "managed_drift" || scenario == "unmanaged_drift" || scenario == "alias_drift" {
				if err == nil && before.Fingerprint == after.Fingerprint {
					t.Fatal("resource drift invisible")
				}
			} else if err == nil {
				t.Fatal("unsafe baseline accepted")
			}
		})
	}
}
