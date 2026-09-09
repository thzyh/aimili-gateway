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
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

type xuiFixture struct {
	mu                 sync.Mutex
	csrfCalls          int
	loginCalls         int
	addedProtocols     []string
	updatedInboundIDs  []int64
	deletedClients     []string
	updatedXray        map[string]any
	updatedXrayCalls   int
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
		fixture.updatedXrayCalls++
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
		if strings.HasPrefix(request.URL.Path, "/panel/panel/api/inbounds/update/") {
			var id int64
			_, _ = fmt.Sscanf(strings.TrimPrefix(request.URL.Path, "/panel/panel/api/inbounds/update/"), "%d", &id)
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				testingError(response, "invalid inbound")
				return
			}
			for index, inbound := range fixture.inbounds {
				if int64(inbound["id"].(float64)) == id {
					payload["id"] = float64(id)
					fixture.inbounds[index] = payload
					fixture.updatedInboundIDs = append(fixture.updatedInboundIDs, id)
					fmt.Fprint(response, `{"success":true,"obj":null}`)
					return
				}
			}
			http.NotFound(response, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/panel/panel/api/clients/del/") {
			fixture.deletedClients = append(fixture.deletedClients, strings.TrimPrefix(request.URL.Path, "/panel/panel/api/clients/del/"))
			fmt.Fprint(response, `{"success":true,"obj":null}`)
			return
		}
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

func TestVLESSInboundUsesOneResourceScopedClientIdentity(t *testing.T) {
	clientEmail := func(resourceName string) string {
		desired := DesiredGroup{ResourceName: resourceName, VLESSClientID: "client-id", VLESSPort: 20000}
		inbound := vlessInbound(desired, resourceName+"-vless", "private", "public", "short")
		var settings struct {
			Clients []struct {
				Email string `json:"email"`
			} `json:"clients"`
		}
		if err := json.Unmarshal([]byte(inbound["settings"].(string)), &settings); err != nil {
			t.Fatal(err)
		}
		if len(settings.Clients) != 1 {
			t.Fatalf("managed VLESS inbound clients = %d, want 1", len(settings.Clients))
		}
		return settings.Clients[0].Email
	}

	jp := clientEmail("agw-jp-dc")
	kr := clientEmail("agw-kr-dc")
	if jp != "aimili-gateway-jp-dc" {
		t.Fatalf("JP client email = %q", jp)
	}
	if kr != "aimili-gateway-kr-dc" {
		t.Fatalf("KR client email = %q", kr)
	}
	if jp == kr {
		t.Fatal("managed groups reused a global 3x-ui client email")
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
	if reality["mldsa65Seed"] != "" || reality["settings"].(map[string]any)["mldsa65Verify"] != "" {
		t.Fatalf("ML-DSA was enabled without a verified capability gate: %#v", reality)
	}
}

func TestMergeManagedXrayCanDisableMixedSourceRestriction(t *testing.T) {
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: false, RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	setting, err := mergeManagedXray(map[string]any{}, desired, "agw-jp-dc-vless", "agw-jp-dc-mixed")
	if err != nil {
		t.Fatal(err)
	}
	routing := setting["routing"].(map[string]any)
	rules := asObjectSlice(routing["rules"])
	if len(rules) != 2 {
		t.Fatalf("rules = %#v", rules)
	}
	if _, exists := rules[1]["source"]; exists {
		t.Fatalf("disabled policy still has source whitelist: %#v", rules[1])
	}
	if stringValue(rules[1]["outboundTag"]) != "agw-jp-dc-socks" {
		t.Fatalf("mixed route = %#v", rules[1])
	}
}

func TestInspectLegacyMainRequiresExact8443To7928Chain(t *testing.T) {
	setting := map[string]any{
		"outbounds": []any{map[string]any{
			"tag": "aimili-socks", "protocol": "socks",
			"settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 7928}}},
		}},
		"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []any{"aimili-reality"}, "outboundTag": "aimili-socks"}}},
	}
	inbounds := []Inbound{{ID: 1, Tag: "aimili-reality", Remark: "Aimili Reality", Protocol: "vless", Port: 8443}}
	if err := verifyLegacyMainChain(inbounds, setting, 8443, 7928); err != nil {
		t.Fatal(err)
	}
	setting["outbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)["servers"].([]any)[0].(map[string]any)["port"] = 9999
	if err := verifyLegacyMainChain(inbounds, setting, 8443, 7928); err == nil {
		t.Fatal("wrong SOCKS port was accepted")
	}
}

func TestEnsureLegacyMainPreserves8443AndAddsOnlyMixedInbound(t *testing.T) {
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{map[string]any{
				"tag": "aimili-socks", "protocol": "socks",
				"settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 7928}}},
			}},
			"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []any{"aimili-reality"}, "outboundTag": "aimili-socks"}}},
		},
		inbounds: []map[string]any{{
			"id": float64(1), "tag": "aimili-reality", "remark": "Aimili Reality", "protocol": "vless", "port": float64(8443),
			"settings": mustJSONString(map[string]any{"clients": []any{
				map[string]any{"id": "legacy-client", "email": "test", "flow": "xtls-rprx-vision"},
				map[string]any{"id": "subscription-client", "email": "aimili-gateway-subscription", "flow": "xtls-rprx-vision"},
			}}),
			"streamSettings": mustJSONString(map[string]any{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]any{
					"target": "127.0.0.1:443", "serverNames": []any{"proxy.example.test"}, "privateKey": "private",
					"shortIds": []any{"short"}, "settings": map[string]any{"publicKey": "public"},
				},
			}),
		}},
	}
	client := newXUIFixtureClient(t, fixture)
	managed, err := client.EnsureLegacyMain(context.Background(), LegacyMainDesired{
		VLESSPort: 8443, MixedPort: 31000, SOCKSPort: 7928, VLESSClientID: "11111111-2222-4333-8444-555555555555", MixedUsername: "user", MixedPassword: "password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if managed.VLESSInboundID != 1 || managed.MixedInboundID == 0 || managed.ClientID != "legacy-client" {
		t.Fatalf("legacy main=%#v", managed)
	}
	if len(fixture.addedProtocols) != 1 || fixture.addedProtocols[0] != "mixed" {
		t.Fatalf("added protocols=%#v", fixture.addedProtocols)
	}
	if len(fixture.updatedInboundIDs) != 0 {
		t.Fatalf("already migrated 8443 was unexpectedly updated: %v", fixture.updatedInboundIDs)
	}
	updatesBefore := fixture.updatedXrayCalls
	if _, err := client.EnsureLegacyMain(context.Background(), LegacyMainDesired{
		VLESSPort: 8443, MixedPort: 31000, SOCKSPort: 7928, MixedUsername: "user", MixedPassword: "password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.updatedXrayCalls != updatesBefore {
		t.Fatal("idempotent main inspection unexpectedly rewrote Xray settings")
	}
}

func TestEnsureLegacyMainBootstrapsAnEmptyOwnedChain(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)

	managed, err := client.EnsureLegacyMain(context.Background(), LegacyMainDesired{
		VLESSPort: 8443, MixedPort: 31000, SOCKSPort: 7928, VLESSClientID: "11111111-2222-4333-8444-555555555555", MixedUsername: "user", MixedPassword: "password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if managed.VLESSInboundID == 0 || managed.MixedInboundID == 0 || managed.ClientID == "" || managed.PublicKey == "" || managed.ShortID == "" {
		t.Fatalf("fresh main is incomplete: %#v", managed)
	}
	if managed.ClientID != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("fresh main client ID = %q", managed.ClientID)
	}
	tags := map[string]bool{}
	listed := make([]Inbound, 0, len(fixture.inbounds))
	for _, inbound := range fixture.inbounds {
		tags[stringValue(inbound["tag"])] = true
		port, _ := inbound["port"].(int)
		if numeric, ok := inbound["port"].(float64); ok {
			port = int(numeric)
		}
		listed = append(listed, Inbound{Tag: stringValue(inbound["tag"]), Remark: stringValue(inbound["remark"]), Protocol: stringValue(inbound["protocol"]), Port: port})
	}
	if !tags["aimili-reality"] || !tags["agw-main-mixed"] {
		t.Fatalf("fresh main inbounds missing: %#v", fixture.inbounds)
	}
	if err := verifyLegacyMainChain(listed, fixture.updatedXray, 8443, 7928); err != nil {
		t.Fatalf("fresh main chain is invalid: %v", err)
	}
}

func TestEnsureLegacyMainMigratesOnlyHistoricalRealityCoverTarget(t *testing.T) {
	legacySettings := mustJSONString(map[string]any{"clients": []any{map[string]any{"id": "legacy-client", "flow": "xtls-rprx-vision", "enable": true}}, "decryption": "none"})
	legacyStream := mustJSONString(map[string]any{
		"network": "tcp", "security": "reality",
		"realitySettings": map[string]any{
			"show": false, "xver": 0, "target": "www.microsoft.com:443",
			"serverNames": []any{"www.microsoft.com"}, "privateKey": "legacy-private", "shortIds": []any{"legacy-short"},
			"settings": map[string]any{"publicKey": "legacy-public", "fingerprint": "chrome", "spiderX": "/"},
		},
	})
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{
				map[string]any{
					"tag": "aimili-socks", "protocol": "socks",
					"settings": map[string]any{
						"servers": []any{map[string]any{"address": "127.0.0.1", "port": 7928}},
					},
				},
			},
			"routing": map[string]any{
				"rules": []any{map[string]any{"type": "field", "inboundTag": []any{"aimili-reality"}, "outboundTag": "aimili-socks"}},
			},
		},
		inbounds: []map[string]any{{
			"id": float64(1), "tag": "aimili-reality", "remark": "Aimili Reality", "protocol": "vless", "port": float64(8443),
			"enable": true, "settings": legacySettings, "streamSettings": legacyStream, "sniffing": "{}",
		}},
	}
	client := newXUIFixtureClient(t, fixture)
	managed, err := client.EnsureLegacyMain(context.Background(), LegacyMainDesired{
		VLESSPort: 8443, MixedPort: 31000, SOCKSPort: 7928, MixedUsername: "user", MixedPassword: "password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(fixture.updatedInboundIDs) != "[1]" {
		t.Fatalf("updated inbound IDs=%v", fixture.updatedInboundIDs)
	}
	updated := fixture.inbounds[0]
	if updated["settings"] != legacySettings || managed.ClientID != "legacy-client" || managed.PublicKey != "legacy-public" || managed.ShortID != "legacy-short" {
		t.Fatal("legacy client identity or Reality key material changed")
	}
	stream, ok := decodeObject(updated["streamSettings"])
	if !ok {
		t.Fatal("updated stream settings are invalid")
	}
	reality, ok := decodeObject(stream["realitySettings"])
	if !ok || reality["target"] != "127.0.0.1:443" || fmt.Sprint(reality["serverNames"]) != "[proxy.example.test]" ||
		reality["privateKey"] != "legacy-private" || fmt.Sprint(reality["shortIds"]) != "[legacy-short]" {
		t.Fatalf("unexpected migrated Reality settings: %#v", reality)
	}
}

func TestUpdateManagedGroupChangesOnlyOwnedRouting(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	beforeInbounds, _ := json.Marshal(fixture.inbounds)
	desired.MixedSourceRestrictionEnabled = false
	desired.MixedSourceCIDRs = nil
	updated, err := client.UpdateManagedGroup(context.Background(), desired, managed)
	if err != nil {
		t.Fatal(err)
	}
	afterInbounds, _ := json.Marshal(fixture.inbounds)
	if string(beforeInbounds) != string(afterInbounds) {
		t.Fatalf("routing update changed inbounds: before=%s after=%s", beforeInbounds, afterInbounds)
	}
	if updated.Fingerprint == managed.Fingerprint || updated.ResourceName != managed.ResourceName {
		t.Fatalf("updated managed group = %#v", updated)
	}
	rules := asObjectSlice(fixture.updatedXray["routing"].(map[string]any)["rules"])
	if len(rules) != 3 { // VLESS, unrestricted mixed, preserved unmanaged rule.
		t.Fatalf("updated rules = %#v", rules)
	}
}

func TestUpdateManagedGroupReturnsCurrentRealityMaterialFromObjectResponse(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	for _, inbound := range fixture.inbounds {
		if inbound["protocol"] != "vless" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound["settings"].(string)), &settings); err != nil {
			t.Fatal(err)
		}
		settings["clients"] = append(settings["clients"].([]any), map[string]any{
			"id": "subscription-client", "email": "aimili-gateway-subscription", "flow": "xtls-rprx-vision",
		})
		inbound["settings"] = settings
		var stream map[string]any
		if err := json.Unmarshal([]byte(inbound["streamSettings"].(string)), &stream); err != nil {
			t.Fatal(err)
		}
		reality := stream["realitySettings"].(map[string]any)
		reality["shortIds"] = []any{"current-short-id"}
		reality["settings"].(map[string]any)["publicKey"] = "current-public-key"
		inbound["streamSettings"] = stream
	}

	updated, err := client.UpdateManagedGroup(context.Background(), desired, managed)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PublicKey != "current-public-key" || updated.ShortID != "current-short-id" || updated.ServerName != "proxy.example.test" {
		t.Fatalf("current Reality material was not returned: %#v", updated)
	}
}

func TestCurrentRealityMaterialReadsOptionalMLDSAVerifyWithoutExposingSeed(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000, VLESSClientID: "client-id", MixedUsername: "user", MixedPassword: "password", RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test"}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	for _, inbound := range fixture.inbounds {
		if inbound["protocol"] != "vless" {
			continue
		}
		stream, _ := decodeObject(inbound["streamSettings"])
		reality, _ := decodeObject(stream["realitySettings"])
		reality["mldsa65Seed"] = "server-secret"
		settings, _ := decodeObject(reality["settings"])
		settings["mldsa65Verify"] = "client-verify"
		inbound["streamSettings"] = stream
	}
	updated, err := client.UpdateManagedGroup(context.Background(), desired, managed)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MLDSA65Verify != "client-verify" {
		t.Fatalf("verify=%q", updated.MLDSA65Verify)
	}
}

func TestUpdateManagedGroupRejectsRealityClientDriftBeforeChangingRouting(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	fixture.updatedXray = nil
	for _, inbound := range fixture.inbounds {
		if inbound["protocol"] != "vless" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound["settings"].(string)), &settings); err != nil {
			t.Fatal(err)
		}
		settings["clients"].([]any)[0].(map[string]any)["id"] = "different-client"
		inbound["settings"] = settings
	}

	_, err = client.UpdateManagedGroup(context.Background(), desired, managed)
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "managed_resource_drift" {
		t.Fatalf("unexpected drift error: %v", err)
	}
	if fixture.updatedXray != nil {
		t.Fatal("Reality drift changed Xray routing before validation")
	}
}

func TestUpdateManagedMixedPolicyPreservesHysteria2PublicInbound(t *testing.T) {
	public := map[string]any{
		"id": float64(11), "tag": "agw-jp-dc-vless", "remark": "Aimili Gateway agw-jp-dc public",
		"protocol": "hysteria", "port": float64(20000), "settings": "{}", "streamSettings": "{}",
	}
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{map[string]any{
				"tag": "agw-jp-dc-socks", "protocol": "socks",
				"settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 17930}}},
			}},
			"routing": map[string]any{"rules": []any{
				map[string]any{"type": "field", "inboundTag": []any{"agw-jp-dc-vless"}, "outboundTag": "agw-jp-dc-socks"},
				map[string]any{"type": "field", "inboundTag": []any{"agw-jp-dc-mixed"}, "outboundTag": "agw-jp-dc-socks"},
			}},
		},
		inbounds: []map[string]any{public, {
			"id": float64(12), "tag": "agw-jp-dc-mixed", "remark": "Aimili Gateway agw-jp-dc mixed",
			"protocol": "mixed", "port": float64(30000),
			"settings":       mustJSONString(map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "proxy-user", "pass": "proxy-password"}}}),
			"streamSettings": "{}",
		}},
	}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	before := cloneObject(public)
	managed, err := client.UpdateManagedMixedPolicy(context.Background(), desired, ManagedGroup{
		ResourceName: "agw-jp-dc", VLESSInboundID: 11, MixedInboundID: 12,
		VLESSInboundTag: "agw-jp-dc-vless", MixedInboundTag: "agw-jp-dc-mixed", OutboundTag: "agw-jp-dc-socks",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, fixture.inbounds[0]) || len(fixture.updatedInboundIDs) != 0 {
		t.Fatalf("mixed policy update changed public inbound: before=%#v after=%#v", before, fixture.inbounds[0])
	}
	if managed.VLESSInboundID != 11 || managed.MixedInboundID != 12 {
		t.Fatalf("managed identity changed: %#v", managed)
	}
	rules := asObjectSlice(fixture.updatedXray["routing"].(map[string]any)["rules"])
	if len(rules) != 3 || len(asStringSlice(rules[0]["source"])) != 3 {
		t.Fatalf("mixed source rules were not updated: %#v", rules)
	}
}

func TestRepairManagedPublicRecreatesMissingTCPWithoutChangingOtherResources(t *testing.T) {
	mixed := map[string]any{
		"id": float64(12), "tag": "agw-jp-dc-mixed", "remark": "Aimili Gateway agw-jp-dc mixed",
		"protocol": "mixed", "port": float64(30000),
		"settings":       mustJSONString(map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "proxy-user", "pass": "proxy-password"}}}),
		"streamSettings": "{}",
	}
	unmanaged := map[string]any{
		"id": float64(99), "tag": "personal-inbound", "remark": "Personal inbound",
		"protocol": "vless", "port": float64(24443), "settings": "{}", "streamSettings": "{}",
	}
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{
				map[string]any{"tag": "agw-jp-dc-socks", "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 17930}}}},
				map[string]any{"tag": "direct", "protocol": "freedom"},
			},
			"routing": map[string]any{"rules": []any{
				map[string]any{"type": "field", "inboundTag": []any{"agw-jp-dc-vless"}, "outboundTag": "agw-jp-dc-socks"},
				map[string]any{"type": "field", "inboundTag": []any{"personal-inbound"}, "outboundTag": "direct"},
			}},
		},
		inbounds: []map[string]any{mixed, unmanaged},
	}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	beforeMixed, beforeUnmanaged := cloneObject(mixed), cloneObject(unmanaged)
	managed, err := client.RepairManagedPublic(context.Background(), desired, ManagedGroup{
		ResourceName: "agw-jp-dc", VLESSInboundID: 11, MixedInboundID: 12,
		VLESSInboundTag: "agw-jp-dc-vless", MixedInboundTag: "agw-jp-dc-mixed", OutboundTag: "agw-jp-dc-socks",
	}, domain.ProtocolVLESSTCPRealityVision)
	if err != nil {
		t.Fatal(err)
	}
	if managed.VLESSInboundID == 0 || managed.VLESSInboundID == 11 || managed.PublicKey == "" || managed.ShortID == "" || managed.ServerName != "proxy.example.test" {
		t.Fatalf("repaired public identity = %#v", managed)
	}
	if strings.Join(fixture.addedProtocols, ",") != "vless" || fixture.updatedXrayCalls != 1 {
		t.Fatalf("public repair writes protocols=%#v xrayUpdates=%d", fixture.addedProtocols, fixture.updatedXrayCalls)
	}
	if !reflect.DeepEqual(beforeMixed, fixture.inbounds[0]) || !reflect.DeepEqual(beforeUnmanaged, fixture.inbounds[1]) {
		t.Fatalf("public repair changed unrelated resources: %#v", fixture.inbounds)
	}
}

func TestRepairManagedPublicRefusesToInventMissingNonTCPProtocol(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	_, err := client.RepairManagedPublic(context.Background(), DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}, ManagedGroup{ResourceName: "agw-jp-dc", VLESSInboundID: 11, MixedInboundID: 12, VLESSInboundTag: "agw-jp-dc-vless", MixedInboundTag: "agw-jp-dc-mixed", OutboundTag: "agw-jp-dc-socks"}, domain.ProtocolHysteria2QUICTLS)
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "managed_resource_missing" || len(fixture.addedProtocols) != 0 {
		t.Fatalf("error=%v writes=%#v", err, fixture.addedProtocols)
	}
}

func TestRepairManagedPublicReclaimsStoredOwnedTCPWithStaleTag(t *testing.T) {
	stale := map[string]any{
		"id": float64(11), "tag": "agw-old-vless", "remark": "Aimili Gateway agw-old VLESS",
		"protocol": "vless", "port": float64(20000), "settings": "{}", "streamSettings": "{}", "sniffing": "{}",
	}
	mixed := map[string]any{
		"id": float64(12), "tag": "agw-jp-dc-mixed", "remark": "Aimili Gateway agw-jp-dc mixed",
		"protocol": "mixed", "port": float64(30000),
		"settings": mustJSONString(map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "proxy-user", "pass": "proxy-password"}}}), "streamSettings": "{}",
	}
	unmanaged := map[string]any{"id": float64(99), "tag": "personal-inbound", "remark": "Personal", "protocol": "vless", "port": float64(24443), "settings": "{}", "streamSettings": "{}"}
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{
				map[string]any{"tag": "agw-jp-dc-socks", "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 17930}}}},
				map[string]any{"tag": "direct", "protocol": "freedom"},
			},
			"routing": map[string]any{"rules": []any{
				map[string]any{"type": "field", "inboundTag": []any{"agw-old-vless"}, "outboundTag": "agw-jp-dc-socks"},
				map[string]any{"type": "field", "inboundTag": []any{"personal-inbound"}, "outboundTag": "direct"},
			}},
		},
		inbounds: []map[string]any{stale, mixed, unmanaged},
	}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	beforeUnmanaged := cloneObject(unmanaged)
	managed, err := client.RepairManagedPublic(context.Background(), desired, ManagedGroup{
		ResourceName: "agw-jp-dc", VLESSInboundID: 11, MixedInboundID: 12,
		VLESSInboundTag: "agw-jp-dc-vless", MixedInboundTag: "agw-jp-dc-mixed", OutboundTag: "agw-jp-dc-socks",
	}, domain.ProtocolVLESSTCPRealityVision)
	if err != nil {
		t.Fatal(err)
	}
	if managed.VLESSInboundID != 11 || strings.Join(fixture.addedProtocols, ",") != "" || !reflect.DeepEqual(fixture.updatedInboundIDs, []int64{11}) {
		t.Fatalf("repair=%#v adds=%#v updates=%#v", managed, fixture.addedProtocols, fixture.updatedInboundIDs)
	}
	if fixture.inbounds[0]["tag"] != "agw-jp-dc-vless" || !reflect.DeepEqual(beforeUnmanaged, fixture.inbounds[2]) {
		t.Fatalf("repaired inbounds = %#v", fixture.inbounds)
	}
	rules := asObjectSlice(fixture.updatedXray["routing"].(map[string]any)["rules"])
	if !ruleContainsInbound(rules[0], "agw-jp-dc-vless") || ruleContainsInbound(rules[0], "agw-old-vless") {
		t.Fatalf("repaired routing = %#v", rules)
	}
}

func TestRepairManagedPublicReclaimsStoredOwnedTCPWithXUIAutoTag(t *testing.T) {
	stale := map[string]any{
		"id": float64(11), "tag": "in-20000-tcp", "remark": "Aimili Gateway agw-jp-dc VLESS",
		"protocol": "vless", "port": float64(20000),
		"settings": mustJSONString(map[string]any{"clients": []any{
			map[string]any{"id": "client-id", "email": "aimili-gateway-jp-dc", "flow": "xtls-rprx-vision", "enable": true},
			map[string]any{"id": "client-id", "email": "aimili-gateway-subscription", "flow": "xtls-rprx-vision", "enable": true},
		}}),
		"streamSettings": mustJSONString(map[string]any{"network": "tcp", "security": "reality"}), "sniffing": "{}",
	}
	mixed := map[string]any{
		"id": float64(12), "tag": "agw-jp-dc-mixed", "remark": "Aimili Gateway agw-jp-dc mixed",
		"protocol": "mixed", "port": float64(30000),
		"settings": mustJSONString(map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "proxy-user", "pass": "proxy-password"}}}), "streamSettings": "{}",
	}
	unmanaged := map[string]any{"id": float64(99), "tag": "personal-inbound", "remark": "Personal", "protocol": "vless", "port": float64(24443), "settings": "{}", "streamSettings": "{}"}
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{
				map[string]any{"tag": "agw-jp-dc-socks", "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 17930}}}},
				map[string]any{"tag": "direct", "protocol": "freedom"},
			},
			"routing": map[string]any{"rules": []any{
				map[string]any{"type": "field", "inboundTag": []any{"in-20000-tcp"}, "outboundTag": "agw-jp-dc-socks"},
				map[string]any{"type": "field", "inboundTag": []any{"personal-inbound"}, "outboundTag": "direct"},
			}},
		},
		inbounds: []map[string]any{stale, mixed, unmanaged},
	}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	beforeUnmanaged := cloneObject(unmanaged)
	managed, err := client.RepairManagedPublic(context.Background(), desired, ManagedGroup{
		ResourceName: "agw-jp-dc", VLESSInboundID: 11, MixedInboundID: 12,
		VLESSInboundTag: "agw-jp-dc-vless", MixedInboundTag: "agw-jp-dc-mixed", OutboundTag: "agw-jp-dc-socks",
	}, domain.ProtocolVLESSTCPRealityVision)
	if err != nil {
		t.Fatal(err)
	}
	if managed.VLESSInboundID != 11 || !reflect.DeepEqual(fixture.updatedInboundIDs, []int64{11}) || fixture.inbounds[0]["tag"] != "agw-jp-dc-vless" {
		t.Fatalf("repair=%#v updates=%#v inbound=%#v", managed, fixture.updatedInboundIDs, fixture.inbounds[0])
	}
	repairedSettings, ok := decodeObject(fixture.inbounds[0]["settings"])
	if !ok || len(asObjectSlice(repairedSettings["clients"])) != 2 {
		t.Fatalf("repair did not preserve Gateway subscription client: %#v", fixture.inbounds[0]["settings"])
	}
	if !reflect.DeepEqual(beforeUnmanaged, fixture.inbounds[2]) {
		t.Fatalf("repair changed unmanaged inbound: %#v", fixture.inbounds[2])
	}
}

func TestXUIAutoTaggedTCPBelongsToGatewayRejectsForeignClient(t *testing.T) {
	detail := inboundDetail{
		ID: 11, Tag: "in-20000-tcp", Remark: "Aimili Gateway agw-jp-dc VLESS", Protocol: "vless", Port: 20000,
		Settings: mustJSONString(map[string]any{"clients": []any{
			map[string]any{"id": "client-id", "email": "aimili-gateway-jp-dc", "flow": "xtls-rprx-vision"},
			map[string]any{"id": "foreign-id", "email": "personal-client", "flow": "xtls-rprx-vision"},
		}}),
		StreamSettings: mustJSONString(map[string]any{"network": "tcp", "security": "reality"}),
	}
	desired := DesiredGroup{ResourceName: "agw-jp-dc", VLESSPort: 20000, VLESSClientID: "client-id"}
	if xuiAutoTaggedTCPBelongsToGateway(detail, desired) {
		t.Fatal("foreign client was accepted as Gateway ownership proof")
	}
}

func TestUpdateLegacyMainMixedPolicyPreservesXHTTPPublicInbound(t *testing.T) {
	public := map[string]any{
		"id": float64(1), "tag": "aimili-reality", "remark": "Aimili Reality",
		"protocol": "vless", "port": float64(8443), "settings": "{}",
		"streamSettings": mustJSONString(map[string]any{"network": "xhttp", "security": "reality"}),
	}
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{map[string]any{
				"tag": "aimili-socks", "protocol": "socks",
				"settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 7928}}},
			}},
			"routing": map[string]any{"rules": []any{
				map[string]any{"type": "field", "inboundTag": []any{"aimili-reality"}, "outboundTag": "aimili-socks"},
				map[string]any{"type": "field", "inboundTag": []any{"agw-main-mixed"}, "outboundTag": "aimili-socks"},
			}},
		},
		inbounds: []map[string]any{public, {
			"id": float64(2), "tag": "agw-main-mixed", "remark": "Aimili Gateway main mixed",
			"protocol": "mixed", "port": float64(31000),
			"settings":       mustJSONString(map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "proxy-user", "pass": "proxy-password"}}}),
			"streamSettings": "{}",
		}},
	}
	client := newXUIFixtureClient(t, fixture)
	before := cloneObject(public)
	if err := client.UpdateLegacyMainMixedPolicy(context.Background(), LegacyMainDesired{
		VLESSPort: 8443, MixedPort: 31000, SOCKSPort: 7928,
		MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, fixture.inbounds[0]) || len(fixture.updatedInboundIDs) != 0 {
		t.Fatalf("main mixed policy update changed public inbound: before=%#v after=%#v", before, fixture.inbounds[0])
	}
	rules := asObjectSlice(fixture.updatedXray["routing"].(map[string]any)["rules"])
	if len(rules) != 3 || len(asStringSlice(rules[0]["source"])) != 3 {
		t.Fatalf("main mixed source rules were not updated: %#v", rules)
	}
}

func TestUpdateManagedGroupRebindsMissingStoredIDsToExactOwnedTags(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	for _, inbound := range fixture.inbounds {
		switch inbound["protocol"] {
		case "vless":
			inbound["id"] = float64(501)
		case "mixed":
			inbound["id"] = float64(502)
		}
	}

	updated, err := client.UpdateManagedGroup(context.Background(), desired, managed)
	if err != nil {
		t.Fatal(err)
	}
	if updated.VLESSInboundID != 501 || updated.MixedInboundID != 502 {
		t.Fatalf("missing stored IDs were not rebound: %#v", updated)
	}
}

func TestUpdateManagedGroupAdoptsUniqueOwnedPortPairWhenResourceNameDrifts(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	for _, inbound := range fixture.inbounds {
		switch inbound["protocol"] {
		case "vless":
			inbound["id"] = float64(601)
			inbound["tag"] = "agw-jp-dc-previous-vless"
		case "mixed":
			inbound["id"] = float64(602)
			inbound["tag"] = "agw-jp-dc-previous-mixed"
		}
	}
	for _, outbound := range fixture.updatedXray["outbounds"].([]any) {
		item := outbound.(map[string]any)
		if item["tag"] == "agw-jp-dc-socks" {
			item["tag"] = "agw-jp-dc-previous-socks"
		}
	}

	updated, err := client.UpdateManagedGroup(context.Background(), desired, managed)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResourceName != "agw-jp-dc-previous" || updated.VLESSInboundID != 601 || updated.MixedInboundID != 602 ||
		updated.VLESSInboundTag != "agw-jp-dc-previous-vless" || updated.MixedInboundTag != "agw-jp-dc-previous-mixed" || updated.OutboundTag != "agw-jp-dc-previous-socks" {
		t.Fatalf("owned port pair was not adopted: %#v", updated)
	}
	if fixture.updatedXray["outbounds"].([]any)[1].(map[string]any)["tag"] != "agw-jp-dc-previous-socks" {
		t.Fatalf("adoption created a second managed outbound: %#v", fixture.updatedXray["outbounds"])
	}
}

func TestUpdateManagedGroupRepairsDifferentMixedAccountBeforeRouting(t *testing.T) {
	fixture := &xuiFixture{}
	client := newXUIFixtureClient(t, fixture)
	desired := DesiredGroup{
		ResourceName: "agw-jp-dc", SOCKSPort: 17930, VLESSPort: 20000, MixedPort: 30000,
		VLESSClientID: "client-id", MixedUsername: "proxy-user", MixedPassword: "proxy-password",
		MixedSourceRestrictionEnabled: true, MixedSourceCIDRs: []string{"198.51.100.0/24"},
		RealityTarget: "127.0.0.1:443", RealityServerName: "proxy.example.test",
	}
	managed, err := client.EnsureManagedGroup(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	fixture.updatedXray = nil
	for _, inbound := range fixture.inbounds {
		if inbound["protocol"] != "mixed" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound["settings"].(string)), &settings); err != nil {
			t.Fatal(err)
		}
		settings["accounts"].([]any)[0].(map[string]any)["pass"] = "different-password"
		inbound["settings"] = settings
	}

	if _, err = client.UpdateManagedGroup(context.Background(), desired, managed); err != nil {
		t.Fatal(err)
	}
	if fixture.updatedXray == nil || len(fixture.updatedInboundIDs) != 1 {
		t.Fatalf("mixed account was not repaired before routing: inbound=%v xray=%#v", fixture.updatedInboundIDs, fixture.updatedXray)
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
	if len(fixture.deletedClients) != 1 || fixture.deletedClients[0] != "aimili-gateway-jp-dc" {
		t.Fatalf("managed client was not fully deleted: %#v", fixture.deletedClients)
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
	if len(fixture.deletedClients) != 1 || fixture.deletedClients[0] != "aimili-gateway-jp-dc" {
		t.Fatalf("partial client was not fully rolled back: %#v", fixture.deletedClients)
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
