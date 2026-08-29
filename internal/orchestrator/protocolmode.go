package orchestrator

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/protocoltxn"
)

func (o *Orchestrator) SwitchProtocolMode(ctx context.Context, egressID string, target domain.ProtocolMode) (domain.EgressProtocolMode, error) {
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
	result, applyErr := o.protocolTransaction.Apply(ctx, request)
	if applyErr == nil && result.Status == "applied" && result.OperationID == operationID {
		applied = true
	} else if applyErr == nil {
		applyErr = &Error{Code: codeOrResult(result.ErrorCode, "protocol_apply_failed")}
	}
	if applyErr == nil {
		if transitionErr := state.Transition(domain.ProtocolSubscriptionPending); transitionErr != nil || o.saveProtocolState(ctx, persistence, &state) != nil {
			applyErr = &Error{Code: "storage_failed"}
		}
	}
	if applyErr == nil {
		_, applyErr = o.Subscription(ctx)
	}
	if applyErr == nil {
		applyErr = o.verifyProtocolTarget(ctx, targetResource)
	}
	if applyErr == nil {
		finalized, finalizeErr := o.protocolTransaction.Finalize(ctx, operationID)
		if finalizeErr != nil {
			applyErr = finalizeErr
		} else if finalized.Status != "finalized" || finalized.OperationID != operationID {
			applyErr = &Error{Code: codeOrResult(finalized.ErrorCode, "protocol_finalize_failed")}
		}
	}
	if applyErr == nil {
		state.ActiveMode = target
		state.DesiredMode = target
		state.LastErrorCode = ""
		if err := state.Transition(domain.ProtocolReady); err != nil || o.saveProtocolState(ctx, persistence, &state) != nil {
			return domain.EgressProtocolMode{}, &Error{Code: "storage_failed"}
		}
		return state, nil
	}
	return o.rollbackProtocolMode(ctx, persistence, state, targetResource, applyErr, applied)
}

type protocolTarget struct {
	egressID   string
	inboundID  int64
	inboundTag string
	port       int
	group      domain.ProxyGroup
	main       bool
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

func (o *Orchestrator) verifyProtocolTarget(ctx context.Context, target protocolTarget) error {
	_, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return err
	}
	if target.main {
		status, err := o.aimili.MainStatus(ctx)
		if err != nil || !status.Active || !status.EgressOK || status.Port != 7928 || net.ParseIP(status.ExitIP) == nil || status.ExitIP != target.group.ExitIP {
			return &Error{Code: "egress_unavailable"}
		}
		target.group.ExitIP = status.ExitIP
	} else {
		checked, err := o.aimili.CheckSlot(ctx, target.group.AimiliSlot)
		if err != nil || !checked.EgressOK || net.ParseIP(checked.ExitIP) == nil || checked.ExitIP != target.group.ExitIP {
			return &Error{Code: "egress_unavailable"}
		}
	}
	if _, err := o.validateSOCKS(ctx, target.group, credentials); err != nil {
		return err
	}
	_, err = o.validateVLESS(ctx, target.group, credentials)
	return err
}

func (o *Orchestrator) rollbackProtocolMode(ctx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode, target protocolTarget, cause error, applied bool) (domain.EgressProtocolMode, error) {
	causeCode := errorCode(cause)
	if causeCode == "operation_failed" {
		causeCode = "protocol_switch_failed"
	}
	if state.State == domain.ProtocolSwitching || state.State == domain.ProtocolSubscriptionPending {
		_ = state.Transition(domain.ProtocolRollingBack)
		state.LastErrorCode = causeCode
		if err := o.saveProtocolState(ctx, persistence, &state); err != nil {
			return domain.EgressProtocolMode{}, &Error{Code: "repair_required"}
		}
	}
	if applied {
		rolled, err := o.protocolTransaction.Rollback(ctx, state.LastOperationID)
		if err != nil || rolled.Status != "rolled_back" || rolled.OperationID != state.LastOperationID {
			return o.markProtocolRepair(ctx, persistence, state)
		}
	}
	if _, err := o.Subscription(ctx); err != nil {
		return o.markProtocolRepair(ctx, persistence, state)
	}
	if err := o.verifyProtocolTarget(ctx, target); err != nil {
		return o.markProtocolRepair(ctx, persistence, state)
	}
	state.DesiredMode = state.ActiveMode
	if err := state.Transition(domain.ProtocolReady); err != nil || o.saveProtocolState(ctx, persistence, &state) != nil {
		return domain.EgressProtocolMode{}, &Error{Code: "repair_required"}
	}
	return domain.EgressProtocolMode{}, &Error{Code: causeCode}
}

func (o *Orchestrator) markProtocolRepair(ctx context.Context, persistence protocolModeStore, state domain.EgressProtocolMode) (domain.EgressProtocolMode, error) {
	state.State = domain.ProtocolRepairRequired
	state.DesiredMode = state.ActiveMode
	state.LastErrorCode = "rollback_failed"
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
