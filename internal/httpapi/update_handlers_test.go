package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

func TestUpdateGETRequiresAuthenticatedAdmin(t *testing.T) {
	manager := &fakeUpdateManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Updates = manager })
	response := environment.request(t, http.MethodGet, "/api/v1/system/updates", nil, "", "")
	assertResponseStatus(t, response, http.StatusUnauthorized)
}

func TestUpdatePOSTRequiresCSRFAndAuthenticatedSession(t *testing.T) {
	manager := &fakeUpdateManager{summary: updateSummaryFixture()}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	session := environment.session(t)
	path := "/api/v1/system/updates/gateway/v1.2.3/apply"
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]string{"runId": strings.Repeat("a", 64)}, environment.origin, ""), http.StatusForbidden)
	response := environment.request(t, http.MethodPost, path, map[string]string{"runId": strings.Repeat("a", 64)}, environment.origin, session.CSRFToken)
	assertResponseStatus(t, response, http.StatusAccepted)
}

func TestUpdateApplyRejectsURLPathVersions(t *testing.T) {
	manager := &fakeUpdateManager{summary: updateSummaryFixture()}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	session := environment.session(t)
	for _, version := range []string{"https:%2F%2Fevil.test", "..%2Fv1.2.3"} {
		response := environment.request(t, http.MethodPost, "/api/v1/system/updates/gateway/"+version+"/apply", map[string]string{"runId": strings.Repeat("a", 64)}, environment.origin, session.CSRFToken)
		assertResponseStatus(t, response, http.StatusBadRequest)
	}
}

func TestUpdateStatusRedactsInternalFields(t *testing.T) {
	manager := &fakeUpdateManager{result: UpdateResult{RunID: strings.Repeat("a", 64), Kind: "gateway", Version: "v1.2.3", State: "failed", ErrorCode: "download_failed"}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/system/updates/"+strings.Repeat("a", 64), nil, "", "")
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || strings.Contains(string(body), "origin") || strings.Contains(string(body), "path") || strings.Contains(string(body), "detail") {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
}

func TestUpdateApplyIsIdempotentByRunID(t *testing.T) {
	manager := &fakeUpdateManager{summary: updateSummaryFixture()}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	session := environment.session(t)
	body := map[string]string{"runId": strings.Repeat("a", 64)}
	path := "/api/v1/system/updates/gateway/v1.2.3/apply"
	for range 2 {
		response := environment.request(t, http.MethodPost, path, body, environment.origin, session.CSRFToken)
		assertResponseStatus(t, response, http.StatusAccepted)
	}
	if manager.submitCalls != 2 || manager.lastRequest.RunID != strings.Repeat("a", 64) {
		t.Fatalf("submit calls=%d request=%#v", manager.submitCalls, manager.lastRequest)
	}
}

func TestRollbackRequiresAvailableCapabilityBeforeSubmit(t *testing.T) {
	manager := &fakeUpdateManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(deps *Dependencies) { deps.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	session := environment.session(t)
	response := environment.request(t, http.MethodPost, "/api/v1/system/updates/ui/rollback", map[string]string{"runId": strings.Repeat("a", 64)}, environment.origin, session.CSRFToken)
	assertResponseStatus(t, response, http.StatusServiceUnavailable)
	if manager.submitCalls != 0 {
		t.Fatal("disabled rollback wrote a request")
	}
}

func TestUpdateMutationAuditUsesClosedRedactedFields(t *testing.T) {
	manager := &fakeUpdateManager{summary: updateSummaryFixture()}
	environment := newAuthTestEnvironmentConfigured(t, true, func(d *Dependencies) { d.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	session := environment.session(t)
	runID := strings.Repeat("a", 64)
	for _, body := range []map[string]string{{}, {"runId": runID}} {
		response := environment.request(t, http.MethodPost, "/api/v1/system/updates/gateway/v1.2.3/apply", body, environment.origin, session.CSRFToken)
		response.Body.Close()
	}
	db, err := sql.Open("sqlite", environment.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT action,resource_type,resource_fingerprint,result,error_code FROM audit_events WHERE resource_type='gateway_update' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var action, kind, fingerprint, result, code string
		if err := rows.Scan(&action, &kind, &fingerprint, &result, &code); err != nil {
			t.Fatal(err)
		}
		if action != "update.apply.gateway.v1.2.3" || len(fingerprint) != 64 || fingerprint == runID || (result != "denied" && result != "success") || (code != "request_rejected" && code != "") {
			t.Fatalf("audit outside closed fields: %q %q %q %q %q", action, kind, fingerprint, result, code)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("missing accepted/rejected update audits: %d", count)
	}
}

func TestUIRollbackAPIHasDedicatedRolledBackResult(t *testing.T) {
	runID := strings.Repeat("b", 64)
	manager := &fakeUpdateManager{summary: updateSummaryFixture(), result: UpdateResult{RunID: runID, Kind: "ui", State: "rolled_back"}}
	env := newAuthTestEnvironmentConfigured(t, true, func(d *Dependencies) { d.Updates = manager })
	assertResponseStatus(t, env.login(t), http.StatusNoContent)
	session := env.session(t)
	response := env.request(t, http.MethodPost, "/api/v1/system/updates/ui/rollback", map[string]string{"runId": runID}, env.origin, session.CSRFToken)
	assertResponseStatus(t, response, http.StatusAccepted)
	if manager.lastRequest.Kind != "ui" || manager.lastRequest.Action != "rollback" || manager.lastRequest.Version != "" {
		t.Fatalf("UI rollback misrouted: %+v", manager.lastRequest)
	}
	result := decodeUpdateResult(t, env.request(t, http.MethodGet, "/api/v1/system/updates/"+runID, nil, "", ""))
	if result.State != "rolled_back" || result.Kind != "ui" {
		t.Fatalf("rollback state lost: %+v", result)
	}
}

func TestDisabledAPIRejectsAllRoutesWithoutSpool(t *testing.T) {
	env := newAuthTestEnvironment(t)
	assertResponseStatus(t, env.login(t), http.StatusNoContent)
	session := env.session(t)
	for _, route := range []struct{ method, path string }{{http.MethodGet, "/api/v1/system/updates"}, {http.MethodGet, "/api/v1/system/updates/" + strings.Repeat("a", 64)}, {http.MethodPost, "/api/v1/system/updates/ui/rollback"}, {http.MethodPost, "/api/v1/system/updates/gateway/rollback"}, {http.MethodPost, "/api/v1/system/updates/gateway/v1.2.3/apply"}} {
		response := env.request(t, route.method, route.path, map[string]string{"runId": strings.Repeat("a", 64)}, env.origin, session.CSRFToken)
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 503 || !strings.Contains(string(body), "updates_disabled") {
			t.Fatalf("disabled route %s: %d %s", route.path, response.StatusCode, body)
		}
	}
}

type fakeUpdateManager struct {
	summary     UpdateSummary
	result      UpdateResult
	submitCalls int
	lastRequest UpdateRequest
}

func (m *fakeUpdateManager) List(context.Context) (UpdateSummary, error) { return m.summary, nil }
func (m *fakeUpdateManager) Submit(_ context.Context, request UpdateRequest) (UpdateResult, error) {
	m.submitCalls++
	m.lastRequest = request
	if m.result.RunID != "" {
		return m.result, nil
	}
	return UpdateResult{RunID: request.RunID, Kind: request.Kind, Version: request.Version, State: string(updatetxn.StatePending)}, nil
}
func (m *fakeUpdateManager) Get(context.Context, string) (UpdateResult, error) { return m.result, nil }

func updateSummaryFixture() UpdateSummary {
	return UpdateSummary{
		Enabled:        true,
		CurrentGateway: "v1.2.2",
		Available:      []UpdateVersion{{Kind: "gateway", Version: "v1.2.3", Compatible: true}},
	}
}

func decodeUpdateResult(t *testing.T, response *http.Response) UpdateResult {
	t.Helper()
	defer response.Body.Close()
	var result UpdateResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
