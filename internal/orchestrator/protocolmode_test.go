package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/protocoltxn"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

func TestSwitchProtocolModeAppliesVerifiesFinalizesAndPreservesOtherState(t *testing.T) {
	fixture, group := protocolFixture(t)
	other := domain.EgressProtocolMode{EgressID: "agw-other", ActiveMode: domain.ProtocolVLESSTCPRealityVision, DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.store.protocolModes[other.EgressID] = other
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	result, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if err != nil {
		t.Fatal(err)
	}
	if result.ActiveMode != domain.ProtocolVLESSTCPRealityVision || result.DesiredMode != result.ActiveMode || result.State != domain.ProtocolReady || result.LastErrorCode != "" {
		t.Fatalf("protocol result = %#v", result)
	}
	want := []string{"protocol.apply", "slot.check", "validate.socks", "validate.public", "protocol.finalize"}
	if !orderedSubset(fixture.calls, want) {
		t.Fatalf("calls = %#v, want ordered %#v", fixture.calls, want)
	}
	if client.applied.EgressID != group.ID || client.applied.InboundID != group.PublicInboundID || client.applied.InboundTag != group.ResourceName+"-vless" || client.applied.Port != group.PublicPort {
		t.Fatalf("unsafe transaction target: %#v", client.applied)
	}
	if got := fixture.store.protocolModes[other.EgressID]; got != other {
		t.Fatalf("non-target protocol state changed: %#v", got)
	}
}

func TestSwitchProtocolModeRejectsPendingSlotBeforeRuntimeWrites(t *testing.T) {
	fixture, group := protocolFixture(t)
	slot := fixture.aimili.createdSlots[group.AimiliSlot]
	slot.Status = "pending"
	slot.EgressOK = false
	fixture.aimili.createdSlots[group.AimiliSlot] = slot
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)

	if codeOf(err) != "egress_unavailable" || len(client.actions) != 0 {
		t.Fatalf("error=%v actions=%#v calls=%#v", err, client.actions, fixture.calls)
	}
}

func TestSwitchProtocolModeRejectsBrokenMixedBeforeRuntimeWrites(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.validator.socksErrors = []error{&validator.Error{Code: "protocol_failed"}}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)

	var validationError *validator.Error
	if !errors.As(err, &validationError) || validationError.Code != "protocol_failed" || len(client.actions) != 0 {
		t.Fatalf("error=%v actions=%#v calls=%#v", err, client.actions, fixture.calls)
	}
}

func TestSwitchProtocolModeRollsBackAndRevalidatesOldPath(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.validator.publicErrors = []error{&validator.Error{Code: "protocol_failed"}, nil}
	fixture.xui.profileSequences = [][]xui.PublicProfile{
		{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSTCPRealityVision, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test"}},
		{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/old-path"}},
	}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if codeOf(err) != "protocol_failed" {
		t.Fatalf("error = %v", err)
	}
	want := []string{"protocol.apply", "validate.public", "protocol.rollback", "validate.public"}
	if !orderedSubset(fixture.calls, want) || contains(fixture.calls, "protocol.finalize") {
		t.Fatalf("rollback calls = %#v", fixture.calls)
	}
	state := fixture.store.protocolModes[group.ID]
	if state.ActiveMode != domain.ProtocolVLESSXHTTPReality || state.DesiredMode != state.ActiveMode || state.State != domain.ProtocolReady || state.LastErrorCode != "protocol_failed" {
		t.Fatalf("rolled back state = %#v", state)
	}
}

func TestSwitchProtocolModeRenewsHelperLeaseWhileValidationIsBlocked(t *testing.T) {
	fixture, group := protocolFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	validator := &blockingProtocolValidator{delegate: fixture.validator, entered: entered, release: release}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client
	orchestrator.validator = validator
	done := make(chan error, 1)

	go func() {
		_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
		done <- err
	}()

	<-entered
	if !orderedSubset(fixture.calls, []string{"protocol.apply", "protocol.renew", "validate.socks"}) {
		t.Fatalf("helper lease was not renewed before blocked validation: %#v", fixture.calls)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSwitchProtocolModeRenewFailureRollsBackAndNeverFinalizes(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.xui.profileSequences = [][]xui.PublicProfile{{{
		InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSXHTTPReality,
		ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id",
		ServerName: "proxy.example.test", XHTTPPath: "/old-path",
	}}}
	client := &fakeProtocolTransaction{calls: &fixture.calls, renewError: errors.New("renew failed")}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)

	if codeOf(err) != "protocol_renew_failed" {
		t.Fatalf("error = %v", err)
	}
	if !orderedSubset(fixture.calls, []string{"protocol.apply", "protocol.renew", "protocol.rollback"}) || contains(fixture.calls, "protocol.finalize") {
		t.Fatalf("renew failure did not fail closed: %#v", fixture.calls)
	}
}

func TestSwitchProtocolModeMarksRepairWhenRollbackOrOldVerificationFails(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.validator.publicErrors = []error{&validator.Error{Code: "protocol_failed"}, &validator.Error{Code: "protocol_failed"}}
	client := &fakeProtocolTransaction{calls: &fixture.calls, rollbackError: errors.New("rollback failed")}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	state := fixture.store.protocolModes[group.ID]
	if state.State != domain.ProtocolRepairRequired || state.ActiveMode != domain.ProtocolVLESSXHTTPReality {
		t.Fatalf("repair state = %#v", state)
	}
}

func TestSwitchProtocolModeMarksRepairWhenFinalStateCannotBePersistedAfterFinalize(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.store.protocolUpdateErrors = map[int]error{3: errors.New("storage unavailable")}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	state := fixture.store.protocolModes[group.ID]
	if state.State != domain.ProtocolRepairRequired || state.ActiveMode != domain.ProtocolVLESSTCPRealityVision || state.DesiredMode != domain.ProtocolVLESSTCPRealityVision || state.LastErrorCode != "final_state_persist_failed" {
		t.Fatalf("finalized runtime was not recorded as repair-required: %#v", state)
	}
	if !contains(fixture.calls, "protocol.finalize") || contains(fixture.calls, "protocol.rollback") {
		t.Fatalf("finalized transaction was incorrectly rolled back: %#v", fixture.calls)
	}
}

func TestSwitchProtocolModeSameReadyTargetPerformsNoWrites(t *testing.T) {
	fixture, group := protocolFixture(t)
	state := fixture.store.protocolModes[group.ID]
	state.ActiveMode = domain.ProtocolVLESSTCPRealityVision
	state.DesiredMode = state.ActiveMode
	fixture.store.protocolModes[group.ID] = state
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	result, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if err != nil || result != state {
		t.Fatalf("idempotent result=%#v err=%v", result, err)
	}
	if len(client.actions) != 0 || fixture.store.protocolUpdates != 0 {
		t.Fatalf("idempotent switch performed writes: actions=%#v updates=%d", client.actions, fixture.store.protocolUpdates)
	}
}

func TestSwitchProtocolModeExpectedModeMismatchPerformsNoWrites(t *testing.T) {
	fixture, group := protocolFixture(t)
	orchestrator := fixture.orchestrator(t)
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolModeExpected(
		context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision, domain.ProtocolHysteria2QUICTLS,
	)

	if codeOf(err) != "expected_state_mismatch" {
		t.Fatalf("error = %v", err)
	}
	if len(client.actions) != 0 || fixture.store.protocolUpdates != 0 {
		t.Fatalf("mismatched CAS performed writes: actions=%#v updates=%d", client.actions, fixture.store.protocolUpdates)
	}
}

func TestCanResumeInterruptedProtocolModeRequiresMatchingRecoveredTransactionFingerprint(t *testing.T) {
	fixture, group := protocolFixture(t)
	state := fixture.store.protocolModes[group.ID]
	state.LastOperationID = "protocol-recovered-proof"
	request := protocoltxn.Request{
		OperationID: state.LastOperationID, EgressID: group.ID,
		InboundID: group.PublicInboundID, InboundTag: group.ResourceName + "-vless", Port: group.PublicPort,
		OldMode: string(domain.ProtocolVLESSXHTTPReality), NewMode: string(domain.ProtocolVLESSTCPRealityVision),
	}
	state.LastRequestHash = protocolRequestFingerprint(request)
	fixture.store.protocolModes[group.ID] = state
	orchestrator := fixture.orchestratorWithMax(t, 3)

	if !orchestrator.CanResumeInterruptedProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSXHTTPReality, domain.ProtocolVLESSTCPRealityVision) {
		t.Fatal("matching recovered transaction was not resumable")
	}
	if orchestrator.CanResumeInterruptedProtocolMode(context.Background(), group.ID, domain.ProtocolHysteria2QUICTLS, domain.ProtocolVLESSTCPRealityVision) {
		t.Fatal("third current mode was accepted as the interrupted original mode")
	}
}

func TestSwitchMainProtocolChecksMainTunnelAndMixedPath(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "US", ProxyType: domain.ProxyTypeDatacenter,
		CandidateID: "main-node", ExitIP: "203.0.113.10", PublicInboundID: 7, MixedInboundID: 8,
		PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.aimili.mainStatus = aimili.MainStatus{CandidateID: "main-node", Country: "US", ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928, EgressOK: true, Active: true}
	fixture.xui.snapshot.Inbounds = []xui.Inbound{{ID: 7, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	result, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != domain.ProtocolReady || fixture.validator.socksCalls != 2 || len(fixture.validator.publicTargets) != 1 {
		t.Fatalf("main path was not fully checked: result=%#v calls=%#v", result, fixture.calls)
	}
	if client.applied.InboundTag != "aimili-reality" || client.applied.Port != 8443 {
		t.Fatalf("main transaction target = %#v", client.applied)
	}
	if !orderedSubset(fixture.calls, []string{"main.lease.acquire", "protocol.apply", "protocol.finalize", "main.lease.release"}) {
		t.Fatalf("main mutation lease did not cover transaction: %#v", fixture.calls)
	}
}

func TestSwitchMainProtocolAcquireFailurePerformsNoHelperMutation(t *testing.T) {
	fixture := mainProtocolFixture()
	fixture.aimili.mutationLeaseAcquireError = &aimili.AdapterError{Code: "operation_busy"}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)

	if codeOf(err) != "operation_busy" {
		t.Fatalf("error = %v", err)
	}
	if len(client.actions) != 0 || contains(fixture.calls, "main.lease.release") {
		t.Fatalf("failed acquire mutated helper or released unknown lease: %#v", fixture.calls)
	}
}

func TestSwitchMainProtocolMutationLeaseRenewFailureStopsHelperMutationAndMarksRepair(t *testing.T) {
	fixture := mainProtocolFixture()
	fixture.aimili.mutationLeaseExpires = float64(time.Now().Add(21 * time.Second).Unix())
	fixture.aimili.mutationLeaseRenewError = &aimili.AdapterError{Code: "lease_not_found"}
	fixture.aimili.mutationLeaseRenewed = make(chan struct{}, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	validator := &blockingProtocolValidator{delegate: fixture.validator, entered: entered, release: release}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client
	orchestrator.validator = validator
	done := make(chan error, 1)
	go func() {
		_, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
		done <- err
	}()
	<-entered

	err := <-done
	close(release)
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	if !orderedSubset(fixture.calls, []string{"main.lease.acquire", "protocol.apply", "main.lease.release"}) || contains(fixture.calls, "protocol.rollback") || contains(fixture.calls, "protocol.finalize") {
		t.Fatalf("lost mutation lease did not fail closed: %#v", fixture.calls)
	}
	select {
	case <-fixture.aimili.mutationLeaseRenewed:
	default:
		t.Fatal("mutation lease renew was not attempted")
	}
	if state := fixture.store.protocolModes["agw-main"]; state.State != domain.ProtocolRepairRequired {
		t.Fatalf("lost mutation lease state = %#v", state)
	}
}

func TestSwitchMainProtocolKeepsMutationLeaseRenewingWhileFinalizeIsBlocked(t *testing.T) {
	fixture := mainProtocolFixture()
	fixture.aimili.mutationLeaseRenewed = make(chan struct{}, 8)
	renewTicks := make(chan struct{}, 8)
	finalizeEntered := make(chan struct{})
	finalizeRelease := make(chan struct{})
	client := &fakeProtocolTransaction{
		calls: &fixture.calls, finalizeEntered: finalizeEntered, finalizeRelease: finalizeRelease,
	}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client
	orchestrator.mutationLeaseRenewWait = controlledMutationLeaseRenewWait(renewTicks)
	done := make(chan error, 1)

	go func() {
		_, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
		done <- err
	}()
	waitForSignal(t, finalizeEntered, "finalize did not start")
	drainSignals(fixture.aimili.mutationLeaseRenewed)
	if contains(fixture.calls, "main.lease.release") {
		close(finalizeRelease)
		<-done
		t.Fatal("mutation lease was released before finalize completed")
	}

	var renewFailure string
	for attempt := 1; attempt <= 2; attempt++ {
		renewTicks <- struct{}{}
		select {
		case <-fixture.aimili.mutationLeaseRenewed:
		case <-time.After(200 * time.Millisecond):
			renewFailure = "mutation lease heartbeat stopped during finalize"
		}
		if renewFailure != "" {
			break
		}
	}
	close(finalizeRelease)
	err := <-done
	if renewFailure != "" {
		t.Fatal(renewFailure)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !contains(fixture.calls, "main.lease.release") {
		t.Fatalf("mutation lease was not released after ready state persisted: %#v", fixture.calls)
	}
}

func TestSwitchMainProtocolMutationLeaseLossDuringFinalizeCancelsHelperAndMarksRepair(t *testing.T) {
	fixture := mainProtocolFixture()
	fixture.aimili.mutationLeaseRenewed = make(chan struct{}, 8)
	fixture.aimili.mutationLeaseRenewErrors = make(chan error, 1)
	renewTicks := make(chan struct{}, 2)
	finalizeEntered := make(chan struct{})
	finalizeRelease := make(chan struct{})
	client := &fakeProtocolTransaction{
		calls: &fixture.calls, finalizeEntered: finalizeEntered, finalizeRelease: finalizeRelease,
	}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client
	orchestrator.mutationLeaseRenewWait = controlledMutationLeaseRenewWait(renewTicks)
	done := make(chan error, 1)

	go func() {
		_, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
		done <- err
	}()
	waitForSignal(t, finalizeEntered, "finalize did not start")
	drainSignals(fixture.aimili.mutationLeaseRenewed)
	fixture.aimili.mutationLeaseRenewErrors <- errors.New("lease lost")
	renewTicks <- struct{}{}

	var err error
	select {
	case err = <-done:
		close(finalizeRelease)
	case <-time.After(300 * time.Millisecond):
		close(finalizeRelease)
		err = <-done
		t.Fatalf("lease loss did not cancel blocked finalize; eventual error = %v", err)
	}
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	if contains(fixture.calls, "protocol.rollback") || !contains(fixture.calls, "main.lease.release") {
		t.Fatalf("lease loss continued helper mutation or leaked lease: %#v", fixture.calls)
	}
	if state := fixture.store.protocolModes["agw-main"]; state.State != domain.ProtocolRepairRequired {
		t.Fatalf("lost mutation lease state = %#v", state)
	}
}

func TestSwitchMainProtocolMutationLeaseCoversBlockedRollbackAndLossFailsClosed(t *testing.T) {
	fixture := mainProtocolFixture()
	fixture.validator.publicErrors = []error{&validator.Error{Code: "protocol_failed"}}
	fixture.aimili.mutationLeaseRenewed = make(chan struct{}, 8)
	fixture.aimili.mutationLeaseRenewErrors = make(chan error, 1)
	renewTicks := make(chan struct{}, 3)
	rollbackEntered := make(chan struct{})
	rollbackRelease := make(chan struct{})
	client := &fakeProtocolTransaction{
		calls: &fixture.calls, rollbackEntered: rollbackEntered, rollbackRelease: rollbackRelease,
	}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client
	orchestrator.mutationLeaseRenewWait = controlledMutationLeaseRenewWait(renewTicks)
	done := make(chan error, 1)

	go func() {
		_, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
		done <- err
	}()
	waitForSignal(t, rollbackEntered, "rollback did not start")
	drainSignals(fixture.aimili.mutationLeaseRenewed)
	renewTicks <- struct{}{}
	waitForSignal(t, fixture.aimili.mutationLeaseRenewed, "mutation lease did not renew during rollback")
	fixture.aimili.mutationLeaseRenewErrors <- errors.New("lease lost")
	renewTicks <- struct{}{}

	var err error
	select {
	case err = <-done:
		close(rollbackRelease)
	case <-time.After(300 * time.Millisecond):
		close(rollbackRelease)
		err = <-done
		t.Fatalf("lease loss did not cancel blocked rollback; eventual error = %v", err)
	}
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	if !contains(fixture.calls, "main.lease.release") {
		t.Fatalf("mutation lease was not released after repair state persisted: %#v", fixture.calls)
	}
	if state := fixture.store.protocolModes["agw-main"]; state.State != domain.ProtocolRepairRequired {
		t.Fatalf("lost mutation lease state = %#v", state)
	}
}

func controlledMutationLeaseRenewWait(ticks <-chan struct{}) func(context.Context, time.Duration) bool {
	return func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-ticks:
			return true
		}
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func drainSignals(signals <-chan struct{}) {
	for {
		select {
		case <-signals:
		default:
			return
		}
	}
}

func mainProtocolFixture() *fixture {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "US", ProxyType: domain.ProxyTypeDatacenter,
		CandidateID: "main-node", ExitIP: "203.0.113.10", PublicInboundID: 7, MixedInboundID: 8,
		PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.aimili.mainStatus = aimili.MainStatus{CandidateID: "main-node", Country: "US", ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928, EgressOK: true, Active: true}
	fixture.xui.snapshot.Inbounds = []xui.Inbound{{ID: 7, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	return fixture
}

func TestProtocolSwitchAndSlotRotationShareMutationGate(t *testing.T) {
	fixture, group := protocolFixture(t)
	other := group
	other.ID = "agw-other"
	other.ResourceName = "agw-other"
	other.AimiliSlot = 2
	other.PublicPort = 20001
	other.MixedPort = 30001
	other.PublicInboundID = 21
	other.MixedInboundID = 22
	fixture.store.groups[other.ID] = other
	fixture.aimili.createdSlots[2] = aimili.Slot{Number: 2, NodeID: "other", Country: "US", ProxyType: "datacenter", ExitIP: "203.0.113.8", EgressOK: true}
	entered := make(chan struct{})
	release := make(chan struct{})
	client := &fakeProtocolTransaction{calls: &fixture.calls, applyEntered: entered, applyRelease: release}
	fixture.aimili.rotateEntered = make(chan struct{})
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	}()
	<-entered
	go func() {
		defer wait.Done()
		_, _ = orchestrator.Rotate(context.Background(), other.ID)
	}()
	select {
	case <-fixture.aimili.rotateEntered:
		t.Fatal("slot rotation entered while protocol transaction held the mutation gate")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	wait.Wait()
	select {
	case <-fixture.aimili.rotateEntered:
	default:
		t.Fatalf("slot rotation did not resume: %#v", fixture.calls)
	}
}

func TestRecoverProtocolModesRollsBackInterruptedTransactionBeforeValidatingOldPath(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/old-path"}}
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolSubscriptionPending
	state.DesiredMode = domain.ProtocolVLESSTCPRealityVision
	state.LastOperationID = "protocol-recovery-one"
	fixture.store.protocolModes[group.ID] = state
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	if err := orchestrator.RecoverProtocolModes(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := fixture.store.protocolModes[group.ID]
	if recovered.State != domain.ProtocolReady || recovered.ActiveMode != domain.ProtocolVLESSXHTTPReality || recovered.DesiredMode != recovered.ActiveMode {
		t.Fatalf("recovered state = %#v", recovered)
	}
	if !orderedSubset(fixture.calls, []string{"protocol.rollback", "slot.check", "validate.socks", "validate.public"}) {
		t.Fatalf("recovery calls = %#v", fixture.calls)
	}
}

func TestRecoverProtocolModesClearsStaleErrorFromReadyStateWithoutRuntimeWrites(t *testing.T) {
	fixture, group := protocolFixture(t)
	state := fixture.store.protocolModes[group.ID]
	state.LastErrorCode = "rollback_failed"
	fixture.store.protocolModes[group.ID] = state
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	if err := orchestrator.RecoverProtocolModes(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := fixture.store.protocolModes[group.ID]
	if recovered.State != domain.ProtocolReady || recovered.LastErrorCode != "" || recovered.Version != state.Version+1 {
		t.Fatalf("recovered state = %#v", recovered)
	}
	if len(client.actions) != 0 {
		t.Fatalf("ready-state cleanup performed runtime writes: %#v", client.actions)
	}
}

func TestRecoverProtocolModesAcceptsNotAppliedOnlyAfterOldPathValidation(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/old-path"}}
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRollingBack
	state.DesiredMode = domain.ProtocolVLESSTCPRealityVision
	state.LastOperationID = "protocol-recovery-two"
	fixture.store.protocolModes[group.ID] = state
	client := &fakeProtocolTransaction{calls: &fixture.calls, rollbackResult: protocoltxn.Result{OperationID: state.LastOperationID, Status: "failed", ErrorCode: "operation_not_applied"}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	if err := orchestrator.RecoverProtocolModes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recovered := fixture.store.protocolModes[group.ID]; recovered.State != domain.ProtocolReady || recovered.DesiredMode != recovered.ActiveMode {
		t.Fatalf("recovered state = %#v", recovered)
	}
}

func TestRecoverProtocolModesMarksRepairWhenRollbackCannotProveOldState(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/old-path"}}
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRollingBack
	state.DesiredMode = domain.ProtocolVLESSTCPRealityVision
	state.LastOperationID = "protocol-recovery-three"
	fixture.store.protocolModes[group.ID] = state
	client := &fakeProtocolTransaction{calls: &fixture.calls, rollbackResult: protocoltxn.Result{OperationID: state.LastOperationID, Status: "failed", ErrorCode: "unsafe_path"}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	if codeOf(orchestrator.RecoverProtocolModes(context.Background())) != "repair_required" {
		t.Fatal("unsafe rollback did not require repair")
	}
	if recovered := fixture.store.protocolModes[group.ID]; recovered.State != domain.ProtocolRepairRequired || recovered.DesiredMode != recovered.ActiveMode {
		t.Fatalf("repair state = %#v", recovered)
	}
}

func TestRecoverRepairRequiredMainRebindsOnlyAfterCurrentProtocolValidation(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "US", CountryName: "United States", ProxyType: domain.ProxyTypeDatacenter,
		CandidateID: "expired-main", ExitIP: "203.0.113.10", PublicInboundID: 7, MixedInboundID: 8,
		PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{
		EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality,
		State: domain.ProtocolRepairRequired, LastOperationID: "protocol-main-recovery", LastErrorCode: "rollback_failed", Version: 3, UpdatedAt: fixture.now(),
	}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "current-main", Country: "JP", CountryName: "日本", ProxyType: "datacenter",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}
	fixture.xui.snapshot.Inbounds = []xui.Inbound{{ID: 7, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: 7, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	if err := orchestrator.RecoverProtocolModes(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := fixture.store.protocolModes["agw-main"]
	if recovered.State != domain.ProtocolReady || recovered.ActiveMode != domain.ProtocolVLESSXHTTPReality || recovered.DesiredMode != recovered.ActiveMode || recovered.LastErrorCode != "" {
		t.Fatalf("recovered protocol = %#v", recovered)
	}
	main := fixture.store.mainEgress
	if main.CandidateID != "current-main" || main.CountryCode != "JP" || main.ExitIP != "203.0.113.20" {
		t.Fatalf("recovered main identity = %#v", main)
	}
	if !orderedSubset(fixture.calls, []string{"protocol.rollback", "validate.socks", "validate.public"}) || fixture.xui.ensureLegacyMainCalls != 0 {
		t.Fatalf("recovery calls = %#v legacy=%d", fixture.calls, fixture.xui.ensureLegacyMainCalls)
	}
	if !orderedSubset(fixture.calls, []string{"main.lease.acquire", "protocol.rollback", "validate.public", "main.lease.release"}) {
		t.Fatalf("main recovery was not covered by mutation lease: %#v", fixture.calls)
	}
	if len(fixture.validator.publicTargets) != 1 || fixture.validator.publicTargets[0].ExpectedExitIP != "203.0.113.20" {
		t.Fatalf("public targets = %#v", fixture.validator.publicTargets)
	}
}

func TestRecoverRepairRequiredMainRejectsUnknownProxyType(t *testing.T) {
	fixture := newFixture()
	fixture.store.mainEgress = store.MainEgress{
		ResourceName: "agw-main", CountryCode: "US", ProxyType: domain.ProxyTypeDatacenter,
		CandidateID: "expired-main", ExitIP: "203.0.113.10", PublicInboundID: 7, MixedInboundID: 8,
		PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: fixture.now(),
	}
	fixture.store.protocolModes["agw-main"] = domain.EgressProtocolMode{
		EgressID: "agw-main", ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality,
		State: domain.ProtocolRepairRequired, LastOperationID: "protocol-main-recovery", Version: 3, UpdatedAt: fixture.now(),
	}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "current-main", Country: "JP", ProxyType: "unknown",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}
	fixture.xui.snapshot.Inbounds = []xui.Inbound{{ID: 7, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: 7, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	if codeOf(orchestrator.RecoverProtocolModes(context.Background())) != "repair_required" {
		t.Fatal("unknown proxy type did not keep repair-required state")
	}
	if fixture.store.mainEgress.CandidateID != "expired-main" || fixture.store.protocolModes["agw-main"].State != domain.ProtocolRepairRequired {
		t.Fatalf("unsafe identity persisted: main=%#v protocol=%#v", fixture.store.mainEgress, fixture.store.protocolModes["agw-main"])
	}
}

func protocolFixture(t *testing.T) (*fixture, domain.ProxyGroup) {
	t.Helper()
	fixture := newFixture()
	group, err := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "slot-one")
	if err != nil {
		t.Fatal(err)
	}
	group.Status = domain.ProxyGroupReady
	group.AimiliSlot = 1
	group.PublicPort = 20000
	group.MixedPort = 30000
	group.PublicInboundID = 11
	group.MixedInboundID = 12
	group.ExitIP = "203.0.113.7"
	group.RealityPublicKey = "public-key"
	group.RealityShortID = "short-id"
	group.RealityServerName = "proxy.example.test"
	group.ConfigFingerprint = "configuration-fingerprint"
	group.Version = 1
	group.CreatedAt = fixture.now()
	group.UpdatedAt = fixture.now()
	fixture.store.groups[group.ID] = group
	fixture.store.protocolModes[group.ID] = domain.EgressProtocolMode{EgressID: group.ID, ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 1, UpdatedAt: fixture.now()}
	fixture.aimili.createdSlots = map[int]aimili.Slot{1: {Number: 1, NodeID: "slot-one", Country: "JP", ProxyType: "datacenter", Port: 17929, Status: "up", ExitIP: group.ExitIP, EgressOK: true}}
	return fixture, group
}

type fakeProtocolTransaction struct {
	calls           *[]string
	actions         []string
	applied         protocoltxn.Request
	applyError      error
	renewError      error
	rollbackError   error
	rollbackResult  protocoltxn.Result
	applyEntered    chan struct{}
	applyRelease    chan struct{}
	finalizeEntered chan struct{}
	finalizeRelease chan struct{}
	rollbackEntered chan struct{}
	rollbackRelease chan struct{}
}

func (f *fakeProtocolTransaction) Apply(_ context.Context, request protocoltxn.Request) (protocoltxn.Result, error) {
	*f.calls = append(*f.calls, "protocol.apply")
	f.actions = append(f.actions, "apply")
	f.applied = request
	if f.applyEntered != nil {
		close(f.applyEntered)
		<-f.applyRelease
	}
	return protocoltxn.Result{OperationID: request.OperationID, Status: "applied"}, f.applyError
}
func (f *fakeProtocolTransaction) Renew(_ context.Context, operationID string) (protocoltxn.Result, error) {
	*f.calls = append(*f.calls, "protocol.renew")
	f.actions = append(f.actions, "renew")
	return protocoltxn.Result{OperationID: operationID, Status: "renewed"}, f.renewError
}
func (f *fakeProtocolTransaction) Finalize(ctx context.Context, operationID string) (protocoltxn.Result, error) {
	*f.calls = append(*f.calls, "protocol.finalize")
	f.actions = append(f.actions, "finalize")
	if f.finalizeEntered != nil {
		close(f.finalizeEntered)
		select {
		case <-ctx.Done():
			return protocoltxn.Result{}, ctx.Err()
		case <-f.finalizeRelease:
		}
	}
	return protocoltxn.Result{OperationID: operationID, Status: "finalized"}, nil
}

type blockingProtocolValidator struct {
	delegate *fakeValidator
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (v *blockingProtocolValidator) ValidateSOCKS5H(ctx context.Context, target validator.SOCKSTarget) (validator.Result, error) {
	return v.delegate.ValidateSOCKS5H(ctx, target)
}

func (v *blockingProtocolValidator) ValidateVLESS(ctx context.Context, target validator.VLESSTarget) (validator.Result, error) {
	return v.delegate.ValidateVLESS(ctx, target)
}

func (v *blockingProtocolValidator) ValidatePublic(ctx context.Context, target validator.PublicTarget) (validator.Result, error) {
	blocked := false
	v.once.Do(func() {
		blocked = true
		close(v.entered)
	})
	if blocked {
		select {
		case <-ctx.Done():
			return validator.Result{}, ctx.Err()
		case <-v.release:
		}
	}
	return v.delegate.ValidatePublic(ctx, target)
}
func (f *fakeProtocolTransaction) Rollback(ctx context.Context, operationID string) (protocoltxn.Result, error) {
	*f.calls = append(*f.calls, "protocol.rollback")
	f.actions = append(f.actions, "rollback")
	if f.rollbackEntered != nil {
		close(f.rollbackEntered)
		select {
		case <-ctx.Done():
			return protocoltxn.Result{}, ctx.Err()
		case <-f.rollbackRelease:
		}
	}
	if f.rollbackResult.OperationID != "" {
		return f.rollbackResult, f.rollbackError
	}
	return protocoltxn.Result{OperationID: operationID, Status: "rolled_back"}, f.rollbackError
}

func orderedSubset(values, expected []string) bool {
	position := 0
	for _, value := range values {
		if position < len(expected) && value == expected[position] {
			position++
		}
	}
	return position == len(expected)
}
