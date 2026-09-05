package gatewayupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/buildinfo"
	"github.com/thzyh/aimili-gateway/internal/releaseverify"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

var ErrOperationBusy = errors.New("another managed transaction is active")

const gatewayService = "aimili-gateway.service"

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.code + ": " + e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return releaseverify.ErrorCode(err)
}

type Runner interface {
	Stop(context.Context, string) error
	Start(context.Context, string) error
	IsActive(context.Context, string) (bool, error)
}

type Probe interface {
	Capture(context.Context) (Snapshot, error)
	Verify(context.Context, Snapshot) error
}

type Snapshot struct {
	Fingerprint string `json:"fingerprint"`
}

type Config struct {
	RunID             string
	StagingDir        string
	BinaryPath        string
	PreviousPath      string
	ConfigPath        string
	DatabasePath      string
	PublicKeyFile     string
	APIVersion        string
	Platform          string
	AllowInstall      bool
	TrustedStagingUID *uint32
	Runner            Runner
	Probe             Probe
	BusyCheck         func(context.Context) error
	ShadowCheck       func(context.Context, string, releaseverify.GatewayManifest) error
	AvailableBytes    func(string) (uint64, error)
	Now               func() time.Time
}

type Result struct {
	RunID      string          `json:"runId"`
	Version    string          `json:"version,omitempty"`
	State      updatetxn.State `json:"state"`
	ErrorCode  string          `json:"errorCode,omitempty"`
	FinishedAt time.Time       `json:"finishedAt"`
}

type verifiedRelease struct {
	manifest releaseverify.GatewayManifest
	binary   []byte
}

func DryRun(ctx context.Context, config Config) (Result, error) {
	release, err := preflight(ctx, config)
	if err != nil {
		return failedResult(config, "", updatetxn.StateFailed, ErrorCode(err)), err
	}
	return finishedResult(config, release.manifest.Version, updatetxn.StateSuccess, ""), nil
}

func Install(ctx context.Context, config Config) (Result, error) {
	release, err := preflight(ctx, config)
	if err != nil {
		return failedResult(config, "", updatetxn.StateFailed, ErrorCode(err)), err
	}
	if !config.AllowInstall {
		err := &codedError{code: "install_disabled", err: errors.New("Gateway installation is disabled")}
		return failedResult(config, release.manifest.Version, updatetxn.StateFailed, ErrorCode(err)), err
	}
	if config.Runner == nil || config.Probe == nil {
		err := &codedError{code: "invalid_config", err: errors.New("runner and probe are required")}
		return failedResult(config, release.manifest.Version, updatetxn.StateFailed, ErrorCode(err)), err
	}
	baseline, err := config.Probe.Capture(ctx)
	if err != nil {
		err = &codedError{code: "preflight_probe_failed", err: err}
		return failedResult(config, release.manifest.Version, updatetxn.StateFailed, ErrorCode(err)), err
	}
	currentInfo, err := os.Stat(config.BinaryPath)
	if err != nil || !currentInfo.Mode().IsRegular() {
		err = &codedError{code: "current_binary_invalid", err: errors.New("current Gateway binary is unavailable")}
		return failedResult(config, release.manifest.Version, updatetxn.StateFailed, ErrorCode(err)), err
	}
	if err := copyAndReplace(config.BinaryPath, config.PreviousPath, currentInfo.Mode().Perm()); err != nil {
		err = &codedError{code: "previous_failed", err: err}
		return failedResult(config, release.manifest.Version, updatetxn.StateFailed, ErrorCode(err)), err
	}
	candidatePath := config.BinaryPath + ".candidate-" + config.RunID
	if err := writeCandidate(candidatePath, release.binary, currentInfo.Mode().Perm()); err != nil {
		err = &codedError{code: "candidate_failed", err: err}
		return failedResult(config, release.manifest.Version, updatetxn.StateFailed, ErrorCode(err)), err
	}
	if err := config.Runner.Stop(ctx, gatewayService); err != nil {
		_ = os.Remove(candidatePath)
		err = &codedError{code: "stop_failed", err: err}
		return failedResult(config, release.manifest.Version, updatetxn.StateFailed, ErrorCode(err)), err
	}
	if err := replacePath(candidatePath, config.BinaryPath); err != nil {
		return rollbackAfterFailure(ctx, config, baseline, release.manifest.Version, "switch_failed", err)
	}
	if err := config.Runner.Start(ctx, gatewayService); err != nil {
		return rollbackAfterFailure(ctx, config, baseline, release.manifest.Version, "start_failed", err)
	}
	active, err := config.Runner.IsActive(ctx, gatewayService)
	if err != nil || !active {
		if err == nil {
			err = errors.New("Gateway service is inactive")
		}
		return rollbackAfterFailure(ctx, config, baseline, release.manifest.Version, "start_failed", err)
	}
	if err := config.Probe.Verify(ctx, baseline); err != nil {
		return rollbackAfterFailure(ctx, config, baseline, release.manifest.Version, "health_check_failed", err)
	}
	_ = os.RemoveAll(config.StagingDir)
	return finishedResult(config, release.manifest.Version, updatetxn.StateSuccess, ""), nil
}

func Rollback(ctx context.Context, config Config) (Result, error) {
	if config.Runner == nil || config.Probe == nil {
		err := &codedError{code: "invalid_config", err: errors.New("runner and probe are required")}
		return failedResult(config, "", updatetxn.StateFailed, ErrorCode(err)), err
	}
	baseline, err := config.Probe.Capture(ctx)
	if err != nil {
		return failedResult(config, "", updatetxn.StateFailed, "preflight_probe_failed"), err
	}
	previous, err := os.ReadFile(config.PreviousPath)
	if err != nil {
		err = &codedError{code: "previous_missing", err: err}
		return failedResult(config, "", updatetxn.StateFailed, ErrorCode(err)), err
	}
	current, err := os.ReadFile(config.BinaryPath)
	if err != nil {
		return failedResult(config, "", updatetxn.StateFailed, "current_binary_invalid"), err
	}
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(config.BinaryPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := config.Runner.Stop(ctx, gatewayService); err != nil {
		return failedResult(config, "", updatetxn.StateFailed, "stop_failed"), err
	}
	if err := writeAndReplace(config.BinaryPath, previous, mode); err != nil {
		return failedResult(config, "", updatetxn.StateRepairRequired, "repair_required"), &codedError{code: "repair_required", err: err}
	}
	if err := writeAndReplace(config.PreviousPath, current, mode); err != nil {
		return failedResult(config, "", updatetxn.StateRepairRequired, "repair_required"), &codedError{code: "repair_required", err: err}
	}
	if err := config.Runner.Start(ctx, gatewayService); err != nil {
		return failedResult(config, "", updatetxn.StateRepairRequired, "repair_required"), &codedError{code: "repair_required", err: err}
	}
	if err := config.Probe.Verify(ctx, baseline); err != nil {
		return failedResult(config, "", updatetxn.StateRepairRequired, "repair_required"), &codedError{code: "repair_required", err: err}
	}
	return finishedResult(config, "", updatetxn.StateRolledBack, ""), nil
}

func preflight(ctx context.Context, config Config) (verifiedRelease, error) {
	if config.RunID == "" || config.StagingDir == "" || config.BinaryPath == "" || config.PreviousPath == "" || config.PublicKeyFile == "" {
		return verifiedRelease{}, &codedError{code: "invalid_config", err: errors.New("required update paths are missing")}
	}
	if config.BusyCheck != nil {
		if err := config.BusyCheck(ctx); err != nil {
			return verifiedRelease{}, &codedError{code: "operation_busy", err: err}
		}
	}
	assets, err := readStaging(config.StagingDir, config.TrustedStagingUID)
	if err != nil {
		return verifiedRelease{}, &codedError{code: "untrusted_staging", err: err}
	}
	publicKey, err := readPublicKey(config.PublicKeyFile)
	if err != nil {
		return verifiedRelease{}, &codedError{code: "public_key_invalid", err: err}
	}
	manifest, err := releaseverify.VerifyGateway(assets["manifest.json"], assets["manifest.sig"], assets["aimili-gateway"], publicKey, config.APIVersion, config.Platform)
	if err != nil {
		return verifiedRelease{}, err
	}
	availableBytes := config.AvailableBytes
	if availableBytes == nil {
		availableBytes = gatewayAvailableBytes
	}
	free, err := availableBytes(filepath.Dir(config.BinaryPath))
	if err != nil {
		return verifiedRelease{}, &codedError{code: "disk_check_failed", err: err}
	}
	required := uint64(len(assets["aimili-gateway"]))*3 + 128<<20
	if free < required {
		return verifiedRelease{}, &codedError{code: "disk_full", err: errors.New("insufficient installation space")}
	}
	shadowCheck := config.ShadowCheck
	if shadowCheck == nil {
		shadowCheck = func(ctx context.Context, candidate string, manifest releaseverify.GatewayManifest) error {
			return defaultShadowCheck(ctx, candidate, config.ConfigPath, manifest)
		}
	}
	if err := shadowCheck(ctx, filepath.Join(config.StagingDir, "aimili-gateway"), manifest); err != nil {
		return verifiedRelease{}, &codedError{code: "shadow_check_failed", err: err}
	}
	return verifiedRelease{manifest: manifest, binary: assets["aimili-gateway"]}, nil
}

func rollbackAfterFailure(ctx context.Context, config Config, baseline Snapshot, version, failureCode string, cause error) (Result, error) {
	failedPath := config.BinaryPath + ".failed-" + config.RunID
	if current, err := os.ReadFile(config.BinaryPath); err == nil {
		_ = writeAndReplace(failedPath, current, 0o700)
	}
	previous, previousErr := os.ReadFile(config.PreviousPath)
	if previousErr != nil {
		err := &codedError{code: "repair_required", err: previousErr}
		return failedResult(config, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	_ = config.Runner.Stop(ctx, gatewayService)
	if err := writeAndReplace(config.BinaryPath, previous, 0o755); err != nil {
		err = &codedError{code: "repair_required", err: err}
		return failedResult(config, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	if err := config.Runner.Start(ctx, gatewayService); err != nil {
		err = &codedError{code: "repair_required", err: err}
		return failedResult(config, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	active, err := config.Runner.IsActive(ctx, gatewayService)
	if err != nil || !active {
		if err == nil {
			err = errors.New("restored Gateway service is inactive")
		}
		err = &codedError{code: "repair_required", err: err}
		return failedResult(config, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	if err := config.Probe.Verify(ctx, baseline); err != nil {
		err = &codedError{code: "repair_required", err: err}
		return failedResult(config, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	_ = os.Remove(failedPath)
	_ = os.RemoveAll(config.StagingDir)
	_ = cause
	return finishedResult(config, version, updatetxn.StateRolledBack, failureCode), nil
}

func defaultShadowCheck(ctx context.Context, stagingBinary, configPath string, manifest releaseverify.GatewayManifest) error {
	body, err := os.ReadFile(stagingBinary)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp("", "aimili-gateway-shadow-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o700); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	versionOutput, err := runCandidate(ctx, name, configPath, "version", "--json")
	if err != nil {
		return err
	}
	var info buildinfo.Info
	decoder := json.NewDecoder(bytes.NewReader(versionOutput))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&info); err != nil || info.Version != manifest.Version || info.Commit != manifest.Commit || info.APIVersion != manifest.APIVersion || info.Platform != manifest.Platform {
		return errors.New("candidate version identity mismatch")
	}
	if _, err := runCandidate(ctx, name, configPath, "config", "validate"); err != nil {
		return err
	}
	_, err = runCandidate(ctx, name, configPath, "database", "check-compatible", "--min", strconv.Itoa(manifest.MinDatabaseSchema), "--max", strconv.Itoa(manifest.MaxDatabaseSchema))
	return err
}

func runCandidate(ctx context.Context, binary, configPath string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, binary, args...)
	command.Env = []string{"GATEWAY_CONFIG=" + configPath}
	return command.Output()
}

func readStaging(directory string, trustedUID *uint32) (map[string][]byte, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("staging directory is invalid")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	want := []string{"aimili-gateway", "download.complete", "manifest.json", "manifest.sig"}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		return nil, errors.New("staging file set is invalid")
	}
	result := make(map[string][]byte, len(want))
	for _, name := range want {
		limit := int64(512 << 20)
		if name == "manifest.json" || name == "download.complete" {
			limit = 1 << 20
		} else if name == "manifest.sig" {
			limit = ed25519.SignatureSize
		}
		body, err := readTrustedFile(filepath.Join(directory, name), trustedUID, limit)
		if err != nil {
			return nil, err
		}
		result[name] = body
	}
	return result, nil
}

func readTrustedFile(name string, trustedUID *uint32, limit int64) ([]byte, error) {
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > limit || gatewayInsecurePermissions(before) {
		return nil, errors.New("staging file metadata is invalid")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("staging file changed while opening")
	}
	uid, links, ownerSupported, err := gatewayFileMetadata(file, after)
	if err != nil || links != 1 || (trustedUID != nil && (!ownerSupported || uid != *trustedUID)) {
		return nil, errors.New("staging file ownership is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, errors.New("staging file is too large")
	}
	return body, nil
}

func readPublicKey(filename string) (ed25519.PublicKey, error) {
	body, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key is invalid")
	}
	return ed25519.PublicKey(raw), nil
}

func copyAndReplace(source, destination string, mode os.FileMode) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeAndReplace(destination, body, mode)
}

func writeAndReplace(destination string, body []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".gateway-update-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replacePath(name, destination)
}

func writeCandidate(destination string, body []byte, mode os.FileMode) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func finishedResult(config Config, version string, state updatetxn.State, code string) Result {
	return Result{RunID: config.RunID, Version: version, State: state, ErrorCode: code, FinishedAt: now(config)}
}

func failedResult(config Config, version string, state updatetxn.State, code string) Result {
	return finishedResult(config, version, state, code)
}

func now(config Config) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}
