package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
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

func TestProxyGroupEnableRequiresCSRFRecentReauthAndReplaysIdempotencyKey(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	payload := map[string]string{"countryCode": "JP", "proxyType": "datacenter"}
	assertResponseStatus(t, environment.request(t, http.MethodPost, "/api/v1/proxy-groups", payload, environment.origin, csrf), http.StatusPreconditionRequired)
	assertResponseStatus(t, environment.request(t, http.MethodPost, "/api/v1/auth/reauth", map[string]string{"password": "local-only-test-password", "totp": "287082"}, environment.origin, csrf), http.StatusNoContent)
	first := environment.requestWithHeaders(t, http.MethodPost, "/api/v1/proxy-groups", payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "enable-jp-dc"})
	assertResponseStatus(t, first, http.StatusCreated)
	second := environment.requestWithHeaders(t, http.MethodPost, "/api/v1/proxy-groups", payload, environment.origin, csrf, map[string]string{"Idempotency-Key": "enable-jp-dc"})
	assertResponseStatus(t, second, http.StatusCreated)
	if manager.enableCalls != 1 {
		t.Fatalf("enable calls=%d", manager.enableCalls)
	}
}

func TestConnectionsRequireReadyGroupAndRecentReauthentication(t *testing.T) {
	manager := &fakeProxyManager{}
	environment := newAuthTestEnvironmentConfigured(t, true, func(dependencies *Dependencies) { dependencies.ProxyManager = manager })
	assertResponseStatus(t, environment.login(t), http.StatusNoContent)
	csrf := environment.session(t).CSRFToken
	assertResponseStatus(t, environment.request(t, http.MethodGet, "/api/v1/proxy-groups/agw-jp-dc/connections", nil, "", ""), http.StatusPreconditionRequired)
	assertResponseStatus(t, environment.request(t, http.MethodPost, "/api/v1/auth/reauth", map[string]string{"password": "local-only-test-password", "totp": "287082"}, environment.origin, csrf), http.StatusNoContent)
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

func TestIdempotencyCacheKeyKeepsLargeSessionIDsDistinct(t *testing.T) {
	left := idempotencyCacheKey(0x110000, http.MethodPost, "/api/v1/proxy-groups", "same")
	right := idempotencyCacheKey(0x110001, http.MethodPost, "/api/v1/proxy-groups", "same")
	if left == right {
		t.Fatal("large session IDs collided in idempotency cache")
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

type fakeProxyManager struct{ enableCalls int }

func (*fakeProxyManager) Countries(context.Context) ([]orchestrator.Country, error) {
	return []orchestrator.Country{{Code: "JP", Name: "日本", DatacenterCount: 2}}, nil
}
func (*fakeProxyManager) List(context.Context) ([]domain.ProxyGroup, error) {
	return []domain.ProxyGroup{}, nil
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
func (*fakeProxyManager) Rotate(context.Context, string) (domain.ProxyGroup, error) {
	return domain.ProxyGroup{}, nil
}
func (*fakeProxyManager) Disable(context.Context, string) error { return nil }
func (*fakeProxyManager) Connections(context.Context, string) (orchestrator.Connections, error) {
	return orchestrator.Connections{VLESSURI: "vless://masked-test", SOCKS5HURI: "socks5h://masked-test"}, nil
}
func (*fakeProxyManager) SetMixedCIDRs(context.Context, []netip.Prefix) error { return nil }
