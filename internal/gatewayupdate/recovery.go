package gatewayupdate

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

// The entry point holds the shared root execution lease throughout this operation.
func switchRelease(ctx context.Context, cfg Config, baseline Snapshot, version string, binary []byte) (result Result, err error) {
	current, err := readTrustedFile(cfg.BinaryPath, nil, 512<<20)
	if err != nil {
		return failedResult(cfg, version, updatetxn.StateFailed, "current_binary_invalid"), err
	}
	j := updatetxn.Journal{Request: cfg.Request, OldDigest: updatetxn.Digest(current), NewDigest: updatetxn.Digest(binary), Baseline: baseline.Fingerprint, Phase: "prepared"}
	if err := updatetxn.WriteJournal(cfg.StateDir, j); err != nil {
		return failedResult(cfg, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	defer func() {
		if result.State.Terminal() {
			j.Phase, j.State, j.ErrorCode = "terminal", result.State, result.ErrorCode
			if writeErr := updatetxn.WriteJournal(cfg.StateDir, j); writeErr != nil {
				result = failedResult(cfg, version, updatetxn.StateRepairRequired, "repair_required")
				err = &codedError{code: "repair_required", err: writeErr}
			}
		}
	}()
	if err := writeAndReplace(cfg.PreviousPath, current, 0755); err != nil {
		return failedResult(cfg, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	candidate := cfg.BinaryPath + ".candidate-" + cfg.RunID
	if err := writeCandidate(candidate, binary, 0755); err != nil {
		return failedResult(cfg, version, updatetxn.StateRepairRequired, "repair_required"), err
	}
	if err := injectFault(cfg, "before_stop"); err != nil {
		return Result{}, err
	}
	if err := cfg.Runner.Stop(ctx, gatewayService); err != nil {
		return rollbackAfterFailure(ctx, cfg, baseline, version, "stop_failed", err)
	}
	if err := checkpoint(cfg, &j, "stopped", "after_stop"); err != nil {
		return Result{}, err
	}
	if err := replacePath(candidate, cfg.BinaryPath); err != nil {
		return rollbackAfterFailure(ctx, cfg, baseline, version, "switch_failed", err)
	}
	if err := checkpoint(cfg, &j, "switched", "after_replace"); err != nil {
		return Result{}, err
	}
	if err := cfg.Runner.Start(ctx, gatewayService); err != nil {
		return rollbackAfterFailure(ctx, cfg, baseline, version, "start_failed", err)
	}
	if err := checkpoint(cfg, &j, "started", "after_start"); err != nil {
		return Result{}, err
	}
	if err := verifyRunning(ctx, cfg, baseline); err != nil {
		return rollbackAfterFailure(ctx, cfg, baseline, version, "health_check_failed", err)
	}
	if err := checkpoint(cfg, &j, "verified", "before_result"); err != nil {
		return Result{}, err
	}
	state := updatetxn.StateSuccess
	if cfg.Request.Action == updatetxn.ActionRollback {
		state = updatetxn.StateRolledBack
	}
	return finishedResult(cfg, version, state, ""), nil
}

func checkpoint(cfg Config, j *updatetxn.Journal, phase, fault string) error {
	j.Phase = phase
	if err := updatetxn.WriteJournal(cfg.StateDir, *j); err != nil {
		return err
	}
	return injectFault(cfg, fault)
}
func injectFault(cfg Config, phase string) error {
	if cfg.Fault != nil {
		return cfg.Fault(phase)
	}
	return nil
}

func verifyRunning(ctx context.Context, cfg Config, baseline Snapshot) error {
	if err := waitReadiness(ctx, cfg.Probe); err != nil {
		return err
	}
	active, err := cfg.Runner.IsActive(ctx, gatewayService)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("Gateway service inactive")
	}
	return cfg.Probe.Verify(ctx, baseline)
}

// Recover observes digests and service state; it never runs preflight, writes a
// candidate, or overwrites previous with the newly installed binary.
func Recover(_ context.Context, cfg Config) (Result, bool, error) {
	j, err := updatetxn.ReadJournal(cfg.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, false, nil
	}
	repair := func(cause error) (Result, bool, error) {
		return failedResult(cfg, "", updatetxn.StateRepairRequired, "repair_required"), true, &codedError{code: "repair_required", err: cause}
	}
	if err != nil {
		return repair(err)
	}
	if j.Request.RunID != cfg.RunID || j.Request.Kind != updatetxn.KindGateway || cfg.Runner == nil || cfg.Probe == nil {
		return repair(errors.New("journal identity mismatch"))
	}
	if j.State == updatetxn.StateRepairRequired {
		return repair(errors.New("previous recovery requires manual repair"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	current, err := readTrustedFile(cfg.BinaryPath, nil, 512<<20)
	if err != nil {
		return repair(err)
	}
	currentDigest := updatetxn.Digest(current)
	if currentDigest != j.OldDigest && currentDigest != j.NewDigest {
		return repair(errors.New("current binary digest is unknown"))
	}
	previous, err := readTrustedFile(cfg.PreviousPath, nil, 512<<20)
	if err != nil || updatetxn.Digest(previous) != j.OldDigest {
		// A crash can happen after journal persistence but before previous backup.
		if j.Phase != "prepared" || currentDigest != j.OldDigest {
			return repair(errors.New("previous binary digest is unknown"))
		}
		if err := writeAndReplace(cfg.PreviousPath, current, 0755); err != nil {
			return repair(err)
		}
	}
	baseline := Snapshot{Fingerprint: j.Baseline}
	active, _ := cfg.Runner.IsActive(ctx, gatewayService)
	if !active {
		if err := cfg.Runner.Start(ctx, gatewayService); err != nil {
			result, restoreErr := rollbackAfterFailure(ctx, cfg, baseline, j.Request.Version, "start_failed", err)
			return persistRecovered(cfg, j, result, restoreErr)
		}
	}
	if err := verifyRunning(ctx, cfg, baseline); err != nil {
		result, restoreErr := rollbackAfterFailure(ctx, cfg, baseline, j.Request.Version, "health_check_failed", err)
		return persistRecovered(cfg, j, result, restoreErr)
	}
	state := updatetxn.StateSuccess
	if currentDigest == j.OldDigest || j.Request.Action == updatetxn.ActionRollback {
		state = updatetxn.StateRolledBack
	}
	if j.State.Terminal() {
		state = j.State
	}
	return persistRecovered(cfg, j, finishedResult(cfg, j.Request.Version, state, j.ErrorCode), nil)
}

func persistRecovered(cfg Config, j updatetxn.Journal, result Result, cause error) (Result, bool, error) {
	j.Phase, j.State, j.ErrorCode = "terminal", result.State, result.ErrorCode
	if err := updatetxn.WriteJournal(cfg.StateDir, j); err != nil {
		return failedResult(cfg, j.Request.Version, updatetxn.StateRepairRequired, "repair_required"), true, &codedError{code: "repair_required", err: err}
	}
	return result, true, cause
}
