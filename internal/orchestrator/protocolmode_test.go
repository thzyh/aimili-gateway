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

func TestSwitchProtocolModeValidatesHysteriaWithInboundTLSIdentity(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.xui.profileSequences = [][]xui.PublicProfile{{{
		InboundID: group.PublicInboundID, Mode: domain.ProtocolHysteria2QUICTLS,
		Auth: "hysteria-auth", ServerName: "tls.example.test",
	}}}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	result, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolHysteria2QUICTLS)
	if err != nil {
		t.Fatal(err)
	}
	if result.ActiveMode != domain.ProtocolHysteria2QUICTLS || len(fixture.validator.publicTargets) != 1 {
		t.Fatalf("result=%#v publicTargets=%#v", result, fixture.validator.publicTargets)
	}
	target := fixture.validator.publicTargets[0]
	if target.TLSServerName != "tls.example.test" || target.InboundAddress != "127.0.0.1:20000" {
		t.Fatalf("Hysteria validation target conflated endpoint and TLS identity: %#v", target)
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

func TestSwitchProtocolAliasDriftRollsBackWithoutAliasWrite(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.xui.verifySubscriptionErr = &xui.AdapterError{Code: "subscription_incomplete"}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if codeOf(err) != "repair_required" || fixture.xui.ensureSubscriptionCalls != 0 || fixture.xui.verifySubscriptionCalls != 2 {
		t.Fatalf("error=%v writes=%d reads=%d calls=%#v", err, fixture.xui.ensureSubscriptionCalls, fixture.xui.verifySubscriptionCalls, fixture.calls)
	}
	if !contains(fixture.calls, "protocol.rollback") || contains(fixture.calls, "protocol.finalize") {
		t.Fatalf("protocol transaction accepted alias drift: %#v", fixture.calls)
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

func TestSwitchProtocolModeRecordsOldPathValidationFailureAfterSuccessfulRollback(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.validator.publicErrors = []error{&validator.Error{Code: "protocol_failed"}, &validator.Error{Code: "protocol_failed"}}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
	state := fixture.store.protocolModes[group.ID]
	if state.State != domain.ProtocolRepairRequired || state.ActiveMode != domain.ProtocolVLESSXHTTPReality || state.LastErrorCode != "protocol_rollback_validation_failed" {
		t.Fatalf("repair state = %#v", state)
	}
}

func TestSwitchProtocolModeRevalidatesRepairRequiredOldPathBeforeAcceptingNewSwitch(t *testing.T) {
	fixture, group := protocolFixture(t)
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRepairRequired
	state.LastErrorCode = "protocol_rollback_validation_failed"
	fixture.store.protocolModes[group.ID] = state
	fixture.xui.profileSequences = [][]xui.PublicProfile{
		{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/old-path"}},
		{{InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSTCPRealityVision, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test"}},
	}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	result, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)

	if err != nil || result.State != domain.ProtocolReady || result.ActiveMode != domain.ProtocolVLESSTCPRealityVision || result.LastErrorCode != "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !orderedSubset(fixture.calls, []string{"validate.socks", "validate.public", "protocol.apply", "protocol.finalize"}) {
		t.Fatalf("repair revalidation did not precede the new switch: %#v", fixture.calls)
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
	fixture.xui.profileSequences = [][]xui.PublicProfile{
		{{InboundID: 7, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path"}},
		{{InboundID: 7, Mode: domain.ProtocolVLESSTCPRealityVision, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test"}},
	}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	result, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != domain.ProtocolReady || fixture.validator.socksCalls != 3 || len(fixture.validator.publicTargets) != 2 {
		t.Fatalf("main path was not fully checked: result=%#v calls=%#v", result, fixture.calls)
	}
	if client.applied.InboundTag != "aimili-reality" || client.applied.Port != 8443 {
		t.Fatalf("main transaction target = %#v", client.applied)
	}
	if !orderedSubset(fixture.calls, []string{"main.lease.acquire", "protocol.apply", "protocol.finalize", "main.lease.release"}) {
		t.Fatalf("main mutation lease did not cover transaction: %#v", fixture.calls)
	}
}

func TestSwitchMainProtocolSynchronizesHealthyRuntimeIdentityBeforeStrictPreflight(t *testing.T) {
	fixture := mainProtocolFixture()
	fixture.store.mainEgress.CandidateID = "stale-main"
	fixture.store.mainEgress.CountryCode = "US"
	fixture.store.mainEgress.CountryName = "美国"
	fixture.store.mainEgress.ExitIP = "203.0.113.10"
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "current-main", Country: "JP", CountryName: "日本", ProxyType: "residential",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	result, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
	if err != nil {
		t.Fatal(err)
	}
	main := fixture.store.mainEgress
	if result.State != domain.ProtocolReady || main.CandidateID != "current-main" || main.CountryCode != "JP" || main.CountryName != "日本" || main.ProxyType != domain.ProxyTypeResidential || main.ExitIP != "203.0.113.20" {
		t.Fatalf("runtime main identity was not synchronized: result=%#v main=%#v", result, main)
	}
	if len(client.actions) == 0 || !orderedSubset(fixture.calls, []string{"main.lease.acquire", "validate.socks", "validate.public", "validate.socks", "protocol.apply"}) {
		t.Fatalf("strict preflight did not run after synchronization: actions=%#v calls=%#v", client.actions, fixture.calls)
	}
	for _, expectedIP := range fixture.validator.socksExpectedIPs {
		if expectedIP != "203.0.113.20" {
			t.Fatalf("mixed validation used stale exit IP: %#v", fixture.validator.socksExpectedIPs)
		}
	}
}

func TestSwitchMainProtocolRejectsUnsafeSynchronizationBeforeHelperWrites(t *testing.T) {
	tests := []struct {
		name      string
		wantCode  string
		configure func(*fixture)
	}{
		{
			name: "main exit unavailable", wantCode: "not_ready",
			configure: func(fixture *fixture) {
				fixture.aimili.mainStatus.EgressOK = false
			},
		},
		{
			name: "mixed validation failed", wantCode: "config_invalid",
			configure: func(fixture *fixture) {
				fixture.validator.socksErrors = []error{&validator.Error{Code: "config_invalid"}}
			},
		},
		{
			name: "current public protocol failed", wantCode: "config_invalid",
			configure: func(fixture *fixture) {
				fixture.validator.publicErrors = []error{&validator.Error{Code: "config_invalid"}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := mainProtocolFixture()
			test.configure(fixture)
			before := fixture.store.protocolModes["agw-main"]
			beforeMain := fixture.store.mainEgress
			client := &fakeProtocolTransaction{calls: &fixture.calls}
			orchestrator := fixture.orchestratorWithMax(t, 3)
			orchestrator.protocolTransaction = client

			_, err := orchestrator.SwitchProtocolMode(context.Background(), "agw-main", domain.ProtocolVLESSTCPRealityVision)
			if codeOf(err) != test.wantCode {
				t.Fatalf("error=%v, want code=%s", err, test.wantCode)
			}
			if len(client.actions) != 0 || fixture.store.protocolModes["agw-main"] != before || fixture.store.mainEgress != beforeMain {
				t.Fatalf("unsafe synchronization mutated helper, protocol state, or main identity: actions=%#v state=%#v main=%#v calls=%#v", client.actions, fixture.store.protocolModes["agw-main"], fixture.store.mainEgress, fixture.calls)
			}
		})
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
	validator := &blockingProtocolValidator{delegate: fixture.validator, entered: entered, release: release, blockOnCall: 2}
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
	fixture.validator.publicErrors = []error{nil, &validator.Error{Code: "protocol_failed"}}
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
	fixture.xui.profileSequences = [][]xui.PublicProfile{
		{{InboundID: 7, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path"}},
		{{InboundID: 7, Mode: domain.ProtocolVLESSTCPRealityVision, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test"}},
		{{InboundID: 7, Mode: domain.ProtocolVLESSXHTTPReality, ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path"}},
	}
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

func TestRecoverProtocolModesConvergesFinalizedTargetAfterGatewayCrash(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: group.PublicInboundID, Mode: domain.ProtocolVLESSTCPRealityVision,
		ClientID: "client-id", PublicKey: "public-key", ShortID: "short-id", ServerName: "proxy.example.test",
	}}
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolSubscriptionPending
	state.DesiredMode = domain.ProtocolVLESSTCPRealityVision
	state.LastOperationID = "protocol-finalized-before-store"
	state.LastRequestHash = "6af401c9f2d5ea78d750f2fe88f14e71946854e4333f26915c8cd26d1383ea1f"
	fixture.store.protocolModes[group.ID] = state
	client := &fakeProtocolTransaction{
		calls: &fixture.calls,
		rollbackResult: protocoltxn.Result{
			OperationID: state.LastOperationID, Status: "failed", ErrorCode: "operation_finalized",
		},
	}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	if err := orchestrator.RecoverProtocolModes(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := fixture.store.protocolModes[group.ID]
	if recovered.State != domain.ProtocolReady || recovered.ActiveMode != domain.ProtocolVLESSTCPRealityVision || recovered.DesiredMode != recovered.ActiveMode || recovered.LastErrorCode != "" {
		t.Fatalf("recovered state = %#v", recovered)
	}
	if !orderedSubset(fixture.calls, []string{"protocol.rollback", "slot.check", "validate.socks", "validate.public"}) {
		t.Fatalf("finalized target was not validated: %#v", fixture.calls)
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

func TestRecoverProtocolModesRevalidatesDegradedRepairSlotWithoutHelperWrites(t *testing.T) {
	fixture, group := protocolFixture(t)
	group.Status = domain.ProxyGroupDegraded
	fixture.store.groups[group.ID] = group
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRepairRequired
	state.LastErrorCode = "protocol_rollback_validation_failed"
	fixture.store.protocolModes[group.ID] = state
	fixture.aimili.createdSlots[group.AimiliSlot] = aimili.Slot{
		Number: group.AimiliSlot, NodeID: "runtime-recovered", CandidateIP: "198.51.100.44",
		Country: "KR", CountryName: "韩国", ProxyType: "residential", ExitIP: "203.0.113.44",
		Port: 17929, Status: "up", EgressOK: true, CheckedAt: 1_700_000_011,
	}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: group.PublicInboundID, Mode: state.ActiveMode, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	if err := orchestrator.RecoverProtocolModes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recovered := fixture.store.protocolModes[group.ID]; recovered.State != domain.ProtocolReady || recovered.LastErrorCode != "" {
		t.Fatalf("recovered protocol = %#v", recovered)
	}
	if recovered := fixture.store.groups[group.ID]; recovered.Status != domain.ProxyGroupReady || recovered.CandidateID != "runtime-recovered" || recovered.ExitIP != "203.0.113.44" {
		t.Fatalf("recovered slot = %#v", recovered)
	}
	if contains(fixture.calls, "protocol.rollback") || contains(fixture.calls, "protocol.apply") {
		t.Fatalf("repair recovery ran helper writes: %#v", fixture.calls)
	}
}

func TestRecoverProtocolModesRevalidatesReadyRepairSlotWithoutHelperWrites(t *testing.T) {
	fixture, group := protocolFixture(t)
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRepairRequired
	state.LastErrorCode = "protocol_rollback_validation_failed"
	fixture.store.protocolModes[group.ID] = state
	fixture.aimili.createdSlots[group.AimiliSlot] = aimili.Slot{
		Number: group.AimiliSlot, NodeID: "runtime-recovered", CandidateIP: "198.51.100.44",
		Country: "KR", CountryName: "韩国", ProxyType: "residential", ExitIP: "203.0.113.44",
		Port: 17929, Status: "up", EgressOK: true, CheckedAt: 1_700_000_011,
	}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: group.PublicInboundID, Mode: state.ActiveMode, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	if err := orchestrator.RecoverProtocolModes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recovered := fixture.store.protocolModes[group.ID]; recovered.State != domain.ProtocolReady || recovered.LastErrorCode != "" {
		t.Fatalf("recovered protocol = %#v", recovered)
	}
	if recovered := fixture.store.groups[group.ID]; recovered.Status != domain.ProxyGroupReady || recovered.CandidateID != "runtime-recovered" || recovered.ExitIP != "203.0.113.44" {
		t.Fatalf("recovered slot = %#v", recovered)
	}
	if contains(fixture.calls, "protocol.rollback") || contains(fixture.calls, "protocol.apply") {
		t.Fatalf("repair recovery ran helper writes: %#v", fixture.calls)
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
	if !orderedSubset(fixture.calls, []string{"validate.socks", "validate.public"}) || contains(fixture.calls, "protocol.rollback") || fixture.xui.ensureLegacyMainCalls != 0 {
		t.Fatalf("recovery calls = %#v legacy=%d", fixture.calls, fixture.xui.ensureLegacyMainCalls)
	}
	if contains(fixture.calls, "main.lease.acquire") || contains(fixture.calls, "main.lease.release") {
		t.Fatalf("read-only main repair recovery acquired a mutation lease: %#v", fixture.calls)
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

func TestSwitchProtocolModeRevalidatesDegradedRepairSlotAndRebindsVerifiedIdentity(t *testing.T) {
	fixture, group := protocolFixture(t)
	group.Status = domain.ProxyGroupDegraded
	group.LastErrorCode = "egress_unavailable"
	fixture.store.groups[group.ID] = group
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRepairRequired
	state.LastErrorCode = "protocol_rollback_validation_failed"
	fixture.store.protocolModes[group.ID] = state
	fixture.aimili.createdSlots[group.AimiliSlot] = aimili.Slot{
		Number: group.AimiliSlot, NodeID: "runtime-recovered", CandidateIP: "198.51.100.44",
		Country: "KR", CountryName: "韩国", ProxyType: "residential", ExitIP: "203.0.113.44",
		Port: 17929, Status: "up", EgressOK: true, CheckedAt: 1_700_000_011,
	}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: group.PublicInboundID, Mode: state.ActiveMode, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	result, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, state.ActiveMode)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != domain.ProtocolReady || result.ActiveMode != state.ActiveMode || result.LastErrorCode != "" {
		t.Fatalf("revalidated protocol = %#v", result)
	}
	recovered := fixture.store.groups[group.ID]
	if recovered.Status != domain.ProxyGroupReady || recovered.CandidateID != "runtime-recovered" ||
		recovered.CountryCode != "KR" || recovered.ProxyType != domain.ProxyTypeResidential ||
		recovered.ExitIP != "203.0.113.44" || recovered.ExitIPCheckedAt != 1_700_000_011 {
		t.Fatalf("recovered slot identity = %#v", recovered)
	}
	if len(fixture.validator.publicTargets) != 1 || fixture.validator.publicTargets[0].ExpectedExitIP != "203.0.113.44" ||
		len(fixture.calls) == 0 || contains(fixture.calls, "protocol.apply") {
		t.Fatalf("unsafe repair path calls=%#v public=%#v", fixture.calls, fixture.validator.publicTargets)
	}
}

func TestSwitchProtocolModeKeepsDegradedRepairSlotLockedWhenSlotIdentityChangesDuringValidation(t *testing.T) {
	fixture, group := protocolFixture(t)
	group.Status = domain.ProxyGroupDegraded
	group.LastErrorCode = "egress_unavailable"
	fixture.store.groups[group.ID] = group
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRepairRequired
	state.LastErrorCode = "protocol_rollback_validation_failed"
	fixture.store.protocolModes[group.ID] = state
	fixture.aimili.checkResults = []aimili.SlotCheck{
		{NodeID: "runtime-recovered", CandidateIP: "198.51.100.44", Country: "KR", CountryName: "韩国", ProxyType: "residential", ExitIP: "203.0.113.44", Port: 17929, Status: "up", EgressOK: true},
		{NodeID: "unexpected-node", CandidateIP: "198.51.100.45", Country: "KR", CountryName: "韩国", ProxyType: "residential", ExitIP: "203.0.113.45", Port: 17929, Status: "up", EgressOK: true},
	}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: group.PublicInboundID, Mode: state.ActiveMode, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, state.ActiveMode)
	if codeOf(err) != "egress_unavailable" {
		t.Fatalf("error = %v", err)
	}
	if locked := fixture.store.protocolModes[group.ID]; locked.State != domain.ProtocolRepairRequired || locked.LastErrorCode != "egress_unavailable" {
		t.Fatalf("protocol lock was cleared: %#v", locked)
	}
	if stored := fixture.store.groups[group.ID]; stored.CandidateID != group.CandidateID || stored.Status != domain.ProxyGroupDegraded {
		t.Fatalf("unverified slot identity was persisted: %#v", stored)
	}
}

func TestSwitchProtocolModeRecordsTheSafeRepairValidationFailureCode(t *testing.T) {
	fixture, group := protocolFixture(t)
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRepairRequired
	fixture.store.protocolModes[group.ID] = state
	fixture.validator.publicErrors = []error{&validator.Error{Code: "protocol_failed"}}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: group.PublicInboundID, Mode: state.ActiveMode, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, state.ActiveMode)
	if errorCode(err) != "protocol_failed" {
		t.Fatalf("repair validation error=%v code=%s", err, errorCode(err))
	}
	if locked := fixture.store.protocolModes[group.ID]; locked.State != domain.ProtocolRepairRequired || locked.LastErrorCode != "protocol_failed" {
		t.Fatalf("repair failure was not persisted precisely: %#v", locked)
	}
}

func TestSwitchProtocolModeRepairsExclusiveSubscriptionAliasesBeforeUnlockingProtocolRepair(t *testing.T) {
	fixture, group := protocolFixture(t)
	state := fixture.store.protocolModes[group.ID]
	state.State = domain.ProtocolRepairRequired
	fixture.store.protocolModes[group.ID] = state
	fixture.xui.verifySubscriptionErrors = []error{&xui.AdapterError{Code: "subscription_incomplete"}, nil}
	fixture.xui.subscriptionProfiles = []xui.PublicProfile{{
		InboundID: group.PublicInboundID, Mode: state.ActiveMode, ClientID: "client-id", PublicKey: "public-key",
		ShortID: "short-id", ServerName: "proxy.example.test", XHTTPPath: "/current-path",
	}}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = &fakeProtocolTransaction{calls: &fixture.calls}

	updated, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, state.ActiveMode)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != domain.ProtocolReady || fixture.xui.repairSubscriptionAliasesCalls != 1 || fixture.xui.verifySubscriptionCalls != 2 || fixture.xui.ensureSubscriptionCalls != 0 {
		t.Fatalf("repair state=%#v aliasRepairs=%d reads=%d ensures=%d", updated, fixture.xui.repairSubscriptionAliasesCalls, fixture.xui.verifySubscriptionCalls, fixture.xui.ensureSubscriptionCalls)
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
	delegate    *fakeValidator
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	call        int
	blockOnCall int
}

func (v *blockingProtocolValidator) ValidateSOCKS5H(ctx context.Context, target validator.SOCKSTarget) (validator.Result, error) {
	return v.delegate.ValidateSOCKS5H(ctx, target)
}

func (v *blockingProtocolValidator) ValidateVLESS(ctx context.Context, target validator.VLESSTarget) (validator.Result, error) {
	return v.delegate.ValidateVLESS(ctx, target)
}

func (v *blockingProtocolValidator) ValidatePublic(ctx context.Context, target validator.PublicTarget) (validator.Result, error) {
	v.call++
	blockOnCall := v.blockOnCall
	if blockOnCall == 0 {
		blockOnCall = 1
	}
	blocked := false
	if v.call == blockOnCall {
		v.once.Do(func() {
			blocked = true
			close(v.entered)
		})
	}
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
