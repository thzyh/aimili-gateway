package domain

import "testing"

func TestProtocolModeIsClosed(t *testing.T) {
	valid := []ProtocolMode{
		ProtocolVLESSTCPRealityVision,
		ProtocolVLESSXHTTPReality,
		ProtocolHysteria2QUICTLS,
	}
	for _, mode := range valid {
		if !mode.Valid() {
			t.Fatalf("mode %q is invalid", mode)
		}
	}
	if ProtocolMode("vless").Valid() || ProtocolMode("").Valid() {
		t.Fatal("open or empty protocol mode was accepted")
	}
}

func TestEgressProtocolStateTransitionsAreClosed(t *testing.T) {
	state := EgressProtocolMode{State: ProtocolReady}
	for _, next := range []ProtocolState{
		ProtocolSwitching,
		ProtocolSubscriptionPending,
		ProtocolRollingBack,
		ProtocolReady,
	} {
		if err := state.Transition(next); err != nil {
			t.Fatalf("transition to %q: %v", next, err)
		}
	}
	if err := state.Transition(ProtocolSubscriptionPending); err == nil {
		t.Fatal("ready transitioned directly to subscription_pending")
	}
	state.State = ProtocolRepairRequired
	if err := state.Transition(ProtocolSwitching); err == nil {
		t.Fatal("repair_required accepted a new switch")
	}
}
