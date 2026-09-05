package httpapi

import (
	"context"
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

func TestUpdatePOSTRequiresCSRFAndFreshPassword(t *testing.T) {
	manager := &fakeUpdateManager{summary: updateSummaryFixture()}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	session := environment.session(t)
	path := "/api/v1/system/updates/gateway/v1.2.3/apply"
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]string{"password": "local-only-test-password", "runId": strings.Repeat("a", 64)}, environment.origin, ""), http.StatusForbidden)
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]string{"password": "wrong", "runId": strings.Repeat("a", 64)}, environment.origin, session.CSRFToken), http.StatusForbidden)
	response := environment.request(t, http.MethodPost, path, map[string]string{"password": "local-only-test-password", "runId": strings.Repeat("a", 64)}, environment.origin, session.CSRFToken)
	assertResponseStatus(t, response, http.StatusAccepted)
}

func TestUpdateApplyRejectsURLPathOrUnknownVersion(t *testing.T) {
	manager := &fakeUpdateManager{summary: updateSummaryFixture()}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.Updates = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	session := environment.session(t)
	for _, version := range []string{"https:%2F%2Fevil.test", "..%2Fv1.2.3", "v9.9.9"} {
		response := environment.request(t, http.MethodPost, "/api/v1/system/updates/gateway/"+version+"/apply", map[string]string{"password": "local-only-test-password", "runId": strings.Repeat("a", 64)}, environment.origin, session.CSRFToken)
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
	body := map[string]string{"password": "local-only-test-password", "runId": strings.Repeat("a", 64)}
	path := "/api/v1/system/updates/gateway/v1.2.3/apply"
	for range 2 {
		response := environment.request(t, http.MethodPost, path, body, environment.origin, session.CSRFToken)
		assertResponseStatus(t, response, http.StatusAccepted)
	}
	if manager.submitCalls != 2 || manager.lastRequest.RunID != strings.Repeat("a", 64) {
		t.Fatalf("submit calls=%d request=%#v", manager.submitCalls, manager.lastRequest)
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
