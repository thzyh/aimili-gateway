package updatetxn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var (
	hexIdentifier = regexp.MustCompile(`^[0-9a-f]{64}$`)
	semVersion    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

type Client struct {
	RequestDir       string
	ResultDir        string
	TrustedResultUID *uint32
	Now              func() time.Time
}

type leaseRecord struct {
	RunID   string `json:"runId"`
	Kind    Kind   `json:"kind"`
	Version string `json:"version,omitempty"`
	Action  Action `json:"action"`
	DryRun  bool   `json:"dryRun"`
}

func NewRunID() (string, error) {
	body := make([]byte, 32)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return hex.EncodeToString(body), nil
}

func (c *Client) Submit(ctx context.Context, request Request) (Result, error) {
	if err := ValidateRequest(request); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if request.RequestedAt.IsZero() {
		request.RequestedAt = c.now()
	}
	release, err := AcquireFileLock(filepath.Join(c.RequestDir, ".submit.lock"), nil)
	if err != nil {
		return Result{}, err
	}
	defer release()
	leasePath := filepath.Join(c.RequestDir, ".update.lease")
	if result, handled, err := c.resolveExistingLease(ctx, leasePath, request); handled || err != nil {
		return result, err
	}
	if err := writeAtomicJSON(leasePath, leaseRecord{RunID: request.RunID, Kind: request.Kind, Version: request.Version, Action: request.Action, DryRun: request.DryRun}); err != nil {
		if errors.Is(err, os.ErrExist) {
			if result, handled, resolveErr := c.resolveExistingLease(ctx, leasePath, request); handled || resolveErr != nil {
				return result, resolveErr
			}
			return Result{}, ErrUpdateBusy
		}
		return Result{}, err
	}
	requestPath := filepath.Join(c.RequestDir, request.RunID+".json")
	if err := writeAtomicJSONMode(requestPath, request, 0o640); err != nil {
		_ = os.Remove(leasePath)
		return Result{}, err
	}
	return pendingResult(request), nil
}

func (c *Client) Get(ctx context.Context, runID string) (Result, error) {
	if !hexIdentifier.MatchString(runID) {
		return Result{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	resultPath := filepath.Join(c.ResultDir, runID+".json")
	var result Result
	if err := readTrustedJSON(resultPath, c.TrustedResultUID, &result); err == nil {
		if err := validateResult(result, runID); err != nil {
			return Result{}, err
		}
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	var request Request
	if err := readTrustedJSON(filepath.Join(c.RequestDir, runID+".json"), nil, &request); err != nil {
		return Result{}, err
	}
	if err := ValidateRequest(request); err != nil || request.RunID != runID {
		return Result{}, ErrInvalidRequest
	}
	return pendingResult(request), nil
}

func (c *Client) resolveExistingLease(ctx context.Context, leasePath string, request Request) (Result, bool, error) {
	var lease leaseRecord
	if err := readTrustedJSON(leasePath, nil, &lease); errors.Is(err, os.ErrNotExist) {
		return Result{}, false, nil
	} else if err != nil {
		return Result{}, true, ErrUpdateBusy
	}
	if lease.RunID == request.RunID {
		if lease.Kind != request.Kind || lease.Version != request.Version || lease.Action != request.Action || lease.DryRun != request.DryRun {
			return Result{}, true, ErrRunConflict
		}
		if result, err := c.Get(ctx, request.RunID); err == nil && result.State.Terminal() {
			return result, true, nil
		}
		var existing Request
		if err := readTrustedJSON(filepath.Join(c.RequestDir, request.RunID+".json"), nil, &existing); err != nil {
			return Result{}, true, ErrUpdateBusy
		}
		if !sameRequest(existing, request) {
			return Result{}, true, ErrRunConflict
		}
		result, err := c.Get(ctx, request.RunID)
		return result, true, err
	}
	result, err := c.Get(ctx, lease.RunID)
	if err == nil && result.State.Terminal() && result.State != StateRepairRequired {
		if removeErr := os.Remove(leasePath); removeErr != nil {
			return Result{}, true, removeErr
		}
		return Result{}, false, nil
	}
	return Result{}, true, ErrUpdateBusy
}

func ValidateRequest(request Request) error {
	if !hexIdentifier.MatchString(request.RunID) || (request.Kind != KindUI && request.Kind != KindGateway) ||
		(request.Action != ActionApply && request.Action != ActionRollback) {
		return ErrInvalidRequest
	}
	if request.Action == ActionRollback {
		if request.Version != "" {
			return ErrInvalidRequest
		}
		return nil
	}
	if request.Kind == KindUI && !hexIdentifier.MatchString(request.Version) {
		return ErrInvalidRequest
	}
	if request.Kind == KindGateway && !semVersion.MatchString(request.Version) {
		return ErrInvalidRequest
	}
	return nil
}

func validateResult(result Result, runID string) error {
	if result.RunID != runID || (result.Kind != KindUI && result.Kind != KindGateway) || !validState(result.State) {
		return fmt.Errorf("%w: invalid result", ErrUntrustedResult)
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StatePending, StateDownloading, StateValidating, StateSwitching, StateVerifying, StateRolledBack, StateSuccess, StateFailed, StateRepairRequired:
		return true
	default:
		return false
	}
}

func sameRequest(left, right Request) bool {
	return left.RunID == right.RunID && left.Kind == right.Kind && left.Version == right.Version && left.Action == right.Action && left.DryRun == right.DryRun
}

func pendingResult(request Request) Result {
	return Result{RunID: request.RunID, Kind: request.Kind, Version: request.Version, State: StatePending, StartedAt: request.RequestedAt}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}
