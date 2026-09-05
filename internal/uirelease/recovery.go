package uirelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func journalSwitch(ctx context.Context, cfg Config, current, previous, target string) (Result, error) {
	j := updatetxn.Journal{Request: cfg.Request, OldDigest: current, NewDigest: target, OldPrevious: previous, Baseline: "ui-pointers-v1", Phase: "prepared"}
	if err := updatetxn.WriteJournal(cfg.StateDir, j); err != nil {
		return Result{}, err
	}
	if err := uiCheckpoint(cfg, &j, "prepared", "before_switch"); err != nil {
		return Result{}, err
	}
	if err := setPointer(cfg, "previous", current); err != nil {
		return uiRepair(cfg, j, err)
	}
	if err := uiCheckpoint(cfg, &j, "stopped", "after_previous"); err != nil {
		return Result{}, err
	}
	if err := setPointer(cfg, "current", target); err != nil {
		return restoreUI(cfg, j, err)
	}
	if err := uiCheckpoint(cfg, &j, "switched", "after_current"); err != nil {
		return Result{}, err
	}
	if cfg.HealthCheck != nil {
		if err := cfg.HealthCheck(ctx, target); err != nil {
			return restoreUI(cfg, j, err)
		}
	}
	if err := uiCheckpoint(cfg, &j, "verified", "before_result"); err != nil {
		return Result{}, err
	}
	state := updatetxn.StateSuccess
	if cfg.Request.Action == updatetxn.ActionRollback {
		state = updatetxn.StateRolledBack
	}
	return uiTerminal(cfg, j, target, current, state, "")
}

func recoverUI(cfg Config) (Result, bool, error) {
	if cfg.StateDir == "" {
		return Result{}, false, nil
	}
	j, err := updatetxn.ReadJournal(cfg.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, true, &codedError{code: "repair_required", err: err}
	}
	if j.Request != cfg.Request || j.Request.Kind != updatetxn.KindUI || j.State == updatetxn.StateRepairRequired {
		return Result{}, true, &codedError{code: "repair_required", err: errors.New("UI journal requires repair")}
	}
	current, currentErr := cfg.Pointers.Get(cfg.Root, "current")
	previous, previousErr := cfg.Pointers.Get(cfg.Root, "previous")
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return Result{}, true, currentErr
	}
	if previousErr != nil && !errors.Is(previousErr, os.ErrNotExist) {
		return Result{}, true, previousErr
	}
	if (current != j.OldDigest && current != j.NewDigest) || (previous != j.OldPrevious && previous != j.OldDigest) {
		result, err := uiRepair(cfg, j, errors.New("UI pointer identity unknown"))
		return result, true, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if current == j.NewDigest {
		if previous != j.OldDigest {
			result, err := uiRepair(cfg, j, errors.New("UI previous pointer drift"))
			return result, true, err
		}
		if cfg.HealthCheck != nil {
			if err := cfg.HealthCheck(ctx, current); err != nil {
				result, restoreErr := restoreUI(cfg, j, err)
				return result, true, restoreErr
			}
		}
		state := updatetxn.StateSuccess
		if j.Request.Action == updatetxn.ActionRollback {
			state = updatetxn.StateRolledBack
		}
		result, err := uiTerminal(cfg, j, current, previous, state, "")
		return result, true, err
	}
	// Before current was switched, keep the old version; never replay apply.
	if cfg.HealthCheck != nil {
		if err := cfg.HealthCheck(ctx, current); err != nil {
			result, repairErr := uiRepair(cfg, j, err)
			return result, true, repairErr
		}
	}
	result, err := uiTerminal(cfg, j, current, previous, updatetxn.StateRolledBack, "")
	return result, true, err
}

func restoreUI(cfg Config, j updatetxn.Journal, cause error) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := setPointer(cfg, "current", j.OldDigest); err != nil {
		return uiRepair(cfg, j, err)
	}
	if err := setPointer(cfg, "previous", j.OldPrevious); err != nil {
		return uiRepair(cfg, j, err)
	}
	if cfg.HealthCheck != nil {
		if err := cfg.HealthCheck(ctx, j.OldDigest); err != nil {
			return uiRepair(cfg, j, err)
		}
	}
	_ = cause
	return uiTerminal(cfg, j, j.OldDigest, j.OldPrevious, updatetxn.StateRolledBack, "health_check_failed")
}

func uiRepair(cfg Config, j updatetxn.Journal, cause error) (Result, error) {
	_, _ = uiTerminal(cfg, j, "", "", updatetxn.StateRepairRequired, "repair_required")
	return Result{State: "repair_required"}, &codedError{code: "repair_required", err: cause}
}

func uiTerminal(cfg Config, j updatetxn.Journal, current, previous string, state updatetxn.State, code string) (Result, error) {
	j.Phase, j.State, j.ErrorCode = "terminal", state, code
	if err := updatetxn.WriteJournal(cfg.StateDir, j); err != nil {
		return Result{State: "repair_required"}, &codedError{code: "repair_required", err: err}
	}
	return Result{Version: current, PreviousVersion: previous, State: string(state)}, nil
}

func uiCheckpoint(cfg Config, j *updatetxn.Journal, phase, fault string) error {
	j.Phase = phase
	if err := updatetxn.WriteJournal(cfg.StateDir, *j); err != nil {
		return err
	}
	if cfg.Fault != nil {
		return cfg.Fault(fault)
	}
	return nil
}

func setPointer(cfg Config, name, value string) error {
	if value == "" {
		return cfg.Pointers.Remove(cfg.Root, name)
	}
	return cfg.Pointers.Set(cfg.Root, name, value)
}

// Cleanup runs only after the root terminal result is durable.
func Cleanup(root string) error {
	p := osPointers{}
	current, err := p.Get(root, "current")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	previous, err := p.Get(root, "previous")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeOlderReleases(filepath.Join(root, "releases"), current, previous)
}
