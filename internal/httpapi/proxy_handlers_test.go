package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
	"github.com/thzyh/aimili-gateway/internal/store"
)

func TestCountriesRequireAuthenticationAndReturnSafeCatalog(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.request(t, http.MethodGet, "/api/v1/countries", nil, "", ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/countries", nil, "", "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	var countries []orchestrator.Country
	if err := json.NewDecoder(response.Body).Decode(&countries); err != nil {
		t.Fatal(err)
	}
	if len(countries) != 1 || countries[0].Code != "JP" || countries[0].DatacenterCount != 2 {
		t.Fatalf("countries=%#v", countries)
	}
}

func TestProxyGroupEnableRequiresCSRFAndReplaysIdempotencyKeyWithoutReauth(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	payload := map[string]string{"countryCode": "JP", "proxyType": "datacenter"}
	assertResponseStatus(t, environment.requestWithHeaders(t, http.MethodPost, "/api/v1/proxy-groups", payload, environment.origin, "", map[string]string{"Idempotency-Key": "missing-csrf"}), http.StatusForbidden)
	assertResponseStatus(t, environment.request(t, http.MethodPost, "/api/v1/proxy-groups", payload, environment.origin, csrf), http.StatusPreconditionRequired)
	first := environment.requestWithHeaders(t, http.MethodPost, "/api/v1/proxy-groups", payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "enable-jp-dc"})
	assertResponseStatus(t, first, http.StatusCreated)
	second := environment.requestWithHeaders(t, http.MethodPost, "/api/v1/proxy-groups", payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "enable-jp-dc"})
	assertResponseStatus(t, second, http.StatusCreated)
	if manager.enableCalls != 1 {
		t.Fatalf("enable calls=%d", manager.enableCalls)
	}
}

func TestConnectionsRequireOnlyAnAuthenticatedSession(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/proxy-groups/agw-jp-dc/connections", nil, "", "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	var connections map[string]string
	if err := json.NewDecoder(response.Body).Decode(&connections); err != nil {
		t.Fatal(err)
	}
	if connections["protocolMode"] != string(domain.ProtocolVLESSTCPRealityVision) || connections["publicUri"] == "" || connections["vlessUri"] == "" || connections["socks5hUri"] == "" {
		t.Fatalf("connections=%#v", connections)
	}
}

func TestRotateMixedCredentialsRequiresMutationGuardsIsIdempotentAndReturnsNoSecret(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	path := "/api/v1/settings/socks5h-credentials/rotate"
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, nil, "", ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, nil, environment.origin, csrf), http.StatusPreconditionRequired)
	first := environment.requestWithHeaders(t, http.MethodPost, path, nil, environment.origin, csrf, map[string]string{"Idempotency-Key": "rotate-mixed-safe"})
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", first.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(first.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(payload)
	if payload["rotatedAt"] == nil || string(encoded) == "" || containsSensitiveCredentialField(payload) {
		t.Fatalf("unsafe response = %s", encoded)
	}
	second := environment.requestWithHeaders(t, http.MethodPost, path, nil, environment.origin, csrf, map[string]string{"Idempotency-Key": "rotate-mixed-safe"})
	assertResponseStatus(t, second, http.StatusOK)
	if manager.rotateMixedCredentialsCalls != 1 {
		t.Fatalf("rotation calls=%d", manager.rotateMixedCredentialsCalls)
	}
}

func containsSensitiveCredentialField(payload map[string]any) bool {
	for _, key := range []string{"username", "password", "mixedUsername", "mixedPassword"} {
		if _, exists := payload[key]; exists {
			return true
		}
	}
	return false
}

func TestLegacyAggregateConnectionsAreGoneAndCannotRecreateResources(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/proxy-groups/aggregate/connections", nil, "", "")
	assertResponseStatus(t, response, http.StatusGone)
}

func TestLegacyAggregateCleanupRequiresMutationGuardsAndIsIdempotent(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	path := "/api/v1/proxy-groups/legacy-aggregate/cleanup"
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]any{}, "", ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]any{}, "", csrf), http.StatusForbidden)
	assertResponseStatus(t, environment.request(t, http.MethodPost, path, map[string]any{}, environment.origin, csrf), http.StatusPreconditionRequired)
	first := environment.requestWithHeaders(t, http.MethodPost, path, map[string]any{}, environment.origin, csrf, map[string]string{"Idempotency-Key": "cleanup-legacy"})
	assertResponseStatus(t, first, http.StatusOK)
	second := environment.requestWithHeaders(t, http.MethodPost, path, map[string]any{}, environment.origin, csrf, map[string]string{"Idempotency-Key": "cleanup-legacy"})
	assertResponseStatus(t, second, http.StatusOK)
	if manager.cleanupCalls != 1 {
		t.Fatalf("cleanup calls=%d", manager.cleanupCalls)
	}
}

func TestSubscriptionRequiresSessionAndReturnsOneNoStoreURL(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.request(t, http.MethodGet, "/api/v1/proxy-groups/subscription", nil, "", ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/proxy-groups/subscription", nil, "", "")
	defer response.Body.Close()
	var result orchestrator.SubscriptionResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" || result.InboundCount != 4 || result.URL == "" {
		t.Fatalf("status=%d result=%#v", response.StatusCode, result)
	}
}

func TestReplaceCandidateRequiresMutationGuardsAndIsIdempotent(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	path := "/api/v1/proxy-groups/agw-jp-dc/replace"
	payload := map[string]string{"targetGroupId": "agw-target"}
	first := environment.requestWithHeaders(t, http.MethodPost, path, payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "replace-jp"})
	assertResponseStatus(t, first, http.StatusOK)
	second := environment.requestWithHeaders(t, http.MethodPost, path, payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "replace-jp"})
	assertResponseStatus(t, second, http.StatusOK)
	if manager.replaceCalls != 1 || manager.replacedCandidate != "agw-jp-dc" {
		t.Fatalf("replace calls=%d candidate=%q", manager.replaceCalls, manager.replacedCandidate)
	}
}

func TestMainReplacementReplaysCompletedPersistentOperation(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	sessionPayload := environment.session(t)
	storedSession, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/proxy-groups/candidate-one/replace"
	key := "persistent-main-replacement"
	keyHash, bodyHash := persistentIdempotencyHashes(storedSession.ID, http.MethodPost, path, key, []any{"agw-main"})
	now := time.Unix(1_700_000_000, 0).UTC()
	main := store.MainEgress{ResourceName: "agw-main", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, CandidateID: "candidate-one", ExitIP: "203.0.113.20", PublicInboundID: 1, MixedInboundID: 2, PublicPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: now}
	if err := environment.database.SaveMainEgress(context.Background(), main); err != nil {
		t.Fatal(err)
	}
	if err := environment.database.CreateEgressOperation(context.Background(), store.EgressOperation{OperationID: "http-persistent-main", EgressID: "agw-main", Kind: "main_assign", Phase: "completed", RequestHash: keyHash, TransactionID: bodyHash, StartedAt: now, CompletedAt: now}); err != nil {
		t.Fatal(err)
	}

	response := environment.requestWithHeaders(t, http.MethodPost, path, map[string]string{"targetGroupId": "agw-main"}, environment.origin, sessionPayload.CSRFToken, map[string]string{"Idempotency-Key": key})
	assertResponseStatus(t, response, http.StatusOK)
	if manager.replaceCalls != 0 {
		t.Fatalf("completed main assignment was executed again: %d", manager.replaceCalls)
	}
}

func TestMainReplacementResumesStartedPersistentOperation(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	sessionPayload := environment.session(t)
	storedSession, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/proxy-groups/candidate-one/replace"
	key := "started-main-replacement"
	keyHash, bodyHash := persistentIdempotencyHashes(storedSession.ID, http.MethodPost, path, key, []any{"agw-main"})
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := environment.database.CreateEgressOperation(context.Background(), store.EgressOperation{OperationID: "http-started-main", EgressID: "agw-main", Kind: "main_assign", Phase: "started", RequestHash: keyHash, TransactionID: bodyHash, StartedAt: now}); err != nil {
		t.Fatal(err)
	}

	response := environment.requestWithHeaders(t, http.MethodPost, path, map[string]string{"targetGroupId": "agw-main"}, environment.origin, sessionPayload.CSRFToken, map[string]string{"Idempotency-Key": key})

	assertResponseStatus(t, response, http.StatusOK)
	if manager.replaceCalls != 1 {
		t.Fatalf("started main assignment was not resumed: %d", manager.replaceCalls)
	}
	if manager.mainAssignmentOperationKey != "http-started-main" {
		t.Fatalf("main assignment operation key = %q", manager.mainAssignmentOperationKey)
	}
}

func TestProtocolModeUpdateRequiresMutationGuardsAndIsIdempotent(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	payload := map[string]string{"protocolMode": "vless_xhttp_reality"}
	assertResponseStatus(t, environment.request(t, http.MethodPut, path, payload, "", ""), http.StatusUnauthorized)
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	assertResponseStatus(t, environment.request(t, http.MethodPut, path, payload, environment.origin, csrf), http.StatusPreconditionRequired)
	first := environment.requestWithHeaders(t, http.MethodPut, path, payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "protocol-jp-xhttp"})
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", first.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(first.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["protocolMode"] != "vless_xhttp_reality" || result["protocolState"] != "ready" || result["subscriptionState"] != "ready" || len(result["availableProtocolModes"].([]any)) != 3 {
		t.Fatalf("protocol response = %#v", result)
	}
	second := environment.requestWithHeaders(t, http.MethodPut, path, payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "protocol-jp-xhttp"})
	assertResponseStatus(t, second, http.StatusOK)
	if manager.protocolCalls != 1 || manager.protocolTarget != domain.ProtocolVLESSXHTTPReality {
		t.Fatalf("protocol calls=%d target=%q", manager.protocolCalls, manager.protocolTarget)
	}
}

func TestProtocolModeIdempotencyKeyRejectsDifferentRequestBody(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	headers := map[string]string{"Idempotency-Key": "protocol-body-conflict"}
	first := environment.requestWithHeaders(t, http.MethodPut, path, map[string]string{"protocolMode": "vless_xhttp_reality"}, environment.origin, csrf, headers)
	assertResponseStatus(t, first, http.StatusOK)
	second := environment.requestWithHeaders(t, http.MethodPut, path, map[string]string{"protocolMode": "hysteria2_quic_tls"}, environment.origin, csrf, headers)
	assertResponseStatus(t, second, http.StatusConflict)
	if manager.protocolCalls != 1 || manager.protocolTarget != domain.ProtocolVLESSXHTTPReality {
		t.Fatalf("conflicting request reached manager: calls=%d target=%q", manager.protocolCalls, manager.protocolTarget)
	}
}

func TestProtocolModeForwardsOptionalExpectedMode(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	response := environment.requestWithHeaders(
		t, http.MethodPut, "/api/v1/proxy-groups/agw-jp-dc/protocol-mode",
		map[string]string{"protocolMode": "vless_xhttp_reality", "expectedProtocolMode": "vless_tcp_reality_vision"},
		environment.origin, csrf, map[string]string{"Idempotency-Key": "protocol-cas"},
	)
	assertResponseStatus(t, response, http.StatusOK)
	if manager.protocolExpected != domain.ProtocolVLESSTCPRealityVision {
		t.Fatalf("expected mode = %q", manager.protocolExpected)
	}
}

func TestProtocolModeResumesStartedOperationAfterRecoveryToOldMode(t *testing.T) {
	manager := &fakeProxyManager{protocolResumeAllowed: true}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	storedSession, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	key := "resume-started-protocol"
	target := domain.ProtocolVLESSXHTTPReality
	keyHash, bodyHash := persistentIdempotencyHashes(storedSession.ID, http.MethodPut, path, key, []any{target})
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := environment.database.CreateEgressProtocolMode(context.Background(), domain.EgressProtocolMode{
		EgressID: "agw-jp-dc", ActiveMode: domain.ProtocolVLESSTCPRealityVision,
		DesiredMode: domain.ProtocolVLESSTCPRealityVision, State: domain.ProtocolReady,
		Version: 2, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := environment.database.CreateEgressOperation(context.Background(), store.EgressOperation{
		OperationID: "http-started-protocol", EgressID: "agw-jp-dc", Kind: "protocol_switch",
		Phase: "started", RequestHash: keyHash, TransactionID: bodyHash, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	response := environment.requestWithHeaders(t, http.MethodPut, path, map[string]string{"protocolMode": string(target)}, environment.origin, csrf, map[string]string{"Idempotency-Key": key})
	assertResponseStatus(t, response, http.StatusOK)
	if manager.protocolCalls != 1 || manager.protocolTarget != target || manager.protocolExpected != domain.ProtocolVLESSTCPRealityVision {
		t.Fatalf("started operation was not safely resumed: calls=%d target=%q expected=%q", manager.protocolCalls, manager.protocolTarget, manager.protocolExpected)
	}
	operation, err := environment.database.GetEgressOperationByRequestHash(context.Background(), "agw-jp-dc", "protocol_switch", keyHash)
	if err != nil || operation.Phase != "completed" {
		t.Fatalf("resumed operation = %#v, err = %v", operation, err)
	}
}

func TestProtocolModeStartedOperationStillRejectsDifferentBody(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	storedSession, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	key := "started-body-conflict"
	keyHash, bodyHash := persistentIdempotencyHashes(storedSession.ID, http.MethodPut, path, key, []any{domain.ProtocolVLESSXHTTPReality})
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := environment.database.CreateEgressOperation(context.Background(), store.EgressOperation{
		OperationID: "http-started-conflict", EgressID: "agw-jp-dc", Kind: "protocol_switch",
		Phase: "started", RequestHash: keyHash, TransactionID: bodyHash, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	response := environment.requestWithHeaders(t, http.MethodPut, path, map[string]string{"protocolMode": "hysteria2_quic_tls"}, environment.origin, csrf, map[string]string{"Idempotency-Key": key})
	assertResponseStatus(t, response, http.StatusConflict)
	if manager.protocolCalls != 0 {
		t.Fatalf("conflicting started request reached manager: %d", manager.protocolCalls)
	}
}

func TestProtocolModeStartedOperationCannotOverwriteCurrentThirdMode(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	storedSession, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	key := "started-before-third-mode"
	target := domain.ProtocolVLESSXHTTPReality
	keyHash, bodyHash := persistentIdempotencyHashes(storedSession.ID, http.MethodPut, path, key, []any{target})
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := environment.database.CreateEgressProtocolMode(context.Background(), domain.EgressProtocolMode{
		EgressID: "agw-jp-dc", ActiveMode: domain.ProtocolHysteria2QUICTLS,
		DesiredMode: domain.ProtocolHysteria2QUICTLS, State: domain.ProtocolReady,
		Version: 3, UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := environment.database.CreateEgressOperation(context.Background(), store.EgressOperation{
		OperationID: "http-started-before-third", EgressID: "agw-jp-dc", Kind: "protocol_switch",
		Phase: "started", RequestHash: keyHash, TransactionID: bodyHash, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	response := environment.requestWithHeaders(t, http.MethodPut, path, map[string]string{"protocolMode": string(target)}, environment.origin, csrf, map[string]string{"Idempotency-Key": key})
	assertResponseStatus(t, response, http.StatusConflict)
	if manager.protocolCalls != 0 {
		t.Fatalf("old started request overwrote third mode: %d", manager.protocolCalls)
	}
}

func TestProtocolModeFailureIsPersistedAndReplayedWithoutRunningAgain(t *testing.T) {
	manager := &fakeProxyManager{protocolError: &orchestrator.Error{Code: "xray_offline_test_failed"}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	payload := map[string]string{"protocolMode": "vless_xhttp_reality", "expectedProtocolMode": "vless_tcp_reality_vision"}
	headers := map[string]string{"Idempotency-Key": "protocol-failed-terminal"}

	first := environment.requestWithHeaders(t, http.MethodPut, path, payload, environment.origin, csrf, headers)
	assertResponseStatus(t, first, http.StatusConflict)
	second := environment.requestWithHeaders(t, http.MethodPut, path, payload, environment.origin, csrf, headers)
	assertResponseStatus(t, second, http.StatusConflict)
	if manager.protocolCalls != 1 {
		t.Fatalf("failed protocol operation ran %d times", manager.protocolCalls)
	}

	storedSession, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyHash, _ := persistentIdempotencyHashes(storedSession.ID, http.MethodPut, path, headers["Idempotency-Key"], []any{domain.ProtocolVLESSXHTTPReality, domain.ProtocolVLESSTCPRealityVision})
	operation, err := environment.database.GetEgressOperationByRequestHash(context.Background(), "agw-jp-dc", "protocol_switch", keyHash)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != "failed" || operation.ErrorCode != "xray_offline_test_failed" || operation.CompletedAt.IsZero() {
		t.Fatalf("failed operation = %#v", operation)
	}
}

func TestProtocolModeInternalFailureReplaysTheSameHTTPStatus(t *testing.T) {
	manager := &fakeProxyManager{protocolError: errors.New("masked internal failure")}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	payload := map[string]string{"protocolMode": "vless_xhttp_reality"}
	headers := map[string]string{"Idempotency-Key": "protocol-internal-terminal"}

	assertResponseStatus(t, environment.requestWithHeaders(t, http.MethodPut, path, payload, environment.origin, csrf, headers), http.StatusInternalServerError)
	assertResponseStatus(t, environment.requestWithHeaders(t, http.MethodPut, path, payload, environment.origin, csrf, headers), http.StatusInternalServerError)
	if manager.protocolCalls != 1 {
		t.Fatalf("internal protocol operation ran %d times", manager.protocolCalls)
	}
}

func TestProtocolModeReplaysCompletedPersistentOperationAfterServerRestart(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	sessionPayload := environment.session(t)
	csrf := sessionPayload.CSRFToken
	storedSession, err := environment.database.GetSession(context.Background(), environment.sessionTokenHash(t), environment.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/proxy-groups/agw-jp-dc/protocol-mode"
	key := "persistent-protocol-request"
	body := []any{domain.ProtocolVLESSXHTTPReality}
	keyHash, bodyHash := persistentIdempotencyHashes(storedSession.ID, http.MethodPut, path, key, body)
	now := time.Unix(1_700_000_000, 0).UTC()
	state := domain.EgressProtocolMode{EgressID: "agw-jp-dc", ActiveMode: domain.ProtocolVLESSXHTTPReality, DesiredMode: domain.ProtocolVLESSXHTTPReality, State: domain.ProtocolReady, Version: 2, UpdatedAt: now}
	if err := environment.database.CreateEgressProtocolMode(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if err := environment.database.CreateEgressOperation(context.Background(), store.EgressOperation{OperationID: "http-persistent-protocol", EgressID: "agw-jp-dc", Kind: "protocol_switch", Phase: "completed", RequestHash: keyHash, TransactionID: bodyHash, StartedAt: now, CompletedAt: now}); err != nil {
		t.Fatal(err)
	}

	response := environment.requestWithHeaders(t, http.MethodPut, path, map[string]string{"protocolMode": "vless_xhttp_reality"}, environment.origin, csrf, map[string]string{"Idempotency-Key": key})
	assertResponseStatus(t, response, http.StatusOK)
	if manager.protocolCalls != 0 {
		t.Fatalf("completed persistent operation was executed again: %d", manager.protocolCalls)
	}
	conflict := environment.requestWithHeaders(t, http.MethodPut, path, map[string]string{"protocolMode": "hysteria2_quic_tls"}, environment.origin, csrf, map[string]string{"Idempotency-Key": key})
	assertResponseStatus(t, conflict, http.StatusConflict)
}

func TestCheckMainRequiresMutationGuards(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	response := environment.requestWithHeaders(t, http.MethodPost, "/api/v1/proxy-groups/agw-main/check", nil, environment.origin, csrf, map[string]string{"Idempotency-Key": "check-main"})
	assertResponseStatus(t, response, http.StatusOK)
	if manager.checkMainCalls != 1 {
		t.Fatalf("main check calls=%d", manager.checkMainCalls)
	}
}

func TestProxyPoolFiltersAndExposesProtocolLatenciesWithoutSecrets(t *testing.T) {
	manager := &fakeProxyManager{groups: []domain.ProxyGroup{
		{ID: "agw-jp-dc-one", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter, Status: domain.ProxyGroupReady, ExitIP: "203.0.113.10", PublicPort: 20000, VLESSLatencyMS: 82, SOCKSLatencyMS: 71, ProtocolMode: domain.ProtocolVLESSXHTTPReality, DesiredProtocolMode: domain.ProtocolVLESSXHTTPReality, ProtocolState: domain.ProtocolReady, Version: 2, LastCheckedAt: time.Unix(1_700_000_000, 0).UTC()},
		{ID: "agw-kr-res-one", CountryCode: "KR", CountryName: "韩国", ProxyType: domain.ProxyTypeResidential, Status: domain.ProxyGroupReady, ExitIP: "203.0.113.11", VLESSLatencyMS: 95, SOCKSLatencyMS: 88, Version: 2},
	}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	response := environment.request(t, http.MethodGet, "/api/v1/proxy-groups?country=JP&proxyType=datacenter&status=ready", nil, "", "")
	defer response.Body.Close()
	var groups []proxyGroupResponse
	if err := json.NewDecoder(response.Body).Decode(&groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].VLESSLatencyMS != 82 || groups[0].SOCKSLatencyMS != 71 {
		t.Fatalf("unexpected filtered pool: %#v", groups)
	}
	if groups[0].PublicPort != 20000 || groups[0].ProtocolMode != domain.ProtocolVLESSXHTTPReality || groups[0].DesiredProtocolMode != domain.ProtocolVLESSXHTTPReality || groups[0].ProtocolState != domain.ProtocolReady || groups[0].SubscriptionState != "ready" || len(groups[0].AvailableProtocolModes) != 3 {
		t.Fatalf("protocol state missing from pool response: %#v", groups[0])
	}
}

func TestSafeProxyGroupExposesMissingProtocolStateAsRepairRequired(t *testing.T) {
	result := safeProxyGroup(domain.ProxyGroup{ID: "agw-jp-dc", Status: domain.ProxyGroupRepairRequired, ProtocolState: domain.ProtocolRepairRequired, ProtocolLastErrorCode: "protocol_state_missing"})
	if result.ProtocolState != domain.ProtocolRepairRequired || result.SubscriptionState != "repair_required" || result.LastErrorCode != "protocol_state_missing" || len(result.AvailableProtocolModes) != 3 {
		t.Fatalf("repair state was hidden: %#v", result)
	}
}

func TestSafeProxyGroupExposesCandidateAndVerifiedExitMetadataSeparately(t *testing.T) {
	result := safeProxyGroup(domain.ProxyGroup{
		ID: "agw-jp-dc-one", CandidateIP: "198.51.100.10",
		ExitIP: "203.0.113.10", ExitIPCheckedAt: 1700000005, AutoRepairPerformed: true,
	})
	if result.CandidateIP != "198.51.100.10" || result.ExitIP != "203.0.113.10" || result.ExitIPCheckedAt != 1700000005 || !result.AutoRepairPerformed {
		t.Fatalf("safe response collapsed candidate and exit metadata: %#v", result)
	}

	missing := safeProxyGroup(domain.ProxyGroup{ID: "agw-us-res-one", CandidateIP: "198.51.100.11"})
	if missing.CandidateIP != "198.51.100.11" || missing.ExitIP != "" || missing.ExitIPCheckedAt != 0 {
		t.Fatalf("safe response fabricated a verified exit: %#v", missing)
	}

}

func TestStandbyCandidateCanBeActivatedWithoutRecentReauthentication(t *testing.T) {
	manager := &fakeProxyManager{groups: []domain.ProxyGroup{
		{ID: "agw-kr-res-standby", CountryCode: "KR", CountryName: "韩国", ProxyType: domain.ProxyTypeResidential, Status: domain.ProxyGroupStandby, CandidateLatencyMS: 35, Version: 1},
	}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	path := "/api/v1/proxy-groups/agw-kr-res-standby/activate"
	response := environment.requestWithHeaders(t, http.MethodPost, path, nil, environment.origin, csrf, map[string]string{"Idempotency-Key": "activate-kr"})
	assertResponseStatus(t, response, http.StatusOK)
	if manager.activateCalls != 1 || manager.activatedID != "agw-kr-res-standby" {
		t.Fatalf("activate calls=%d id=%q", manager.activateCalls, manager.activatedID)
	}
}

func TestPoolExportUsesAuthenticatedSessionAndExportsOnlyReadyFilteredRows(t *testing.T) {
	manager := &fakeProxyManager{groups: []domain.ProxyGroup{
		{ID: "ready-jp", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, Status: domain.ProxyGroupReady},
		{ID: "broken-jp", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, Status: domain.ProxyGroupDegraded},
	}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	path := "/api/v1/proxy-groups/export?protocol=vless&country=JP&proxyType=datacenter"
	response := environment.request(t, http.MethodGet, path, nil, "", "")
	defer response.Body.Close()
	var body string
	raw := make([]byte, 256)
	n, _ := response.Body.Read(raw)
	body = string(raw[:n])
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/plain; charset=utf-8" || body != "vless://masked-test\n" {
		t.Fatalf("status=%d type=%q body=%q", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
}

func TestIdempotencyCacheKeyKeepsLargeSessionIDsDistinct(t *testing.T) {
	left := idempotencyCacheKey(0x110000, http.MethodPost, "/api/v1/proxy-groups", "same")
	right := idempotencyCacheKey(0x110001, http.MethodPost, "/api/v1/proxy-groups", "same")
	if left == right {
		t.Fatal("large session IDs collided in idempotency cache")
	}
}

func TestMixedSourcePolicyCanBeReadAndDisabledWithoutCIDRs(t *testing.T) {
	manager := &fakeProxyManager{mixedPolicy: store.MixedSourcePolicy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, ApplyStatus: store.MixedPolicyApplied}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)

	response := environment.request(t, http.MethodGet, "/api/v1/settings/mixed-source-policy", nil, "", "")
	if response.StatusCode != http.StatusOK {
		assertResponseStatus(t, response, http.StatusOK)
	}
	defer response.Body.Close()
	var got struct {
		Enabled     bool     `json:"enabled"`
		CIDRs       []string `json:"cidrs"`
		ApplyStatus string   `json:"applyStatus"`
	}
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || len(got.CIDRs) != 1 || got.CIDRs[0] != "198.51.100.0/24" || got.ApplyStatus != "applied" {
		t.Fatalf("policy = %#v", got)
	}

	csrf := environment.session(t).CSRFToken
	response = environment.request(t, http.MethodPut, "/api/v1/settings/mixed-source-policy", map[string]any{"enabled": false, "cidrs": []string{}}, environment.origin, csrf)
	assertResponseStatus(t, response, http.StatusOK)
	if manager.mixedPolicy.Enabled || manager.mixedPolicy.ApplyStatus != store.MixedPolicyApplied {
		t.Fatalf("saved policy = %#v", manager.mixedPolicy)
	}
}

func TestMixedSourcePolicyRejectsUnsafeOrNonCanonicalCIDRs(t *testing.T) {
	tests := []struct {
		name string
		cidr string
	}{
		{name: "IPv4 all", cidr: "0.0.0.0/0"},
		{name: "IPv6 all", cidr: "::/0"},
		{name: "not canonical", cidr: "198.51.100.1/24"},
		{name: "invalid", cidr: "not-a-prefix"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &fakeProxyManager{}
			environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
			assertResponseStatus(t, environment.login(t), http.StatusNoContent)
			csrf := environment.session(t).CSRFToken
			response := environment.request(t, http.MethodPut, "/api/v1/settings/mixed-source-policy", map[string]any{"enabled": true, "cidrs": []string{test.cidr}}, environment.origin, csrf)
			assertResponseStatus(t, response, http.StatusBadRequest)
		})
	}
}

func TestMixedSourcePolicyAuthorizesCurrentForwardedClientAsSingleHost(t *testing.T) {
	manager := &fakeProxyManager{mixedPolicy: store.MixedSourcePolicy{
		Enabled:     false,
		CIDRs:       []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
		ApplyStatus: store.MixedPolicyFailed,
	}}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	request := environment.newRequest(t, http.MethodPost, "/api/v1/settings/mixed-source-policy/authorize-current", nil, environment.origin, csrf)
	request.RemoteAddr = "127.0.0.1:41000"
	request.Header.Set("X-Forwarded-For", "198.51.100.7")
	response, err := environment.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertResponseStatus(t, response, http.StatusOK)
	if !manager.mixedPolicy.Enabled || manager.mixedPolicy.ApplyStatus != store.MixedPolicyApplied || len(manager.mixedPolicy.CIDRs) != 2 || manager.mixedPolicy.CIDRs[1] != netip.MustParsePrefix("198.51.100.7/32") {
		t.Fatalf("authorized policy = %#v", manager.mixedPolicy)
	}
}

func TestCurrentForwardedClientPrefixClassifiesUnavailableSources(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		wantCode   string
	}{
		{name: "untrusted peer", remoteAddr: "198.51.100.8:41000", forwarded: "198.51.100.7", wantCode: "client_peer_untrusted"},
		{name: "missing forwarded header", remoteAddr: "127.0.0.1:41000", wantCode: "client_forwarded_for_missing"},
		{name: "invalid forwarded value", remoteAddr: "127.0.0.1:41000", forwarded: "unknown", wantCode: "client_forwarded_for_invalid"},
		{name: "private forwarded value", remoteAddr: "127.0.0.1:41000", forwarded: "192.168.1.2", wantCode: "client_forwarded_for_non_public"},
		{name: "loopback forwarded value", remoteAddr: "127.0.0.1:41000", forwarded: "127.0.0.1", wantCode: "client_forwarded_for_non_public"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("X-Forwarded-For", test.forwarded)
			_, code := currentForwardedClientPrefix(request)
			if code != test.wantCode {
				t.Fatalf("code = %q, want %q", code, test.wantCode)
			}
		})
	}
}

func (e *authTestEnvironment) requestWithHeaders(t *testing.T, method, path string, payload any, origin, csrf string, headers map[string]string) *http.Response {
	t.Helper()
	responseRequest := e.newRequest(t, method, path, payload, origin, csrf)
	for key, value := range headers {
		responseRequest.Header.Set(key, value)
	}
	response, err := e.client.Do(responseRequest)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type fakeProxyManager struct {
	enableCalls                 int
	activateCalls               int
	activatedID                 string
	groups                      []domain.ProxyGroup
	mixedPolicy                 store.MixedSourcePolicy
	replaceCalls                int
	replacedCandidate           string
	mainAssignmentOperationKey  string
	checkMainCalls              int
	cleanupCalls                int
	protocolCalls               int
	protocolTarget              domain.ProtocolMode
	protocolExpected            domain.ProtocolMode
	protocolError               error
	protocolResumeAllowed       bool
	rotateMixedCredentialsCalls int
}

func (m *fakeProxyManager) RotateMixedCredentials(context.Context) (time.Time, error) {
	m.rotateMixedCredentialsCalls++
	return time.Unix(1700000000, 0).UTC(), nil
}

func (m *fakeProxyManager) SwitchProtocolModeExpected(_ context.Context, egressID string, target, expected domain.ProtocolMode) (domain.EgressProtocolMode, error) {
	m.protocolCalls++
	m.protocolTarget = target
	m.protocolExpected = expected
	if m.protocolError != nil {
		return domain.EgressProtocolMode{}, m.protocolError
	}
	return domain.EgressProtocolMode{EgressID: egressID, ActiveMode: target, DesiredMode: target, State: domain.ProtocolReady, Version: 2, UpdatedAt: time.Unix(1700000000, 0).UTC()}, nil
}

func (m *fakeProxyManager) CanResumeInterruptedProtocolMode(context.Context, string, domain.ProtocolMode, domain.ProtocolMode) bool {
	return m.protocolResumeAllowed
}

func (*fakeProxyManager) Subscription(context.Context) (orchestrator.SubscriptionResult, error) {
	return orchestrator.SubscriptionResult{URL: "https://example.test/sub/masked", InboundCount: 4, UpdatedAt: time.Unix(1700000000, 0).UTC()}, nil
}
func (m *fakeProxyManager) ReplaceCandidate(ctx context.Context, candidateID, targetID string) (domain.ProxyGroup, error) {
	m.replaceCalls++
	m.replacedCandidate = candidateID
	m.mainAssignmentOperationKey = orchestrator.MainAssignmentOperationKey(ctx)
	return domain.ProxyGroup{ID: targetID, Status: domain.ProxyGroupReady, CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter}, nil
}
func (m *fakeProxyManager) CheckMain(context.Context) (store.MainEgress, error) {
	m.checkMainCalls++
	return store.MainEgress{ResourceName: "agw-main", CountryCode: "JP", ProxyType: domain.ProxyTypeDatacenter, Enabled: true, VLESSLatencyMS: 10, SOCKSLatencyMS: 12}, nil
}

func (*fakeProxyManager) Countries(context.Context) ([]orchestrator.Country, error) {
	return []orchestrator.Country{{Code: "JP", Name: "日本", DatacenterCount: 2}}, nil
}
func (m *fakeProxyManager) List(context.Context) ([]domain.ProxyGroup, error) {
	return append([]domain.ProxyGroup(nil), m.groups...), nil
}
func (m *fakeProxyManager) Pool(context.Context) ([]domain.ProxyGroup, error) {
	return append([]domain.ProxyGroup(nil), m.groups...), nil
}
func (m *fakeProxyManager) Enable(_ context.Context, request orchestrator.EnableRequest) (domain.ProxyGroup, error) {
	m.enableCalls++
	group, _ := domain.NewProxyGroupIdentity(request.CountryCode, request.ProxyType)
	group.Status = domain.ProxyGroupReady
	group.Version = 2
	return group, nil
}
func (*fakeProxyManager) Check(context.Context, string) (domain.ProxyGroup, error) {
	return domain.ProxyGroup{}, nil
}
func (m *fakeProxyManager) Activate(_ context.Context, id string) (domain.ProxyGroup, error) {
	m.activateCalls++
	m.activatedID = id
	return domain.ProxyGroup{ID: id, CountryCode: "KR", CountryName: "韩国", ProxyType: domain.ProxyTypeResidential, Status: domain.ProxyGroupReady, Version: 2}, nil
}
func (*fakeProxyManager) Rotate(context.Context, string) (domain.ProxyGroup, error) {
	return domain.ProxyGroup{}, nil
}
func (*fakeProxyManager) Disable(context.Context, string) error { return nil }
func (*fakeProxyManager) Connections(context.Context, string) (orchestrator.Connections, error) {
	return orchestrator.Connections{ProtocolMode: domain.ProtocolVLESSTCPRealityVision, PublicURI: "vless://masked-test", VLESSURI: "vless://masked-test", SOCKS5HURI: "socks5h://masked-test"}, nil
}
func (m *fakeProxyManager) CleanupLegacyAggregate(context.Context) (orchestrator.LegacyAggregateCleanup, error) {
	m.cleanupCalls++
	return orchestrator.LegacyAggregateCleanup{Removed: true, UpdatedAt: time.Unix(1700000000, 0).UTC()}, nil
}
func (m *fakeProxyManager) MixedPolicy(context.Context) (store.MixedSourcePolicy, error) {
	return m.mixedPolicy, nil
}
func (m *fakeProxyManager) SetMixedPolicy(_ context.Context, policy store.MixedSourcePolicy) error {
	policy.ApplyStatus = store.MixedPolicyApplied
	m.mixedPolicy = policy
	return nil
}
func (*fakeProxyManager) Reconcile(context.Context) orchestrator.ReconcileResult {
	return orchestrator.ReconcileResult{Discovered: 1, Ready: 1}
}
