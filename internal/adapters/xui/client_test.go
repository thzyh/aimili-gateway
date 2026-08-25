package xui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type xuiFixture struct {
	mu                 sync.Mutex
	csrfCalls          int
	loginCalls         int
	addedProtocols     []string
	updatedXray        map[string]any
	inbounds           []map[string]any
	failProtocol       string
	initialXray        map[string]any
	stringXrayEnvelope bool
}

func (fixture *xuiFixture) handler(response http.ResponseWriter, request *http.Request) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	response.Header().Set("Content-Type", "application/json")
	switch request.URL.Path {
	case "/panel/csrf-token":
		fixture.csrfCalls++
		fmt.Fprintf(response, `{"success":true,"obj":"csrf-%d"}`, fixture.csrfCalls)
	case "/panel/login":
		fixture.loginCalls++
		http.SetCookie(response, &http.Cookie{Name: "session", Value: "memory-only", Path: "/panel/"})
		fmt.Fprint(response, `{"success":true,"obj":null}`)
	case "/panel/panel/api/xray/":
		setting := fixture.updatedXray
		if setting == nil {
			setting = fixture.initialXray
		}
		if setting == nil {
			setting = map[string]any{
				"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
				"routing":   map[string]any{"domainStrategy": "AsIs", "rules": []any{map[string]any{"type": "field", "domain": []any{"example.test"}, "outboundTag": "direct"}}},
			}
		}
		encoded, _ := json.Marshal(setting)
		xrayEnvelope := map[string]any{"xraySetting": string(encoded), "outboundTestUrl": "https://probe.invalid/"}
		if fixture.stringXrayEnvelope {
			encodedEnvelope, _ := json.Marshal(xrayEnvelope)
			body, _ := json.Marshal(map[string]any{"success": true, "obj": string(encodedEnvelope)})
			_, _ = response.Write(body)
		} else {
			body, _ := json.Marshal(map[string]any{"success": true, "obj": xrayEnvelope})
			_, _ = response.Write(body)
		}
	case "/panel/panel/api/xray/update":
		if err := request.ParseForm(); err != nil {
			testingError(response, "invalid form")
			return
		}
		if err := json.Unmarshal([]byte(request.Form.Get("xraySetting")), &fixture.updatedXray); err != nil {
			testingError(response, "invalid xray setting")
			return
		}
		fmt.Fprint(response, `{"success":true,"obj":null}`)
	case "/panel/panel/api/inbounds/list":
		body, _ := json.Marshal(map[string]any{"success": true, "obj": fixture.inbounds})
		_, _ = response.Write(body)
	case "/panel/panel/api/inbounds/add":
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			testingError(response, "invalid inbound")
			return
		}
		protocol := fmt.Sprint(payload["protocol"])
		if protocol == fixture.failProtocol {
			fmt.Fprint(response, `{"success":false,"obj":null}`)
			return
		}
		fixture.addedProtocols = append(fixture.addedProtocols, protocol)
		payload["id"] = float64(100 + len(fixture.addedProtocols))
		fixture.inbounds = append(fixture.inbounds, payload)
		fmt.Fprint(response, `{"success":true,"obj":null}`)
	case "/panel/panel/api/server/getNewX25519Cert":
		fmt.Fprint(response, `{"success":true,"obj":{"privateKey":"test-private","publicKey":"test-public"}}`)
	default:
		if strings.HasPrefix(request.URL.Path, "/panel/panel/api/inbounds/del/") {
			var id int64
			_, _ = fmt.Sscanf(strings.TrimPrefix(request.URL.Path, "/panel/panel/api/inbounds/del/"), "%d", &id)
			kept := fixture.inbounds[:0]
			for _, inbound := range fixture.inbounds {
				if int64(inbound["id"].(float64)) != id {
					kept = append(kept, inbound)
				}
			}
			fixture.inbounds = kept
			fmt.Fprint(response, `{"success":true,"obj":null}`)
			return
		}
		http.NotFound(response, request)
	}
}

func testingError(response http.ResponseWriter, message string) {
	response.WriteHeader(http.StatusBadRequest)
	_, _ = response.Write([]byte(message))
}

func newXUIFixtureClient(t *testing.T, fixture *xuiFixture) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/panel/", Credentials{Username: "automation", Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientSnapshotAuthenticatesWithCSRFAndKeepsCookiesInMemory(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fixture.csrfCalls != 2 || fixture.loginCalls != 1 {
		t.Fatalf("authentication calls csrf=%d login=%d", fixture.csrfCalls, fixture.loginCalls)
	}
	if len(snapshot.Outbounds) != 1 || snapshot.Outbounds[0].Tag != "direct" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if cookies := client.httpClient.Jar.Cookies(client.baseURL); len(cookies) != 1 || cookies[0].Name != "session" {
		t.Fatalf("session cookie not held by memory jar: %#v", cookies)
	}
}

func TestClientSnapshotAcceptsStringEncodedXrayEnvelope(t *testing.T) {
	fixture := &xuiFixture{stringXrayEnvelope: true}
	client := newXUIFixtureClient(t, fixture)
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Outbounds) != 1 || snapshot.Outbounds[0].Tag != "direct" || snapshot.OutboundTestURL != "https://probe.invalid/" {
		t.Fatalf("unexpected string-encoded snapshot: %#v", snapshot)
	}
}

func TestEnsureManagedGroupPreservesUnmanagedXrayResources(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName:      "agw-jp-dc",
		SOCKSPort:         17930,
		VLESSPort:         20000,
		MixedPort:         30000,
		VLESSClientID:     "test-client-id",
		MixedUsername:     "proxy-user",
		MixedPassword:     "proxy-password",
		MixedSourceCIDRs:  []string{"198.51.100.0/24"},
		RealityTarget:     "127.0.0.1:443",
		RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if managed.ResourceName != "agw-jp-dc" || managed.Fingerprint == "" ||
		managed.PublicKey == "" || managed.ShortID == "" || managed.ServerName != "proxy.example.test" {
		t.Fatalf("unexpected managed group: %#v", managed)
	}
	if strings.Join(fixture.addedProtocols, ",") != "vless,mixed" {
		t.Fatalf("created protocols = %#v", fixture.addedProtocols)
	}
	outboundTags := make(map[string]bool)
	for _, raw := range fixture.updatedXray["outbounds"].([]any) {
		outboundTags[raw.(map[string]any)["tag"].(string)] = true
	}
	for _, tag := range []string{"direct", "agw-jp-dc-socks", "agw-blackhole"} {
		if !outboundTags[tag] {
			t.Fatalf("missing outbound %q in %#v", tag, outboundTags)
		}
	}
	rules := fixture.updatedXray["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 4 {
		t.Fatalf("managed rules did not preserve unmanaged rule: %#v", rules)
	}
	allowed := rules[1].(map[string]any)["source"].([]any)
	joinedAllowed := fmt.Sprint(allowed)
	for _, prefix := range []string{"198.51.100.0/24", "127.0.0.1/32", "::1/128"} {
		if !strings.Contains(joinedAllowed, prefix) {
			t.Fatalf("mixed allow rule missing %s: %#v", prefix, allowed)
		}
	}
}

func TestEnsureManagedGroupUsesConfiguredLocalRealityTarget(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "test-client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceCIDRs: []string{"198.51.100.0/24"}, RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}

	if _, err := client.EnsureManagedGroup(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	var vless map[string]any
	for _, inbound := range fixture.inbounds {
		if inbound["protocol"] == "vless" {
			vless = inbound
			break
		}
	}
	if vless == nil {
		t.Fatal("managed VLESS inbound was not created")
	}
	var stream map[string]any
	if err := json.Unmarshal([]byte(vless["streamSettings"].(string)), &stream); err != nil {
		t.Fatal(err)
	}
	reality := stream["realitySettings"].(map[string]any)
	if reality["target"] != "127.0.0.1:443" || fmt.Sprint(reality["serverNames"]) != "[proxy.example.test]" {
		t.Fatalf("unexpected Reality target: %#v", reality)
	}
}

func TestEnsureManagedGroupRejectsSameTagWithoutOwnershipMarker(t *testing.T) {
	fixture := &xuiFixture{inbounds: []map[string]any{{
		"id": float64(9), "tag": "agw-jp-dc-vless", "remark": "User inbound", "protocol": "vless", "port": float64(20000),
	}}}
	client := newXUIFixtureClient(t, fixture)
	_, err := client.EnsureManagedGroup(context.Background(), DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "test-client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget:    "127.0.0.1:443", RealityServerName: "proxy.example.test",
	})
	var adapterError *AdapterError
	if err == nil || !strings.Contains(err.Error(), "ownership_conflict") || !errors.As(err, &adapterError) {
		t.Fatalf("unexpected ownership error: %v", err)
	}
	if fixture.updatedXray != nil || len(fixture.addedProtocols) != 0 {
		t.Fatal("ownership conflict changed 3x-ui state")
	}
}

func TestEnsureManagedGroupRejectsConflictingNamespacedOutbound(t *testing.T) {
	fixture := &xuiFixture{initialXray: map[string]any{
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "agw-jp-dc-socks", "protocol": "freedom"},
		},
		"routing": map[string]any{"rules": []any{}},
	}}
	client := newXUIFixtureClient(t, fixture)
	_, err := client.EnsureManagedGroup(context.Background(), DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "test-client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget:    "127.0.0.1:443", RealityServerName: "proxy.example.test",
	})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "ownership_conflict" {
		t.Fatalf("unexpected ownership error: %v", err)
	}
	if fixture.updatedXray != nil || len(fixture.addedProtocols) != 0 {
		t.Fatal("conflicting outbound was overwritten")
	}
}

func TestEnsureManagedGroupRejectsUnmanagedPortConflict(t *testing.T) {
	fixture := &xuiFixture{inbounds: []map[string]any{{
		"id": float64(9), "tag": "user-vless", "remark": "User inbound", "protocol": "vless", "port": float64(20000),
	}}}
	client := newXUIFixtureClient(t, fixture)
	_, err := client.EnsureManagedGroup(context.Background(), DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "test-client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget:    "127.0.0.1:443", RealityServerName: "proxy.example.test",
	})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "port_conflict" {
		t.Fatalf("unexpected port error: %v", err)
	}
	if fixture.updatedXray != nil {
		t.Fatal("port conflict changed Xray")
	}
}

func TestDeleteManagedGroupRemovesOnlyNamedResources(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "test-client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget:    "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteManagedGroup(context.Background(), managed); err != nil {
		t.Fatal(err)
	}
	if len(fixture.inbounds) != 0 {
		t.Fatalf("managed inbounds remain: %#v", fixture.inbounds)
	}
	outbounds := fixture.updatedXray["outbounds"].([]any)
	if len(outbounds) != 1 || outbounds[0].(map[string]any)["tag"] != "direct" {
		t.Fatalf("delete changed unmanaged outbounds: %#v", outbounds)
	}
}

func TestEnsureManagedGroupRollsBackPartialInboundCreation(t *testing.T) {
	fixture := &xuiFixture{failProtocol: "mixed"}
	client := newXUIFixtureClient(t, fixture)
	_, err := client.EnsureManagedGroup(context.Background(), DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "test-client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget:    "127.0.0.1:443", RealityServerName: "proxy.example.test",
	})
	if err == nil {
		t.Fatal("partial inbound creation succeeded")
	}
	if len(fixture.inbounds) != 0 {
		t.Fatalf("partial inbound was not rolled back: %#v", fixture.inbounds)
	}
	outbounds := fixture.updatedXray["outbounds"].([]any)
	if len(outbounds) != 1 || outbounds[0].(map[string]any)["tag"] != "direct" {
		t.Fatalf("Xray setting was not rolled back: %#v", outbounds)
	}
}

func TestNewClientRejectsRemotePanelAndCredentialsInURL(t *testing.T) {
	for _, raw := range []string{"http://192.0.2.10:2053/", "http://user@127.0.0.1:2053/"} {
		if _, err := NewClient(raw, Credentials{Username: "u", Password: "p"}); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
}

func TestReadCredentialsFileDecodesClosedSecretDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xui.json")
	if err := os.WriteFile(path, []byte(`{"username":"automation","password":"test-password"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials, err := ReadCredentialsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Username != "automation" || credentials.Password != "test-password" {
		t.Fatal("credentials file did not round trip")
	}
	if err := os.WriteFile(path, []byte(`{"username":"automation","password":"test-password","cookie":"forbidden"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredentialsFile(path); err == nil {
		t.Fatal("unknown credential field was accepted")
	}
}
