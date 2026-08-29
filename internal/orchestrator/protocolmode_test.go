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
	want := []string{"protocol.apply", "slot.check", "validate.socks", "validate.vless", "protocol.finalize"}
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

func TestSwitchProtocolModeRollsBackAndRevalidatesOldPath(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.validator.vlessErrors = []error{&validator.Error{Code: "protocol_failed"}, nil}
	client := &fakeProtocolTransaction{calls: &fixture.calls}
	orchestrator := fixture.orchestratorWithMax(t, 3)
	orchestrator.protocolTransaction = client

	_, err := orchestrator.SwitchProtocolMode(context.Background(), group.ID, domain.ProtocolVLESSTCPRealityVision)
	if codeOf(err) != "protocol_failed" {
		t.Fatalf("error = %v", err)
	}
	want := []string{"protocol.apply", "validate.vless", "protocol.rollback", "validate.vless"}
	if !orderedSubset(fixture.calls, want) || contains(fixture.calls, "protocol.finalize") {
		t.Fatalf("rollback calls = %#v", fixture.calls)
	}
	state := fixture.store.protocolModes[group.ID]
	if state.ActiveMode != domain.ProtocolVLESSXHTTPReality || state.DesiredMode != state.ActiveMode || state.State != domain.ProtocolReady || state.LastErrorCode != "protocol_failed" {
		t.Fatalf("rolled back state = %#v", state)
	}
}

func TestSwitchProtocolModeMarksRepairWhenRollbackOrOldVerificationFails(t *testing.T) {
	fixture, group := protocolFixture(t)
	fixture.validator.vlessErrors = []error{&validator.Error{Code: "protocol_failed"}, &validator.Error{Code: "protocol_failed"}}
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
	if result.State != domain.ProtocolReady || fixture.validator.socksCalls != 1 || fixture.validator.vlessCalls != 1 {
		t.Fatalf("main path was not fully checked: result=%#v calls=%#v", result, fixture.calls)
	}
	if client.applied.InboundTag != "aimili-reality" || client.applied.Port != 8443 {
		t.Fatalf("main transaction target = %#v", client.applied)
	}
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
	fixture.aimili.createdSlots = map[int]aimili.Slot{1: {Number: 1, NodeID: "slot-one", Country: "JP", ProxyType: "datacenter", ExitIP: group.ExitIP, EgressOK: true}}
	return fixture, group
}

type fakeProtocolTransaction struct {
	calls         *[]string
	actions       []string
	applied       protocoltxn.Request
	applyError    error
	rollbackError error
	applyEntered  chan struct{}
	applyRelease  chan struct{}
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
func (f *fakeProtocolTransaction) Finalize(_ context.Context, operationID string) (protocoltxn.Result, error) {
	*f.calls = append(*f.calls, "protocol.finalize")
	f.actions = append(f.actions, "finalize")
	return protocoltxn.Result{OperationID: operationID, Status: "finalized"}, nil
}
func (f *fakeProtocolTransaction) Rollback(_ context.Context, operationID string) (protocoltxn.Result, error) {
	*f.calls = append(*f.calls, "protocol.rollback")
	f.actions = append(f.actions, "rollback")
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
