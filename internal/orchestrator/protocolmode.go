package orchestrator

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/protocoltxn"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

func (o *Orchestrator) SwitchProtocolMode(ctx context.Context, egressID string, target domain.ProtocolMode) (domain.EgressProtocolMode, error) {
	return o.SwitchProtocolModeExpected(ctx, egressID, target, "")
}

func (o *Orchestrator) CanResumeInterruptedProtocolMode(ctx context.Context, egressID string, original, target domain.ProtocolMode) bool {
	if !original.Valid() || !target.Valid() || original == target {
		return false
	}
	persistence, ok := o.store.(protocolModeStore)
	if !ok {
		return false
	}
	state, err := persistence.GetEgressProtocolMode(ctx, egressID)
	if err != nil || state.State != domain.ProtocolReady || state.ActiveMode != original ||
		state.LastOperationID == "" || state.LastRequestHash == "" {
		return false
	}
	resource, err := o.protocolTarget(ctx, egressID)
	if err != nil {
		return false
	}
	request := protocoltxn.Request{
		OperationID: state.LastOperationID, EgressID: egressID,
		InboundID: resource.inboundID, InboundTag: resource.inboundTag, Port: resource.port,
		OldMode: string(original), NewMode: string(target),
	}
	return protocolRequestFingerprint(request) == state.LastRequestHash
}

func (o *Orchestrator) SwitchProtocolModeExpected(ctx context.Context, egressID string, target, expected domain.ProtocolMode) (domain.EgressProtocolMode, error) {
	if !target.Valid() || !strings.HasPrefix(egressID, "agw-") {
		return domain.EgressProtocolMode{}, &Error{Code: "invalid_request"}
	}
	persistence, ok := o.store.(protocolModeStore)
	if !ok || o.protocolTransaction == nil {
		return domain.EgressProtocolMode{}, &Error{Code: "not_configured"}
	}
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	unlocked := o.locks.lock(egressID)
	defer unlocked()

	state, err := persistence.GetEgressProtocolMode(ctx, egressID)
	if err != nil {
		return domain.EgressProtocolMode{}, operationError(err)
	}
	if expected != "" && (!expected.Valid() || state.ActiveMode != expected) {
		return domain.EgressProtocolMode{}, &Error{Code: "expected_state_mismatch"}
	}
	if state.State == domain.ProtocolRepairRequired {
		if err := o.revalidateProtocolRepair(ctx, persistence, &state); err != nil {
			return domain.EgressProtocolMode{}, err
		}
	}
	if state.State == domain.ProtocolReady && state.ActiveMode == target {
		return state, nil
	}
	if state.State != domain.ProtocolReady {
		return domain.EgressProtocolMode{}, &Error{Code: "operation_busy"}
	}
	targetResource, err := o.protocolTarget(ctx, egressID)
	if err != nil {
		return domain.EgressProtocolMode{}, err
	}
	operationID, err := newProtocolOperationID()
	if err != nil {
		return domain.EgressProtocolMode{}, &Error{Code: "operation_failed"}
	}
	operationCtx := ctx
	var mutationLease *mainMutationLeaseGuard
	if targetResource.main {
		mutationLease, err = o.acquireMainMutationLease(ctx, operationID)
		if err != nil {
			return domain.EgressProtocolMode{}, err
		}
		operationCtx = mutationLease.operationCtx
		defer func() {
			mutationLease.stopAndWait()
			_ = o.aimili.ReleaseMutationLease(context.WithoutCancel(ctx), mutationLease.leaseID)
			mutationLease.cancelOperation()
		}()
	}
	targetResource, err = o.syncProtocolTargetEgress(operationCtx, targetResource)
	if err != nil {
		return domain.EgressProtocolMode{}, err
	}
	if err := o.verifyEgressReady(operationCtx, &targetResource); err != nil {
		return domain.EgressProtocolMode{}, err
	}
	request := protocoltxn.Request{
		OperationID: operationID, EgressID: egressID,
		InboundID: targetResource.inboundID, InboundTag: targetResource.inboundTag, Port: targetResource.port,
		OldMode: string(state.ActiveMode), NewMode: string(target),
	}
	request.ExpectedFingerprint = protocolRequestFingerprint(request)
	state.DesiredMode = target
	state.LastOperationID = operationID
	state.LastRequestHash = request.ExpectedFingerprint
	state.LastErrorCode = ""
	if err := state.Transition(domain.ProtocolSwitching); err != nil || o.saveProtocolState(ctx, persistence, &state) != nil {
		return domain.EgressProtocolMode{}, &Error{Code: "storage_failed"}
	}

	applied := false
	validationCtx := operationCtx
	stopHelperRenew := func() {}
	waitHelperRenew := func() error { return nil }
	result, applyErr := o.protocolTransaction.Apply(operationCtx, request)
	if applyErr == nil && result.Status == "applied" && result.OperationID == operationID {
		applied = true
	} else if applyErr == nil {
		applyErr = &Error{Code: codeOrResult(result.ErrorCode, "protocol_apply_failed")}
	}
	if applyErr == nil {
		renewed, renewErr := o.protocolTransaction.Renew(operationCtx, operationID)
		if renewErr != nil || renewed.Status != "renewed" || renewed.OperationID != operationID {
			applyErr = &Error{Code: "protocol_renew_failed"}
		} else {
			var cancelValidation context.CancelFunc
			validationCtx, cancelValidation = context.WithCancel(operationCtx)
			renewCtx, cancelRenew := context.WithCancel(operationCtx)
			renewDone := make(chan struct{})
			renewFailed := make(chan error, 1)
			go func() {
				defer close(renewDone)
				ticker := time.NewTicker(60 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-renewCtx.Done():
						return
					case <-ticker.C:
						result, err := o.protocolTransaction.Renew(renewCtx, operationID)
						if err != nil || result.Status != "renewed" || result.OperationID != operationID {
							select {
							case renewFailed <- &Error{Code: "protocol_renew_failed"}:
							default:
							}
							cancelValidation()
							return
						}
					}
				}
			}()
			stopHelperRenew = cancelRenew
			waitHelperRenew = func() error {
				cancelRenew()
				<-renewDone
				cancelValidation()
				select {
				case err := <-renewFailed:
					return err
				default:
					return nil
				}
			}
		}
	}
	if applyErr == nil {
		if transitionErr := state.Transition(domain.ProtocolSubscriptionPending); transitionErr != nil || o.saveProtocolState(ctx, persistence, &state) != nil {
			applyErr = &Error{Code: "storage_failed"}
		}
	}
	if applyErr == nil {
		var subscription SubscriptionResult
		subscription, applyErr = o.verifySubscription(validationCtx)
		if applyErr == nil {
			applyErr = o.verifyProtocolTarget(validationCtx, targetResource, target, subscription)
		}
	}
	stopHelperRenew()
	if renewErr := waitHelperRenew(); renewErr != nil {
		applyErr = renewErr
	}
	if mutationLease != nil {
		if leaseErr := mutationLease.failure(); leaseErr != nil {
			applyErr = leaseErr
		}
		if applyErr == nil {
			_, applyErr = mutationLease.renewNow(operationCtx)
		}
	}
	if applyErr == nil {
		finalized, finalizeErr := o.protocolTransaction.Finalize(operationCtx, operationID)
		if finalizeErr != nil {
			applyErr = finalizeErr
		} else if finalized.Status != "finalized" || finalized.OperationID != operationID {
			applyErr = &Error{Code: codeOrResult(finalized.ErrorCode, "protocol_finalize_failed")}
		}
		if mutationLease != nil {
			if leaseErr := mutationLease.failure(); leaseErr != nil {
				applyErr = leaseErr
			}
		}
	}
	if applyErr == nil {
		state.ActiveMode = target
		state.DesiredMode = target
		state.LastErrorCode = ""
		if err := state.Transition(domain.ProtocolReady); err != nil {
			return domain.EgressProtocolMode{}, &Error{Code: "storage_failed"}
		}
		if err := o.saveProtocolState(ctx, persistence, &state); err != nil {
			return o.markFinalizedProtocolRepair(ctx, persistence, state)
		}
		if mutationLease != nil {
			mutationLease.stopAndWait()
			if leaseErr := mutationLease.failure(); leaseErr != nil {
				return o.markMutationLeaseRepair(ctx, persistence, state)
			}
		}
		return state, nil
	}
	if errorCode(applyErr) == "mutation_lease_lost" {
		return o.markProtocolRepair(ctx, persistence, state)
	}
	return o.rollbackProtocolMode(operationCtx, ctx, persistence, state, targetResource, applyErr, applied, mutationLease)
}

// revalidateProtocolRepair only unlocks a previous failed transaction after the
// currently recorded protocol, subscription, and egress identity all pass the
// same read-only checks used by a completed rollback.
func (o *Orchestrator) revalidateProtocolRepair(ctx context.Context, persistence protocolModeStore, state *domain.EgressProtocolMode) error {
	target, err := o.protocolRepairTarget(ctx, state.EgressID)
	if err != nil {
		return err
	}
	if target.main {
		target, err = o.snapshotProtocolRepairMain(ctx, target)
	} else {
		target, err = o.syncProtocolTargetEgress(ctx, target)
	}
	if err != nil {
		return err
	}
	subscription, err := o.verifySubscription(ctx)
	if errorCode(err) == "subscription_incomplete" {
		if _, repairErr := o.repairSubscriptionAliases(ctx); repairErr != nil {
			err = repairErr
		} else {
			subscription, err = o.verifySubscription(ctx)
		}
	}
	if err == nil {
		err = o.verifyProtocolTarget(ctx, target, state.ActiveMode, subscription)
	}
	if err != nil {
		state.LastErrorCode = codeOr(err, "protocol_repair_validation_failed")
		_ = o.saveProtocolState(ctx, persistence, state)
		return err
	}
	if target.main {
		if err := o.saveProtocolRepairMain(ctx, target.group); err != nil {
			return err
		}
	} else if target.repairEgress {
		target.group.Status = domain.ProxyGroupReady
		target.group.LastErrorCode = ""
		target.group.LastCheckedAt = o.config.Now().UTC()
		target.group.UpdatedAt = target.group.LastCheckedAt
		if err := o.save(ctx, &target.group); err != nil {
			return err
		}
	}
	state.DesiredMode = state.ActiveMode
	state.LastErrorCode = ""
	if err := state.Transition(domain.ProtocolReady); err != nil || o.saveProtocolState(ctx, persistence, state) != nil {
		return &Error{Code: "storage_failed"}
	}
	return nil
}

func (o *Orchestrator) snapshotProtocolRepairMain(ctx context.Context, target protocolTarget) (protocolTarget, error) {
	status, err := o.aimili.MainStatus(ctx)
	if err != nil || !mainStatusUsable(status) {
		return protocolTarget{}, &Error{Code: "egress_unavailable"}
	}
	target.group.CandidateID = status.CandidateID
	target.group.CountryCode = normalizedMainCountry(status.Country)
	target.group.CountryName = status.CountryName
	target.group.ProxyType = normalizedMainProxyType(status.ProxyType)
	target.group.ExitIP = status.ExitIP
	return target, nil
}

func (o *Orchestrator) saveProtocolRepairMain(ctx context.Context, group domain.ProxyGroup) error {
	persistence, ok := o.store.(mainEgressStore)
	if !ok {
		return &Error{Code: "not_configured"}
	}
	main, err := persistence.GetMainEgress(ctx)
	if err != nil {
		return operationError(err)
	}
	main.CandidateID = group.CandidateID
	main.CountryCode = group.CountryCode
	main.CountryName = group.CountryName
	main.ProxyType = group.ProxyType
	main.ExitIP = group.ExitIP
	main.LastErrorCode = ""
	main.LastCheckedAt = o.config.Now().UTC()
	main.UpdatedAt = main.LastCheckedAt
	if err := persistence.SaveMainEgress(ctx, main); err != nil {
		return &Error{Code: "storage_failed"}
	}
	return nil
}

type mainMutationLeaseGuard struct {
	leaseID            string
	operationCtx       context.Context
	cancelOperation    context.CancelFunc
	stopRenew          context.CancelFunc
	done               chan struct{}
	renewMutationLease func(context.Context, string) (aimili.MutationLease, error)
	renewMu            sync.Mutex
	failureMu          sync.Mutex
	failureErr         error
}

func (o *Orchestrator) acquireMainMutationLease(ctx context.Context, idempotencyKey string) (*mainMutationLeaseGuard, error) {
	lease, err := o.aimili.AcquireMutationLease(ctx, idempotencyKey)
	if err != nil {
		var adapterError *aimili.AdapterError
		if errors.As(err, &adapterError) && (adapterError.Code == "lease_busy" || adapterError.Code == "operation_busy") {
			return nil, &Error{Code: adapterError.Code}
		}
		return nil, &Error{Code: "mutation_lease_acquire_failed"}
	}
	if lease.State != "active" || lease.LeaseID == "" || lease.ExpiresAt <= 0 {
		return nil, &Error{Code: "mutation_lease_acquire_failed"}
	}
	if mutationLeaseRenewDelay(lease.ExpiresAt) <= 0 {
		leaseID := lease.LeaseID
		lease, err = o.aimili.RenewMutationLease(ctx, leaseID)
		if err != nil || lease.State != "active" || lease.LeaseID != leaseID {
			_ = o.aimili.ReleaseMutationLease(context.WithoutCancel(ctx), leaseID)
			return nil, &Error{Code: "mutation_lease_lost"}
		}
	}
	operationCtx, cancelOperation := context.WithCancel(ctx)
	renewCtx, stopRenew := context.WithCancel(ctx)
	guard := &mainMutationLeaseGuard{
		leaseID: lease.LeaseID, operationCtx: operationCtx, cancelOperation: cancelOperation, stopRenew: stopRenew,
		done: make(chan struct{}), renewMutationLease: o.aimili.RenewMutationLease,
	}
	waitForRenewal := o.mutationLeaseRenewWait
	if waitForRenewal == nil {
		waitForRenewal = waitForMutationLeaseRenewal
	}
	go func(expiresAt float64) {
		defer close(guard.done)
		for {
			if !waitForRenewal(renewCtx, mutationLeaseRenewDelay(expiresAt)) {
				return
			}
			var renewErr error
			expiresAt, renewErr = guard.renewNow(renewCtx)
			if renewErr != nil {
				return
			}
		}
	}(lease.ExpiresAt)
	return guard, nil
}

func (g *mainMutationLeaseGuard) renewNow(ctx context.Context) (float64, error) {
	g.renewMu.Lock()
	defer g.renewMu.Unlock()
	if err := g.failure(); err != nil {
		return 0, err
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	renewed, err := g.renewMutationLease(ctx, g.leaseID)
	if err != nil || renewed.State != "active" || renewed.LeaseID != g.leaseID || renewed.ExpiresAt <= 0 {
		return 0, g.fail()
	}
	return renewed.ExpiresAt, nil
}

func (g *mainMutationLeaseGuard) fail() error {
	g.failureMu.Lock()
	defer g.failureMu.Unlock()
	if g.failureErr == nil {
		g.failureErr = &Error{Code: "mutation_lease_lost"}
		g.cancelOperation()
	}
	return g.failureErr
}

func waitForMutationLeaseRenewal(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (g *mainMutationLeaseGuard) stopAndWait() {
	g.stopRenew()
	<-g.done
}

func (g *mainMutationLeaseGuard) failure() error {
	g.failureMu.Lock()
	defer g.failureMu.Unlock()
	return g.failureErr
}

func mutationLeaseRenewDelay(expiresAt float64) time.Duration {
	seconds, fraction := math.Modf(expiresAt)
	expires := time.Unix(int64(seconds), int64(fraction*float64(time.Second)))
	delay := time.Until(expires.Add(-20 * time.Second))
	if delay < 0 {
		return 0
	}
	return delay
}

// RecoverProtocolModes converges protocol transactions interrupted by a
// Gateway restart before ordinary pool reconciliation is allowed to mutate
// shared runtime state.
func (o *Orchestrator) RecoverProtocolModes(ctx context.Context) error {
	persistence, ok := o.store.(protocolModeStore)
	if !ok {
		return &Error{Code: "not_configured"}
	}
	ctx, mutationUnlock := o.lockMutation(ctx)
	defer mutationUnlock()
	states, err := persistence.ListEgressProtocolModes(ctx)
	if err != nil {
		return &Error{Code: "storage_failed"}
	}
	repairRemains := false
	for _, state := range states {
		if state.State == domain.ProtocolReady {
			if state.ActiveMode != state.DesiredMode {
				_, repairErr := o.markProtocolRepair(ctx, persistence, state)
				return repairErr
			}
			if state.LastErrorCode != "" {
				unlock := o.locks.lock(state.EgressID)
				state.LastErrorCode = ""
				err := o.saveProtocolState(ctx, persistence, &state)
				unlock()
				if err != nil {
					return &Error{Code: "storage_failed"}
				}
			}
			continue
		}
		unlock := o.locks.lock(state.EgressID)
		if state.State == domain.ProtocolRepairRequired {
			err := o.revalidateProtocolRepair(ctx, persistence, &state)
			unlock()
			if err != nil {
				if errorCode(err) == "storage_failed" || errorCode(err) == "not_configured" {
					return err
				}
				repairRemains = true
			}
			continue
		}
		err := o.recoverProtocolMode(ctx, persistence, state)
		unlock()
		if err != nil {
			return err
		}
	}
	if repairRemains {
		return &Error{Code: "repair_required"}
	}
	return nil
}

func (o *Orchestrator) recoverProtocolMode(ctx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode) error {
	if o.protocolTransaction == nil {
		_, err := o.markProtocolRepair(ctx, persistence, state)
		return err
	}
	if state.State == domain.ProtocolSwitching || state.State == domain.ProtocolSubscriptionPending {
		if err := state.Transition(domain.ProtocolRollingBack); err != nil {
			_, repairErr := o.markProtocolRepair(ctx, persistence, state)
			return repairErr
		}
		if state.LastErrorCode == "" {
			state.LastErrorCode = "protocol_switch_interrupted"
		}
		if err := o.saveProtocolState(ctx, persistence, &state); err != nil {
			return &Error{Code: "repair_required"}
		}
	}
	target, err := o.protocolTarget(ctx, state.EgressID)
	if err != nil {
		_, repairErr := o.markProtocolRepair(ctx, persistence, state)
		return repairErr
	}
	operationCtx := ctx
	var mutationLease *mainMutationLeaseGuard
	if target.main {
		mutationLease, err = o.acquireMainMutationLease(ctx, state.LastOperationID)
		if err != nil {
			_, repairErr := o.markProtocolRepair(ctx, persistence, state)
			return repairErr
		}
		operationCtx = mutationLease.operationCtx
		defer func() {
			mutationLease.stopAndWait()
			_ = o.aimili.ReleaseMutationLease(context.WithoutCancel(ctx), mutationLease.leaseID)
			mutationLease.cancelOperation()
		}()
	}
	result, rollbackErr := o.protocolTransaction.Rollback(operationCtx, state.LastOperationID)
	finalized := rollbackErr == nil && result.OperationID == state.LastOperationID && result.Status == "failed" && result.ErrorCode == "operation_finalized"
	if finalized {
		request := protocoltxn.Request{
			OperationID: state.LastOperationID, EgressID: state.EgressID,
			InboundID: target.inboundID, InboundTag: target.inboundTag, Port: target.port,
			OldMode: string(state.ActiveMode), NewMode: string(state.DesiredMode),
		}
		if state.DesiredMode == state.ActiveMode || state.LastRequestHash == "" || protocolRequestFingerprint(request) != state.LastRequestHash {
			_, repairErr := o.markProtocolRepair(ctx, persistence, state)
			return repairErr
		}
		return o.convergeRecoveredProtocolMode(operationCtx, ctx, persistence, state, target, state.DesiredMode, mutationLease)
	}
	rollbackSafe := rollbackErr == nil && result.OperationID == state.LastOperationID && (result.Status == "rolled_back" ||
		(result.Status == "failed" && result.ErrorCode == "operation_not_applied"))
	if mutationLease != nil && mutationLease.failure() != nil {
		_, repairErr := o.markMutationLeaseRepair(ctx, persistence, state)
		return repairErr
	}
	if !rollbackSafe {
		_, repairErr := o.markProtocolRepair(ctx, persistence, state)
		return repairErr
	}
	return o.convergeRecoveredProtocolMode(operationCtx, ctx, persistence, state, target, state.ActiveMode, mutationLease)
}

func (o *Orchestrator) convergeRecoveredProtocolMode(runtimeCtx, persistenceCtx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode, target protocolTarget, recoveredMode domain.ProtocolMode, mutationLease *mainMutationLeaseGuard) error {
	if target.main {
		status, statusErr := o.aimili.MainStatus(runtimeCtx)
		if statusErr != nil || !mainStatusUsable(status) {
			_, repairErr := o.markProtocolRepair(persistenceCtx, persistence, state)
			return repairErr
		}
		target.group.CandidateID = status.CandidateID
		target.group.CountryCode = normalizedMainCountry(status.Country)
		target.group.CountryName = status.CountryName
		target.group.ProxyType = normalizedMainProxyType(status.ProxyType)
		target.group.ExitIP = status.ExitIP
	}
	subscription, err := o.verifySubscription(runtimeCtx)
	if err == nil {
		err = o.verifyProtocolTarget(runtimeCtx, target, recoveredMode, subscription)
	}
	if err != nil {
		_, repairErr := o.markProtocolRepair(persistenceCtx, persistence, state)
		return repairErr
	}
	if target.main {
		if _, renewErr := mutationLease.renewNow(runtimeCtx); renewErr != nil {
			_, repairErr := o.markProtocolRepair(persistenceCtx, persistence, state)
			return repairErr
		}
		status, statusErr := o.aimili.MainStatus(runtimeCtx)
		mainStore, storeOK := o.store.(mainEgressStore)
		if statusErr != nil || !storeOK || !mainStatusMatchesGroup(status, target.group) {
			_, repairErr := o.markProtocolRepair(persistenceCtx, persistence, state)
			return repairErr
		}
		main, mainErr := mainStore.GetMainEgress(persistenceCtx)
		if mainErr != nil {
			_, repairErr := o.markProtocolRepair(persistenceCtx, persistence, state)
			return repairErr
		}
		main.CandidateID = status.CandidateID
		main.CountryCode = normalizedMainCountry(status.Country)
		main.CountryName = status.CountryName
		main.ProxyType = normalizedMainProxyType(status.ProxyType)
		main.ExitIP = status.ExitIP
		main.LastErrorCode = ""
		main.LastCheckedAt = o.config.Now().UTC()
		main.UpdatedAt = main.LastCheckedAt
		if o.store.SaveMainEgress(persistenceCtx, main) != nil {
			_, repairErr := o.markProtocolRepair(persistenceCtx, persistence, state)
			return repairErr
		}
	}
	state.ActiveMode = recoveredMode
	state.DesiredMode = recoveredMode
	state.LastErrorCode = ""
	if err := state.Transition(domain.ProtocolReady); err != nil || o.saveProtocolState(persistenceCtx, persistence, &state) != nil {
		return &Error{Code: "repair_required"}
	}
	if mutationLease != nil {
		mutationLease.stopAndWait()
		if mutationLease.failure() != nil {
			_, repairErr := o.markMutationLeaseRepair(persistenceCtx, persistence, state)
			return repairErr
		}
	}
	return nil
}

func (o *Orchestrator) markFinalizedProtocolRepair(ctx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode) (domain.EgressProtocolMode, error) {
	state.State = domain.ProtocolRepairRequired
	state.DesiredMode = state.ActiveMode
	state.LastErrorCode = "final_state_persist_failed"
	_ = o.saveProtocolState(ctx, persistence, &state)
	return domain.EgressProtocolMode{}, &Error{Code: "repair_required"}
}

func (o *Orchestrator) markMutationLeaseRepair(ctx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode) (domain.EgressProtocolMode, error) {
	state.State = domain.ProtocolRepairRequired
	state.DesiredMode = state.ActiveMode
	state.LastErrorCode = "mutation_lease_lost"
	_ = o.saveProtocolState(ctx, persistence, &state)
	return domain.EgressProtocolMode{}, &Error{Code: "repair_required"}
}

type protocolTarget struct {
	egressID     string
	inboundID    int64
	inboundTag   string
	port         int
	group        domain.ProxyGroup
	main         bool
	repairEgress bool
}

func (o *Orchestrator) protocolTarget(ctx context.Context, egressID string) (protocolTarget, error) {
	if egressID == "agw-main" {
		persistence, ok := o.store.(mainEgressStore)
		if !ok {
			return protocolTarget{}, &Error{Code: "not_configured"}
		}
		main, err := persistence.GetMainEgress(ctx)
		if err != nil || !main.Enabled || main.PublicInboundID < 1 || main.PublicPort != 8443 {
			return protocolTarget{}, &Error{Code: "not_ready"}
		}
		return protocolTarget{egressID: egressID, inboundID: main.PublicInboundID, inboundTag: "aimili-reality", port: main.PublicPort, group: mainEgressGroup(main), main: true}, nil
	}
	group, err := o.store.GetProxyGroup(ctx, egressID)
	if err != nil {
		return protocolTarget{}, operationError(err)
	}
	if group.Status != domain.ProxyGroupReady || group.PublicInboundID < 1 || group.PublicPort < 1 || group.AimiliSlot < 0 {
		return protocolTarget{}, &Error{Code: "not_ready"}
	}
	return protocolTarget{egressID: egressID, inboundID: group.PublicInboundID, inboundTag: group.ResourceName + "-vless", port: group.PublicPort, group: group}, nil
}

// protocolRepairTarget admits only a structurally complete ready or degraded
// ordinary slot. Its live Aimili identity is still re-read and verified before
// any stored identity or protocol lock is changed.
func (o *Orchestrator) protocolRepairTarget(ctx context.Context, egressID string) (protocolTarget, error) {
	if egressID == "agw-main" {
		return o.protocolTarget(ctx, egressID)
	}
	group, err := o.store.GetProxyGroup(ctx, egressID)
	if err != nil {
		return protocolTarget{}, operationError(err)
	}
	if (group.Status != domain.ProxyGroupReady && group.Status != domain.ProxyGroupDegraded) || group.PublicInboundID < 1 || group.PublicPort < 1 || group.AimiliSlot < 0 {
		return protocolTarget{}, &Error{Code: "not_ready"}
	}
	return protocolTarget{egressID: egressID, inboundID: group.PublicInboundID, inboundTag: group.ResourceName + "-vless", port: group.PublicPort, group: group, repairEgress: true}, nil
}

func (o *Orchestrator) syncProtocolTargetEgress(ctx context.Context, target protocolTarget) (protocolTarget, error) {
	if !target.main && !target.repairEgress {
		return target, nil
	}
	if target.repairEgress {
		checked, err := o.aimili.CheckSlot(ctx, target.group.AimiliSlot)
		if err != nil || !usableProtocolRepairSlot(checked) {
			return protocolTarget{}, &Error{Code: "egress_unavailable"}
		}
		applySlotSnapshot(&target.group, checked)
		return target, nil
	}
	if _, err := o.checkMain(ctx, true); err != nil {
		return protocolTarget{}, err
	}
	return o.protocolTarget(ctx, target.egressID)
}

func (o *Orchestrator) verifyProtocolTarget(ctx context.Context, target protocolTarget, mode domain.ProtocolMode, subscription SubscriptionResult) error {
	if err := o.verifyEgressReady(ctx, &target); err != nil {
		return err
	}
	var profile *xui.PublicProfile
	for index := range subscription.PublicProfiles {
		candidate := &subscription.PublicProfiles[index]
		if candidate.InboundID == target.inboundID && candidate.Mode == mode {
			profile = candidate
			break
		}
	}
	if profile == nil {
		return &Error{Code: "subscription_incomplete"}
	}
	_, err := o.validator.ValidatePublic(ctx, validator.PublicTarget{
		Mode: mode, XrayPath: o.config.XrayPath, InboundAddress: net.JoinHostPort("127.0.0.1", fmt.Sprint(target.port)),
		ClientID: profile.ClientID, Auth: profile.Auth, PublicKey: profile.PublicKey, ShortID: profile.ShortID,
		ServerName: profile.ServerName, MLDSA65Verify: profile.MLDSA65Verify, XHTTPPath: profile.XHTTPPath,
		TLSServerName: profile.ServerName, ProbeHost: o.config.ProbeHost, ExpectedExitIP: target.group.ExitIP,
	})
	return err
}

func (o *Orchestrator) verifyEgressReady(ctx context.Context, target *protocolTarget) error {
	_, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return err
	}
	if target.main {
		status, err := o.aimili.MainStatus(ctx)
		if err != nil || !mainStatusMatchesGroup(status, target.group) {
			return &Error{Code: "egress_unavailable"}
		}
		target.group.ExitIP = status.ExitIP
	} else {
		checked, err := o.aimili.CheckSlot(ctx, target.group.AimiliSlot)
		if err != nil || !protocolRepairSlotMatchesGroup(checked, target.group) {
			return &Error{Code: "egress_unavailable"}
		}
	}
	if _, err := o.validateSOCKS(ctx, target.group, credentials); err != nil {
		return err
	}
	return nil
}

func usableProtocolRepairSlot(checked aimili.SlotCheck) bool {
	country := strings.ToUpper(strings.TrimSpace(checked.Country))
	proxyType := domain.ProxyType(strings.ToLower(strings.TrimSpace(checked.ProxyType)))
	return checked.Status == "up" && checked.EgressOK && strings.TrimSpace(checked.NodeID) != "" &&
		net.ParseIP(strings.TrimSpace(checked.ExitIP)) != nil && len(country) == 2 && proxyType.Valid()
}

func protocolRepairSlotMatchesGroup(checked aimili.SlotCheck, group domain.ProxyGroup) bool {
	return usableProtocolRepairSlot(checked) && checked.NodeID == group.CandidateID &&
		strings.TrimSpace(checked.ExitIP) == group.ExitIP &&
		strings.ToUpper(strings.TrimSpace(checked.Country)) == group.CountryCode &&
		domain.ProxyType(strings.ToLower(strings.TrimSpace(checked.ProxyType))) == group.ProxyType
}

func mainStatusUsable(status aimili.MainStatus) bool {
	proxyType := domain.ProxyType(strings.ToLower(strings.TrimSpace(status.ProxyType)))
	return status.Active && status.EgressOK && status.Port == 7928 && status.CandidateID != "" &&
		net.ParseIP(status.ExitIP) != nil && normalizedMainCountry(status.Country) != "ZZ" &&
		proxyType.Valid()
}

func mainStatusMatchesGroup(status aimili.MainStatus, group domain.ProxyGroup) bool {
	return mainStatusUsable(status) && status.CandidateID == group.CandidateID && status.ExitIP == group.ExitIP &&
		normalizedMainCountry(status.Country) == group.CountryCode && normalizedMainProxyType(status.ProxyType) == group.ProxyType
}

func (o *Orchestrator) rollbackProtocolMode(runtimeCtx, persistenceCtx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode, target protocolTarget, cause error, applied bool, mutationLease *mainMutationLeaseGuard) (domain.EgressProtocolMode, error) {
	causeCode := errorCode(cause)
	if causeCode == "operation_failed" {
		causeCode = "protocol_switch_failed"
	}
	if state.State == domain.ProtocolSwitching || state.State == domain.ProtocolSubscriptionPending {
		_ = state.Transition(domain.ProtocolRollingBack)
		state.LastErrorCode = causeCode
		if err := o.saveProtocolState(persistenceCtx, persistence, &state); err != nil {
			return domain.EgressProtocolMode{}, &Error{Code: "repair_required"}
		}
	}
	if applied {
		rolled, err := o.protocolTransaction.Rollback(runtimeCtx, state.LastOperationID)
		if err != nil || rolled.Status != "rolled_back" || rolled.OperationID != state.LastOperationID {
			if mutationLease != nil && mutationLease.failure() != nil {
				return o.markMutationLeaseRepair(persistenceCtx, persistence, state)
			}
			return o.markProtocolRepairWithCode(persistenceCtx, persistence, state, "protocol_rollback_failed")
		}
	}
	if mutationLease != nil && mutationLease.failure() != nil {
		return o.markMutationLeaseRepair(persistenceCtx, persistence, state)
	}
	subscription, err := o.verifySubscription(runtimeCtx)
	if err != nil {
		if mutationLease != nil && mutationLease.failure() != nil {
			return o.markMutationLeaseRepair(persistenceCtx, persistence, state)
		}
		return o.markProtocolRepairWithCode(persistenceCtx, persistence, state, "protocol_rollback_subscription_failed")
	}
	if err := o.verifyProtocolTarget(runtimeCtx, target, state.ActiveMode, subscription); err != nil {
		if mutationLease != nil && mutationLease.failure() != nil {
			return o.markMutationLeaseRepair(persistenceCtx, persistence, state)
		}
		return o.markProtocolRepairWithCode(persistenceCtx, persistence, state, "protocol_rollback_validation_failed")
	}
	if mutationLease != nil {
		if _, err := mutationLease.renewNow(runtimeCtx); err != nil {
			return o.markMutationLeaseRepair(persistenceCtx, persistence, state)
		}
	}
	state.DesiredMode = state.ActiveMode
	if err := state.Transition(domain.ProtocolReady); err != nil || o.saveProtocolState(persistenceCtx, persistence, &state) != nil {
		return domain.EgressProtocolMode{}, &Error{Code: "repair_required"}
	}
	if mutationLease != nil {
		mutationLease.stopAndWait()
		if mutationLease.failure() != nil {
			return o.markMutationLeaseRepair(persistenceCtx, persistence, state)
		}
	}
	return domain.EgressProtocolMode{}, &Error{Code: causeCode}
}

func (o *Orchestrator) markProtocolRepair(ctx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode) (domain.EgressProtocolMode, error) {
	return o.markProtocolRepairWithCode(ctx, persistence, state, "rollback_failed")
}

func (o *Orchestrator) markProtocolRepairWithCode(ctx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode, code string) (domain.EgressProtocolMode, error) {
	state.State = domain.ProtocolRepairRequired
	state.DesiredMode = state.ActiveMode
	state.LastErrorCode = code
	_ = o.saveProtocolState(ctx, persistence, &state)
	return domain.EgressProtocolMode{}, &Error{Code: "repair_required"}
}

func (o *Orchestrator) saveProtocolState(ctx context.Context, persistence protocolModeStore, state *domain.EgressProtocolMode) error {
	expected := state.Version
	state.UpdatedAt = o.config.Now().UTC()
	if err := persistence.UpdateEgressProtocolMode(ctx, *state, expected); err != nil {
		return err
	}
	state.Version++
	return nil
}

func newProtocolOperationID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "protocol-" + hex.EncodeToString(value), nil
}

func protocolRequestFingerprint(request protocoltxn.Request) string {
	material := map[string]any{
		"egressId": request.EgressID, "inboundId": request.InboundID, "inboundTag": request.InboundTag,
		"newMode": request.NewMode, "oldMode": request.OldMode, "operationId": request.OperationID, "port": request.Port,
	}
	encoded, _ := json.Marshal(material)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func codeOrResult(code, fallback string) string {
	if code == "" {
		return fallback
	}
	if len(code) > 64 {
		return fallback
	}
	for _, char := range code {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return fallback
		}
	}
	return code
}
