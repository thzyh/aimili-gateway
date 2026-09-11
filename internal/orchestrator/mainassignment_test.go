package orchestrator

import (
	"context"
	"errors"
	"reflect"
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

func TestReplaceCandidateCanRecoverAnOfflineMainWithARestorableAssignment(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{{
		ID: "new-main", CountryCode: "JP", CountryName: "日本",
		ProxyType: "residential", ProbeStatus: "available",
	}}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "old-main", Country: "US", CountryName: "United States",
		ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928,
		EgressOK: false, Active: false,
	}
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "日本",
		ProxyType: "residential", ExitIP: "203.0.113.20", Port: 7928,
		EgressOK: true, Active: true,
	}

	group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "new-main", "agw-main")

	if err != nil {
		t.Fatal(err)
	}
	if group.CandidateID != "new-main" || group.ExitIP != "203.0.113.20" || group.Status != domain.ProxyGroupReady {
		t.Fatalf("main group = %#v", group)
	}
	want := []string{"main.stage", "validate.socks", "validate.vless", "main.commit"}
	if !equalStrings(fixture.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fixture.calls, want)
	}
}

func TestReplaceCandidateCanRecreateAMissingMain(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.candidates = []aimili.Candidate{{
		ID: "new-main", CountryCode: "JP", CountryName: "日本",
		ProxyType: "residential", ProbeStatus: "available",
	}}
	fixture.aimili.mainStatus = aimili.MainStatus{Port: 7928}
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "日本",
		ProxyType: "residential", ExitIP: "203.0.113.20", Port: 7928,
		EgressOK: true, Active: true,
	}

	group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "new-main", "agw-main")

	if err != nil {
		t.Fatal(err)
	}
	if group.CandidateID != "new-main" || group.ExitIP != "203.0.113.20" || group.Status != domain.ProxyGroupReady {
		t.Fatalf("main group = %#v", group)
	}
	want := []string{"main.stage", "validate.socks", "validate.vless", "main.commit"}
	if !equalStrings(fixture.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fixture.calls, want)
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

func TestReplaceMainAliasWriteFailureRestoresOldMainAndAliases(t *testing.T) {
	tests := []struct {
		name   string
		errors []error
		drifts []bool
		repair bool
	}{
		{"alias write failure", []error{errors.New("alias write failed"), nil}, nil, false},
		{"subscription fetch failure", []error{errors.New("subscription fetch failed"), nil}, nil, false},
		{"post write mismatch", nil, []bool{true, false}, false},
		{"alias rollback failure", []error{errors.New("alias write failed"), errors.New("alias rollback failed")}, nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, _ := aliasReplacementFixture(t)
			fixture.store.mainEgress.CandidateID = "old-main"
			fixture.aimili.mainStatus = aimili.MainStatus{CandidateID: "old-main", Country: "US", CountryName: "美国", ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928, EgressOK: true, Active: true}
			fixture.aimili.stagedMainStatus = aimili.MainStatus{CandidateID: "new-main", Country: "JP", CountryName: "日本", ProxyType: "datacenter", ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true}
			fixture.aimili.candidates = []aimili.Candidate{{ID: "new-main", CountryCode: "JP", CountryName: "日本", ProxyType: "datacenter", ProbeStatus: "available"}}
			fixture.xui.ensureSubscriptionErrors = test.errors
			fixture.xui.ensureSubscriptionAliasDrifts = test.drifts

			_, err := fixture.orchestratorWithMax(t, 3).ReplaceCandidate(context.Background(), "new-main", "agw-main")
			oldRuntime := aimili.MainStatus{CandidateID: "old-main", Country: "US", CountryName: "United States", ProxyType: "datacenter", ExitIP: "203.0.113.10", Port: 7928, EgressOK: true, Active: true}
			if fixture.aimili.mainStatus != oldRuntime {
				t.Fatalf("AimiliVPN main runtime not restored: %#v", fixture.aimili.mainStatus)
			}
			if len(fixture.xui.updated) != 0 || len(fixture.xui.updateNames) != 0 {
				t.Fatalf("non-target Gateway groups were touched: updated=%#v names=%#v", fixture.xui.updated, fixture.xui.updateNames)
			}
			if test.repair {
				if codeOf(err) != "repair_required" || !contains(fixture.calls, "main.rollback") {
					t.Fatalf("error=%v calls=%#v", err, fixture.calls)
				}
				if fixture.store.mainEgress.CandidateID != "old-main" || fixture.store.mainEgress.ExitIP != "203.0.113.10" {
					t.Fatalf("repair state did not retain restored main: %#v", fixture.store.mainEgress)
				}
				if fixture.aimili.createdSlots[0].NodeID != "old-node" || fixture.aimili.createdSlots[1].NodeID != "us-node" || fixture.aimili.createdSlots[2].NodeID != "kr-node" {
					t.Fatalf("non-target exits changed: %#v", fixture.aimili.createdSlots)
				}
				return
			}
			if err == nil || fixture.store.mainEgress.CandidateID != "old-main" || !contains(fixture.calls, "main.rollback") {
				t.Fatalf("error=%v main=%#v calls=%#v", err, fixture.store.mainEgress, fixture.calls)
			}
			want := map[int64]string{1: "主连接_United States", 2: "出口位 1_日本", 3: "出口位 2_美国", 4: "出口位 3_韩国"}
			if !reflect.DeepEqual(fixture.xui.subscriptionDesired.Aliases, want) || fixture.xui.ensureSubscriptionCalls != 2 {
				t.Fatalf("aliases=%#v writes=%d", fixture.xui.subscriptionDesired.Aliases, fixture.xui.ensureSubscriptionCalls)
			}
			if fixture.aimili.createdSlots[0].NodeID != "old-node" || fixture.aimili.createdSlots[1].NodeID != "us-node" || fixture.aimili.createdSlots[2].NodeID != "kr-node" {
				t.Fatalf("non-target exits changed: %#v", fixture.aimili.createdSlots)
			}
		})
	}
}

func TestRepairCommitRequiresGatewayMixedAndPublicValidationBeforeFinalize(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainAssignment = repairAssignment("new-main", "JP", "datacenter")
	fixture.aimili.mainStatus = aimili.MainStatus{CandidateID: "", Port: 7928}
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "Japan", ProxyType: "datacenter",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}

	group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "new-main", "agw-main")
	if err != nil {
		t.Fatal(err)
	}
	if group.CandidateID != "new-main" || fixture.aimili.mainAssignment.State != "committed" {
		t.Fatalf("group=%#v assignment=%#v", group, fixture.aimili.mainAssignment)
	}
	want := []string{"main.repair-commit", "validate.socks", "validate.vless", "main.commit"}
	if !equalStrings(fixture.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fixture.calls, want)
	}
}

func TestRepairCommitRejectsCheckedCandidateMismatchBeforeFinalize(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainAssignment = repairAssignment("new-main", "JP", "datacenter")
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "different-main", Country: "JP", CountryName: "Japan", ProxyType: "datacenter",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}

	_, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "new-main", "agw-main")

	if codeOf(err) != "conflict" {
		t.Fatalf("error = %v", err)
	}
	if fixture.aimili.mainAssignment.State != "pending_gateway_validation" || contains(fixture.calls, "main.commit") || contains(fixture.calls, "main.rollback") {
		t.Fatalf("assignment=%#v calls=%#v", fixture.aimili.mainAssignment, fixture.calls)
	}
}

func TestRepairReplaceUsesOnlyTheExplicitCandidateThenWaitsForGatewayValidation(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainAssignment = repairAssignment("failed-new", "JP", "datacenter")
	fixture.aimili.candidates = []aimili.Candidate{{
		ID: "third-main", CountryCode: "KR", CountryName: "Korea", ProxyType: "residential", ProbeStatus: "available",
	}}
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "third-main", Country: "KR", CountryName: "Korea", ProxyType: "residential",
		ExitIP: "203.0.113.30", Port: 7928, EgressOK: true, Active: true,
	}

	group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "third-main", "agw-main")
	if err != nil {
		t.Fatal(err)
	}
	if group.CandidateID != "third-main" || fixture.aimili.repairReplaceRequests[0].CandidateID != "third-main" {
		t.Fatalf("group=%#v requests=%#v", group, fixture.aimili.repairReplaceRequests)
	}
	want := []string{"main.repair-replace", "validate.socks", "validate.vless", "main.commit"}
	if !equalStrings(fixture.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fixture.calls, want)
	}
}

func TestRepairReplaceRejectsCheckedNormalizedClassificationMismatchBeforeFinalize(t *testing.T) {
	for _, test := range []struct {
		name      string
		country   string
		proxyType string
	}{
		{name: "country", country: "jp", proxyType: "residential"},
		{name: "proxy type", country: "kr", proxyType: "datacenter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture()
			fixture.aimili.mainAssignment = repairAssignment("failed-new", "JP", "datacenter")
			fixture.aimili.candidates = []aimili.Candidate{{
				ID: "third-main", CountryCode: "KR", CountryName: "Korea", ProxyType: "residential", ProbeStatus: "available",
			}}
			fixture.aimili.stagedMainStatus = aimili.MainStatus{
				CandidateID: "third-main", Country: test.country, CountryName: "Checked", ProxyType: test.proxyType,
				ExitIP: "203.0.113.30", Port: 7928, EgressOK: true, Active: true,
			}

			_, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "third-main", "agw-main")

			if codeOf(err) != "conflict" {
				t.Fatalf("error = %v", err)
			}
			if fixture.aimili.mainAssignment.State != "pending_gateway_validation" || contains(fixture.calls, "main.commit") || contains(fixture.calls, "main.rollback") {
				t.Fatalf("assignment=%#v calls=%#v", fixture.aimili.mainAssignment, fixture.calls)
			}
		})
	}
}

func TestRepairGatewayValidationFailureKeepsTransactionProtectedAndCanResume(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainAssignment = repairAssignment("new-main", "JP", "datacenter")
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "Japan", ProxyType: "datacenter",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}
	fixture.validator.vlessErrors = []error{&validator.Error{Code: "config_invalid"}, nil}
	orchestrator := fixture.orchestrator(t)

	_, firstErr := orchestrator.ReplaceCandidate(context.Background(), "new-main", "agw-main")
	group, secondErr := orchestrator.ReplaceCandidate(context.Background(), "new-main", "agw-main")

	if codeOf(firstErr) != "config_invalid" || secondErr != nil {
		t.Fatalf("first=%v second=%v", firstErr, secondErr)
	}
	if group.CandidateID != "new-main" || fixture.aimili.repairCommitCalls != 1 || fixture.aimili.mainCommitCalls != 1 {
		t.Fatalf("group=%#v repair=%d commits=%d", group, fixture.aimili.repairCommitCalls, fixture.aimili.mainCommitCalls)
	}
	if contains(fixture.calls, "main.rollback") {
		t.Fatalf("repair validation failure released protection: %#v", fixture.calls)
	}
}

func TestRepairReplaceGatewayValidationFailureKeepsTheSameExplicitCandidate(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainAssignment = repairAssignment("failed-new", "JP", "datacenter")
	fixture.aimili.candidates = []aimili.Candidate{{
		ID: "third-main", CountryCode: "KR", CountryName: "Korea", ProxyType: "residential", ProbeStatus: "available",
	}}
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "third-main", Country: "KR", CountryName: "Korea", ProxyType: "residential",
		ExitIP: "203.0.113.30", Port: 7928, EgressOK: true, Active: true,
	}
	fixture.validator.vlessErrors = []error{&validator.Error{Code: "config_invalid"}, nil}
	orchestrator := fixture.orchestrator(t)

	_, firstErr := orchestrator.ReplaceCandidate(context.Background(), "third-main", "agw-main")
	group, secondErr := orchestrator.ReplaceCandidate(context.Background(), "third-main", "agw-main")

	if codeOf(firstErr) != "config_invalid" || secondErr != nil || group.CandidateID != "third-main" {
		t.Fatalf("first=%v second=%v group=%#v", firstErr, secondErr, group)
	}
	if len(fixture.aimili.repairReplaceRequests) != 1 || fixture.aimili.repairReplaceRequests[0].CandidateID != "third-main" || contains(fixture.calls, "main.rollback") {
		t.Fatalf("repair replace was replayed or changed candidate: calls=%#v requests=%#v", fixture.calls, fixture.aimili.repairReplaceRequests)
	}
}

func TestRepairActionsRetryAfterStagingResponseLoss(t *testing.T) {
	for _, test := range []struct {
		name      string
		candidate string
		replace   bool
	}{
		{name: "repair commit", candidate: "new-main"},
		{name: "repair replace", candidate: "third-main", replace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture()
			fixture.aimili.mainAssignment = repairAssignment("new-main", "JP", "datacenter")
			fixture.aimili.stagedMainStatus = aimili.MainStatus{
				CandidateID: test.candidate, Country: "JP", CountryName: "Target", ProxyType: "datacenter",
				ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
			}
			if test.replace {
				fixture.aimili.candidates = []aimili.Candidate{{ID: test.candidate, CountryCode: "JP", CountryName: "Target", ProxyType: "datacenter", ProbeStatus: "available"}}
				fixture.aimili.repairReplaceErrors = []error{errors.New("response lost"), nil}
			} else {
				fixture.aimili.repairCommitErrors = []error{errors.New("response lost"), nil}
			}

			group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), test.candidate, "agw-main")

			if err != nil || group.CandidateID != test.candidate {
				t.Fatalf("group=%#v err=%v", group, err)
			}
			if test.replace && len(fixture.aimili.repairReplaceRequests) != 2 {
				t.Fatalf("repair replace attempts = %d", len(fixture.aimili.repairReplaceRequests))
			}
			if !test.replace && fixture.aimili.repairCommitCalls != 2 {
				t.Fatalf("repair commit attempts = %d", fixture.aimili.repairCommitCalls)
			}
		})
	}
}

func TestRepairFinalizeRetriesAfterResponseLoss(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainAssignment = repairAssignment("new-main", "JP", "datacenter")
	fixture.aimili.stagedMainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "Japan", ProxyType: "datacenter",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}
	fixture.aimili.mainCommitErrors = []error{errors.New("response lost"), nil}

	group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "new-main", "agw-main")

	if err != nil || group.CandidateID != "new-main" || fixture.aimili.mainCommitCalls != 2 {
		t.Fatalf("group=%#v err=%v commits=%d", group, err, fixture.aimili.mainCommitCalls)
	}
}

func TestMainReplacementRecoversWhenFinalizeSucceededBeforeGatewayStore(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "Japan", ProxyType: "datacenter",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}

	group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "new-main", "agw-main")

	if err != nil || group.CandidateID != "new-main" || contains(fixture.calls, "main.stage") {
		t.Fatalf("group=%#v err=%v calls=%#v", group, err, fixture.calls)
	}
}

func TestMainReplacementResumesPendingCommitBeforeReportingSuccess(t *testing.T) {
	fixture := newFixture()
	fixture.aimili.mainAssignment = aimili.MainAssignmentStatus{
		OperationID: "operation-safe-1", State: "pending_commit", OldCandidateID: "old-main",
		NewCandidateID: "new-main", Country: "JP", ProxyType: "datacenter", Port: 7928,
		DNSVerified: true, ExitVerified: true, Available: true,
	}
	fixture.aimili.mainStatus = aimili.MainStatus{
		CandidateID: "new-main", Country: "JP", CountryName: "Japan", ProxyType: "datacenter",
		ExitIP: "203.0.113.20", Port: 7928, EgressOK: true, Active: true,
	}

	group, err := fixture.orchestrator(t).ReplaceCandidate(context.Background(), "new-main", "agw-main")

	if err != nil || group.CandidateID != "new-main" {
		t.Fatalf("group=%#v err=%v", group, err)
	}
	if fixture.aimili.mainCommitCalls != 1 || contains(fixture.calls, "main.stage") || contains(fixture.calls, "main.repair-commit") {
		t.Fatalf("pending commit was not resumed safely: calls=%#v commits=%d", fixture.calls, fixture.aimili.mainCommitCalls)
	}
	if fixture.store.mainEgress.CandidateID != "new-main" || fixture.store.mainEgress.ExitIP != "203.0.113.20" {
		t.Fatalf("stored main = %#v", fixture.store.mainEgress)
	}
}

func repairAssignment(candidateID, country, proxyType string) aimili.MainAssignmentStatus {
	return aimili.MainAssignmentStatus{
		OperationID: "operation-safe-1", State: "repair_required", OldCandidateID: "old-main",
		NewCandidateID: candidateID, Country: country, ProxyType: proxyType, Port: 7928,
	}
}
