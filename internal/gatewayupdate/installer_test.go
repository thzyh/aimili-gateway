package gatewayupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/buildinfo"
	"github.com/thzyh/aimili-gateway/internal/releaseverify"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func TestDryRunPerformsNoProductionWrites(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	before := fileDigests(t, config.BinaryPath, config.ConfigPath, config.DatabasePath)
	result, err := DryRun(context.Background(), config)
	if err != nil || result.State != updatetxn.StateSuccess {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	after := fileDigests(t, config.BinaryPath, config.ConfigPath, config.DatabasePath)
	if before != after || len(runner.calls) != 0 {
		t.Fatalf("dry run changed production: before=%v after=%v calls=%v", before, after, runner.calls)
	}
}

func TestDisabledInstallNeverExecutesShadow(t *testing.T) {
	config, _ := gatewayFixture(t, "control-plane-only")
	config.AllowInstall = false
	called := false
	config.ShadowCheck = func(context.Context, []byte, releaseverify.GatewayManifest) error {
		called = true
		return nil
	}
	_, err := Install(context.Background(), config)
	if ErrorCode(err) != "install_disabled" || called {
		t.Fatalf("disabled install: shadow=%v err=%v", called, err)
	}
}

func TestShadowConsumesVerifiedContentAfterStagingReplacement(t *testing.T) {
	config, _ := gatewayFixture(t, "control-plane-only")
	config.ShadowCheck = func(_ context.Context, candidate []byte, _ releaseverify.GatewayManifest) error {
		if err := os.WriteFile(filepath.Join(config.StagingDir, "aimili-gateway"), []byte("unverified replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := string(candidate); got != "new gateway" {
			t.Fatalf("shadow consumed unverified bytes: %q", got)
		}
		return nil
	}
	if _, err := DryRun(context.Background(), config); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsBusyProtocolTransactionBeforeStop(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	config.BusyCheck = func(context.Context) error { return ErrOperationBusy }
	if _, err := Install(context.Background(), config); ErrorCode(err) != "operation_busy" {
		t.Fatalf("busy error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("service calls before busy rejection = %v", runner.calls)
	}
}

func TestInstallRejectsUnreadableCurrentVersion(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	config.ReadVersion = nil
	_, err := Install(context.Background(), config)
	if ErrorCode(err) != "current_version_unavailable" || len(runner.calls) != 0 {
		t.Fatalf("unreadable current build identity must fail closed: err=%v calls=%v", err, runner.calls)
	}
}

func TestInstallOnlyAllowsNewerCanonicalGatewayVersion(t *testing.T) {
	for _, tc := range []struct{ current, want string }{
		{"v1.2.3", "version_not_newer"}, {"v1.3.0", "version_not_newer"}, {"v1.2.2", ""}, {"dev", "current_version_unavailable"}, {"v01.2.0", "current_version_unavailable"}, {"v+1.2.0", "current_version_unavailable"},
	} {
		t.Run(tc.current, func(t *testing.T) {
			config, runner := gatewayFixture(t, "control-plane-only")
			config.ReadVersion = func(context.Context, string, string) (buildinfo.Info, error) {
				return buildinfo.Info{Version: tc.current, Commit: "old1234", Platform: "linux-amd64", APIVersion: "v1"}, nil
			}
			_, err := Install(context.Background(), config)
			if ErrorCode(err) != tc.want {
				t.Fatalf("current %s: err=%v want=%s", tc.current, err, tc.want)
			}
			if tc.want != "" && len(runner.calls) != 0 {
				t.Fatal("rejected version mutated service")
			}
		})
	}
}

func TestNonemptyUICompatibilityFailsClosed(t *testing.T) {
	config, _ := gatewayFixture(t, "control-plane-only")
	var manifest releaseverify.GatewayManifest
	if err := json.Unmarshal(mustRead(t, filepath.Join(config.StagingDir, "manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.UICompatibility = &releaseverify.UICompatibility{Min: strings.Repeat("a", 64), Max: strings.Repeat("f", 64)}
	body, _ := json.Marshal(manifest)
	if _, err := releaseverify.ParseGateway(body, "v1", "linux-amd64"); releaseverify.ErrorCode(err) != "unsupported_ui_compatibility" {
		t.Fatalf("UI compatibility silently ignored: %v", err)
	}
}

func TestInstallRejectsRuntimeImpactBeforeStop(t *testing.T) {
	config, runner := gatewayFixture(t, "runtime")
	if _, err := Install(context.Background(), config); ErrorCode(err) != "manual_staged_deploy_required" {
		t.Fatalf("runtime impact error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("service calls before impact rejection = %v", runner.calls)
	}
}

func TestInstallRejectsInsufficientSpaceBeforeStop(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	config.AvailableBytes = func(string) (uint64, error) { return 1, nil }
	if _, err := Install(context.Background(), config); ErrorCode(err) != "disk_full" {
		t.Fatalf("disk error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("service calls before disk rejection = %v", runner.calls)
	}
	if _, err := os.Stat(config.PreviousPath); !os.IsNotExist(err) {
		t.Fatalf("previous was written before disk rejection: %v", err)
	}
}

func TestInstallRejectsUntrustedStagingMetadata(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("new gateway"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(config.StagingDir, "aimili-gateway")
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, binary); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Install(context.Background(), config); ErrorCode(err) != "untrusted_staging" {
		t.Fatalf("staging error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("service calls before staging rejection = %v", runner.calls)
	}
}

func TestInstallStartFailureRestoresPrevious(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	runner.startErrors = []error{errors.New("new start failed"), nil}
	result, err := Install(context.Background(), config)
	if err != nil || result.State != updatetxn.StateRolledBack || result.ErrorCode != "start_failed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := string(mustRead(t, config.BinaryPath)); got != "old gateway" {
		t.Fatalf("active binary = %q", got)
	}
}

func TestInstallHealthFailureRestoresPrevious(t *testing.T) {
	config, _ := gatewayFixture(t, "control-plane-only")
	config.Probe = &fakeProbe{verifyError: errors.New("health failed")}
	result, err := Install(context.Background(), config)
	if err != nil || result.State != updatetxn.StateRolledBack || result.ErrorCode != "health_check_failed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := string(mustRead(t, config.BinaryPath)); got != "old gateway" {
		t.Fatalf("active binary = %q", got)
	}
}

func TestRollbackFailurePreservesBothBinariesAndRepairState(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	runner.startErrors = []error{errors.New("new start failed"), errors.New("old start failed")}
	result, err := Install(context.Background(), config)
	if ErrorCode(err) != "repair_required" || result.State != updatetxn.StateRepairRequired {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, statErr := os.Stat(config.PreviousPath); statErr != nil {
		t.Fatalf("previous missing: %v", statErr)
	}
	if matches, _ := filepath.Glob(config.BinaryPath + ".failed-*"); len(matches) != 1 {
		t.Fatalf("failed binary assets = %v", matches)
	}
}

func TestRollbackBusyHasZeroServiceMutation(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	if err := os.WriteFile(config.PreviousPath, []byte("previous gateway"), 0700); err != nil {
		t.Fatal(err)
	}
	config.BusyCheck = func(context.Context) error { return ErrOperationBusy }
	_, err := Rollback(context.Background(), config)
	if ErrorCode(err) != "operation_busy" || len(runner.calls) != 0 {
		t.Fatalf("busy rollback mutated service: err=%v calls=%v", err, runner.calls)
	}
}

type cancelingRunner struct {
	cancel context.CancelFunc
	starts int
}

func (r *cancelingRunner) Stop(ctx context.Context, _ string) error { return ctx.Err() }
func (r *cancelingRunner) Start(ctx context.Context, _ string) error {
	r.starts++
	if r.starts == 1 {
		r.cancel()
		return context.DeadlineExceeded
	}
	return ctx.Err()
}
func (r *cancelingRunner) IsActive(ctx context.Context, _ string) (bool, error) {
	return ctx.Err() == nil, ctx.Err()
}

func TestAutomaticRecoveryUsesFreshBoundedContext(t *testing.T) {
	config, _ := gatewayFixture(t, "control-plane-only")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config.Runner = &cancelingRunner{cancel: cancel}
	result, err := Install(ctx, config)
	if err != nil || result.State != updatetxn.StateRolledBack {
		t.Fatalf("expired install ctx prevented recovery: result=%+v err=%v", result, err)
	}
	if string(mustRead(t, config.BinaryPath)) != "old gateway" {
		t.Fatal("old binary not restored")
	}
}

type delayedProbe struct{ ready bool }

func (p *delayedProbe) Capture(context.Context) (Snapshot, error) {
	return Snapshot{Fingerprint: "baseline"}, nil
}
func (p *delayedProbe) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
		p.ready = true
		return nil
	}
}
func (p *delayedProbe) Verify(context.Context, Snapshot) error {
	if !p.ready {
		return errors.New("not ready yet")
	}
	return nil
}

func TestInstallerWaitsForReadinessBeforeVerification(t *testing.T) {
	config, _ := gatewayFixture(t, "control-plane-only")
	config.Probe = &delayedProbe{}
	result, err := Install(context.Background(), config)
	if err != nil || result.State != updatetxn.StateSuccess {
		t.Fatalf("slow start did not get readiness wait: %+v %v", result, err)
	}
}

func TestSuccessfulInstallKeepsExactlyOnePreviousAndOnlyRestartsGateway(t *testing.T) {
	config, runner := gatewayFixture(t, "control-plane-only")
	if err := os.WriteFile(config.PreviousPath, []byte("older gateway"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Install(context.Background(), config)
	if err != nil || result.State != updatetxn.StateSuccess {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if string(mustRead(t, config.BinaryPath)) != "new gateway" || string(mustRead(t, config.PreviousPath)) != "old gateway" {
		t.Fatalf("current=%q previous=%q", mustRead(t, config.BinaryPath), mustRead(t, config.PreviousPath))
	}
	for _, call := range runner.calls {
		if !strings.HasSuffix(call, ":aimili-gateway.service") {
			t.Fatalf("data-plane service call = %q", call)
		}
	}
}

type fakeRunner struct {
	calls       []string
	startErrors []error
}

func (r *fakeRunner) Stop(_ context.Context, service string) error {
	r.calls = append(r.calls, "stop:"+service)
	return nil
}

func (r *fakeRunner) Start(_ context.Context, service string) error {
	r.calls = append(r.calls, "start:"+service)
	if len(r.startErrors) == 0 {
		return nil
	}
	err := r.startErrors[0]
	r.startErrors = r.startErrors[1:]
	return err
}

func (r *fakeRunner) IsActive(_ context.Context, service string) (bool, error) {
	r.calls = append(r.calls, "active:"+service)
	return true, nil
}

type fakeProbe struct{ verifyError error }

func (p *fakeProbe) Capture(context.Context) (Snapshot, error) {
	return Snapshot{Fingerprint: "baseline"}, nil
}

func (p *fakeProbe) Verify(context.Context, Snapshot) error {
	err := p.verifyError
	p.verifyError = nil
	return err
}

func gatewayFixture(t *testing.T, impactClass string) (Config, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("new gateway")
	digest := sha256.Sum256(newBinary)
	manifest, err := json.Marshal(releaseverify.GatewayManifest{
		SchemaVersion: 1, Kind: "gateway", Version: "v1.2.3", Commit: "abc1234",
		BuiltAt: "2026-09-05T00:00:00Z", Platform: "linux-amd64", APIVersion: "v1", ImpactClass: impactClass,
		Binary:            releaseverify.File{Path: "aimili-gateway", SHA256: fmt.Sprintf("%x", digest), Bytes: int64(len(newBinary))},
		MinDatabaseSchema: 11, MaxDatabaseSchema: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"manifest.json":     manifest,
		"manifest.sig":      ed25519.Sign(privateKey, manifest),
		"aimili-gateway":    newBinary,
		"download.complete": []byte(`{"state":"validating"}`),
	} {
		if err := os.WriteFile(filepath.Join(staging, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	publicKeyFile := filepath.Join(root, "release.pub")
	if err := os.WriteFile(publicKeyFile, []byte(hex.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(root, "bin", "aimili-gateway")
	if err := os.Mkdir(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("old gateway"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "gateway.json")
	databasePath := filepath.Join(root, "gateway.db")
	if err := os.WriteFile(configPath, []byte("config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databasePath, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	return Config{
		StateDir: filepath.Join(root, "state"), Request: updatetxn.Request{RunID: strings.Repeat("a", 64), Kind: updatetxn.KindGateway, Version: "v1.2.3", Action: updatetxn.ActionApply},
		RunID: strings.Repeat("a", 64), StagingDir: staging, BinaryPath: binaryPath,
		PreviousPath: binaryPath + ".previous", ConfigPath: configPath, DatabasePath: databasePath,
		PublicKeyFile: publicKeyFile, APIVersion: "v1", Platform: "linux-amd64", AllowInstall: true,
		Runner: runner, Probe: &fakeProbe{}, BusyCheck: func(context.Context) error { return nil },
		ShadowCheck: func(context.Context, []byte, releaseverify.GatewayManifest) error { return nil },
		ReadVersion: func(context.Context, string, string) (buildinfo.Info, error) {
			return buildinfo.Info{Version: "v1.2.2", Commit: "old1234", APIVersion: "v1", Platform: "linux-amd64"}, nil
		},
		Now: func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) },
	}, runner
}

func TestReadPublicKeyAcceptsLowercaseHexKeygenFormat(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "release.pub")
	if err := os.WriteFile(filename, []byte(hex.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := readPublicKey(filename)
	if err != nil || !bytes.Equal(actual, publicKey) {
		t.Fatalf("key=%x err=%v", actual, err)
	}
}

func TestReadPublicKeyRejectsNonCanonicalKeygenFormat(t *testing.T) {
	publicKey := ed25519.PublicKey(bytes.Repeat([]byte{0xab}, ed25519.PublicKeySize))
	for name, body := range map[string]string{
		"uppercase hex":  strings.ToUpper(hex.EncodeToString(publicKey)),
		"mixed case hex": strings.Replace(hex.EncodeToString(publicKey), "ab", "aB", 1),
		"base64":         base64.StdEncoding.EncodeToString(publicKey),
	} {
		t.Run(name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "release.pub")
			if err := os.WriteFile(filename, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readPublicKey(filename); err == nil {
				t.Fatal("non-canonical public key accepted")
			}
		})
	}
}

func fileDigests(t *testing.T, names ...string) [3][32]byte {
	t.Helper()
	var result [3][32]byte
	for index, name := range names {
		result[index] = sha256.Sum256(mustRead(t, name))
	}
	return result
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
