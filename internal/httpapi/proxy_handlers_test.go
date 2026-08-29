package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
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
	if connections["vlessUri"] == "" || connections["socks5hUri"] == "" {
		t.Fatalf("connections=%#v", connections)
	}
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
		{ID: "agw-jp-dc-one", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter, Status: domain.ProxyGroupReady, ExitIP: "203.0.113.10", VLESSLatencyMS: 82, SOCKSLatencyMS: 71, Version: 2, LastCheckedAt: time.Unix(1_700_000_000, 0).UTC()},
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
	enableCalls       int
	activateCalls     int
	activatedID       string
	groups            []domain.ProxyGroup
	mixedPolicy       store.MixedSourcePolicy
	replaceCalls      int
	replacedCandidate string
	checkMainCalls    int
	cleanupCalls      int
}

func (*fakeProxyManager) Subscription(context.Context) (orchestrator.SubscriptionResult, error) {
	return orchestrator.SubscriptionResult{URL: "https://example.test/sub/masked", InboundCount: 4, UpdatedAt: time.Unix(1700000000, 0).UTC()}, nil
}
func (m *fakeProxyManager) ReplaceCandidate(_ context.Context, candidateID, targetID string) (domain.ProxyGroup, error) {
	m.replaceCalls++
	m.replacedCandidate = candidateID
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
	return orchestrator.Connections{VLESSURI: "vless://masked-test", SOCKS5HURI: "socks5h://masked-test"}, nil
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
