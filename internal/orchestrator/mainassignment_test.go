package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

func TestReplaceCandidateCanCommitMainAfterEndToEndValidation(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{{
		ID: "new-main", CountryCode: "JP", CountryName: "日本",
		ProxyType: "datacenter", ProbeStatus: "available",
	}}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "old-main", Country: "US", CountryName: "United States",
		ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928,
		EgressOK: true, Active: true,
	}
	fixture.aimili.stagedMainStatus = fixture.aimili.mainStatus
	fixture.aimili.stagedMainStatus.CandidateID = "new-main"
	fixture.aimili.stagedMainStatus.Country = "JP"
	fixture.aimili.stagedMainStatus.CountryName = "日本"
	fixture.aimili.stagedMainStatus.ExitIP = "203.0.113.20"

	group, err := fixture.orchestrator(t).ReplaceCandidate(
		context.Background(), "new-main", "agw-main",
	)
	if err != nil {
		t.Fatal(err)
	}
	if group.ID != "agw-main" || group.CandidateID != "new-main" || group.ExitIP != "203.0.113.20" || group.EgressSource != domain.EgressSourceMain {
		t.Fatalf("main group = %#v", group)
	}
	want := []string{"main.stage", "validate.socks", "validate.vless", "main.commit"}
	if !equalStrings(fixture.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fixture.calls, want)
	}
	if fixture.store.mainEgress.CandidateID != "new-main" {
		t.Fatalf("stored main = %#v", fixture.store.mainEgress)
	}
}

func TestReplaceCandidateRollsBackMainWhenPublicValidationFails(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{{
		ID: "new-main", CountryCode: "JP", CountryName: "日本",
		ProxyType: "datacenter", ProbeStatus: "available",
	}}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "old-main", Country: "US", CountryName: "United States",
		ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928,
		EgressOK: true, Active: true,
	}
	fixture.aimili.stagedMainStatus = fixture.aimili.mainStatus
	fixture.aimili.stagedMainStatus.CandidateID = "new-main"
	fixture.aimili.stagedMainStatus.Country = "JP"
	fixture.aimili.stagedMainStatus.ExitIP = "203.0.113.20"
	fixture.validator.vlessErrors = []error{&validator.Error{Code: "config_invalid"}}

	_, err := fixture.orchestrator(t).ReplaceCandidate(
		context.Background(), "new-main", "agw-main",
	)
	if codeOf(err) != "config_invalid" {
		t.Fatalf("error = %v", err)
	}
	if !contains(fixture.calls, "main.rollback") || contains(fixture.calls, "main.commit") {
		t.Fatalf("calls = %#v", fixture.calls)
	}
	if fixture.store.mainEgress.CandidateID != "old-main" || fixture.store.mainEgress.ExitIP != "203.0.113.10" {
		t.Fatalf("old main was not restored: %#v", fixture.store.mainEgress)
	}
}

func TestReplaceCandidateMarksMainRepairRequiredWhenRollbackFails(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{{
		ID: "new-main", CountryCode: "JP", CountryName: "日本",
		ProxyType: "datacenter", ProbeStatus: "available",
	}}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "old-main", Country: "US", CountryName: "United States",
		ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928,
		EgressOK: true, Active: true,
	}
	fixture.aimili.stagedMainStatus = fixture.aimili.mainStatus
	fixture.aimili.stagedMainStatus.CandidateID = "new-main"
	fixture.aimili.stagedMainStatus.ExitIP = "203.0.113.20"
	fixture.validator.vlessError = &validator.Error{Code: "config_invalid"}
	fixture.aimili.mainRollbackError = errors.New("rollback failed")

	_, err := fixture.orchestrator(t).ReplaceCandidate(
		context.Background(), "new-main", "agw-main",
	)
	if codeOf(err) != "repair_required" {
		t.Fatalf("error = %v", err)
	}
}
