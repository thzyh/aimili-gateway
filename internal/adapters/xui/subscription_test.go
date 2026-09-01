package xui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type subscriptionFixture struct {
	client      map[string]any
	added       map[string]any
	attached    []int64
	settingPath int
	settingVerb string
	attachCalls int
	aliasWrites [][]map[string]any
}

func (f *subscriptionFixture) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/panel/csrf-token":
		fmt.Fprint(w, `{"success":true,"obj":"csrf"}`)
	case "/panel/login":
		fmt.Fprint(w, `{"success":true,"obj":null}`)
	case "/panel/panel/api/xray/":
		fmt.Fprint(w, `{"success":true,"obj":{"xraySetting":"{\"outbounds\":[],\"routing\":{\"rules\":[]}}","outboundTestUrl":"https://probe.invalid/"}}`)
	case "/panel/panel/api/inbounds/list":
		fmt.Fprint(w, `{"success":true,"obj":[
			{"id":1,"tag":"aimili-reality","remark":"Aimili Reality","protocol":"vless","port":8443,"settings":"{\"clients\":[{\"id\":\"stable-client\",\"email\":\"aimili-gateway-subscription\",\"flow\":\"xtls-rprx-vision\"}]}","streamSettings":"{\"network\":\"tcp\",\"security\":\"reality\",\"realitySettings\":{\"serverNames\":[\"proxy.example.test\"],\"shortIds\":[\"short-one\"],\"settings\":{\"publicKey\":\"public-one\",\"fingerprint\":\"chrome\"}}}"},
			{"id":2,"tag":"agw-jp-dc-vless","remark":"Aimili Gateway agw-jp-dc VLESS","protocol":"vless","port":20000,"settings":"{\"clients\":[{\"id\":\"stable-client\",\"email\":\"aimili-gateway-subscription\",\"flow\":\"\"}]}","streamSettings":"{\"network\":\"xhttp\",\"security\":\"reality\",\"xhttpSettings\":{\"path\":\"/safe-xhttp-path\",\"mode\":\"auto\"},\"realitySettings\":{\"serverNames\":[\"proxy.example.test\"],\"shortIds\":[\"short-two\"],\"settings\":{\"publicKey\":\"public-two\",\"fingerprint\":\"chrome\"}}}"},
            {"id":3,"tag":"agw-jp-dc-mixed","remark":"Aimili Gateway agw-jp-dc mixed","protocol":"mixed","port":30000},
			{"id":4,"tag":"user-vless","remark":"User VLESS","protocol":"vless","port":40000},
			{"id":5,"tag":"agw-us-dc-vless","remark":"Aimili Gateway agw-us-dc VLESS","protocol":"hysteria","port":20001,"settings":"{\"version\":2,\"clients\":[{\"auth\":\"stable-auth\",\"email\":\"aimili-gateway-subscription\"}]}","streamSettings":"{\"network\":\"hysteria\",\"security\":\"tls\",\"hysteriaSettings\":{\"version\":2}}"}
        ]}`)
	case "/panel/panel/api/setting/all":
		f.settingVerb = r.Method
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprint(w, `{"success":false,"msg":"method not allowed"}`)
			return
		}
		f.settingPath++
		fmt.Fprint(w, `{"success":true,"obj":{"subPath":"/sub-test/"}}`)
	default:
		switch {
		case strings.HasPrefix(r.URL.Path, "/panel/panel/api/clients/get/"):
			if f.client == nil {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `not found`)
				return
			}
			body, _ := json.Marshal(map[string]any{"success": true, "obj": f.client})
			_, _ = w.Write(body)
		case r.URL.Path == "/panel/panel/api/clients/add":
			if err := json.NewDecoder(r.Body).Decode(&f.added); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			client, _ := f.added["client"].(map[string]any)
			f.client = map[string]any{"id": 42, "email": client["email"], "subId": client["subId"], "client": client, "inboundIds": []any{}}
			fmt.Fprint(w, `{"success":true,"obj":null}`)
		case strings.HasPrefix(r.URL.Path, "/panel/panel/api/clients/") && strings.HasSuffix(r.URL.Path, "/attach"):
			f.attachCalls++
			var payload struct {
				InboundIDs []int64 `json:"inboundIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.attached = append([]int64(nil), payload.InboundIDs...)
			values := make([]any, len(payload.InboundIDs))
			for i, id := range payload.InboundIDs {
				values[i] = id
			}
			f.client["inboundIds"] = values
			fmt.Fprint(w, `{"success":true,"obj":null}`)
		case strings.HasPrefix(r.URL.Path, "/panel/panel/api/clients/") && strings.HasSuffix(r.URL.Path, "/inboundAliases"):
			var payload struct {
				Aliases []map[string]any `json:"aliases"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.aliasWrites = append(f.aliasWrites, payload.Aliases)
			f.client["inboundAliases"] = payload.Aliases
			fmt.Fprint(w, `{"success":true,"obj":null}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestEnsureSubscriptionClientSetsAndVerifiesOwnedAliases(t *testing.T) {
	fixture := &subscriptionFixture{client: map[string]any{
		"id": 42, "email": "aimili-gateway-subscription", "subId": "stable-sub",
		"client": map[string]any{"id": "stable-client"}, "inboundIds": []any{1, 2},
	}}
	client := newSubscriptionFixtureClient(t, fixture)
	want := map[int64]string{1: "主连接_日本", 2: "出口位 1_日本"}

	subscription, err := client.EnsureSubscriptionClient(context.Background(), SubscriptionDesired{
		ClientEmail: "aimili-gateway-subscription", ClientUUID: "stable-client", InboundIDs: []int64{1, 2}, Aliases: want,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(subscription.Aliases, want) {
		t.Fatalf("aliases = %#v, want %#v", subscription.Aliases, want)
	}
	if len(fixture.aliasWrites) != 1 || len(fixture.aliasWrites[0]) != 2 {
		t.Fatalf("alias writes = %#v", fixture.aliasWrites)
	}
}

func TestEnsureSubscriptionClientRejectsUnownedAliasBeforeAnyClientWrite(t *testing.T) {
	fixture := &subscriptionFixture{}
	client := newSubscriptionFixtureClient(t, fixture)

	_, err := client.EnsureSubscriptionClient(context.Background(), SubscriptionDesired{
		ClientEmail: "aimili-gateway-subscription", ClientUUID: "stable-client", InboundIDs: []int64{1, 2, 4},
		Aliases: map[int64]string{1: "主连接_日本", 2: "出口位 1_日本", 4: "出口位 2_美国"},
	})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "invalid_request" {
		t.Fatalf("error = %v", err)
	}
	if fixture.added != nil || fixture.attachCalls != 0 || len(fixture.aliasWrites) != 0 {
		t.Fatalf("unowned alias caused writes: added=%#v attach=%d aliases=%#v", fixture.added, fixture.attachCalls, fixture.aliasWrites)
	}
}

func TestEnsureSubscriptionClientReadsMixedPublicProfilesWithoutReattaching(t *testing.T) {
	fixture := &subscriptionFixture{client: map[string]any{
		"id": 42, "email": "aimili-gateway-subscription", "subId": "stable-sub",
		"uuid": "stable-client", "auth": "stable-auth", "inboundIds": []any{1, 2, 5},
	}}
	client := newSubscriptionFixtureClient(t, fixture)
	subscription, err := client.EnsureSubscriptionClient(context.Background(), SubscriptionDesired{
		ClientEmail: "aimili-gateway-subscription", ClientUUID: "stable-client", InboundIDs: []int64{1, 2, 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.attachCalls != 0 {
		t.Fatalf("stable attachment set was rewritten %d times", fixture.attachCalls)
	}
	if len(subscription.PublicProfiles) != 3 {
		t.Fatalf("public profile count = %d", len(subscription.PublicProfiles))
	}
	wantModes := []string{"vless_tcp_reality_vision", "vless_xhttp_reality", "hysteria2_quic_tls"}
	for index, profile := range subscription.PublicProfiles {
		if profile.InboundID != []int64{1, 2, 5}[index] || string(profile.Mode) != wantModes[index] {
			t.Fatalf("profile %d has wrong identity or mode", index)
		}
	}
	if subscription.PublicProfiles[1].XHTTPPath != "/safe-xhttp-path" || subscription.PublicProfiles[2].Auth != "stable-auth" {
		t.Fatal("protocol-specific transient material was not resolved")
	}
	if subscription.PublicProfiles[0].ClientID != "stable-client" || subscription.PublicProfiles[1].ClientID != "stable-client" || subscription.PublicProfiles[2].ClientID != "" {
		t.Fatal("protocol profile identities were not isolated by protocol")
	}
}

func newSubscriptionFixtureClient(t *testing.T, fixture *subscriptionFixture) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL+"/panel/", Credentials{Username: "automation", Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestEnsureSubscriptionClientCreatesAndAttachesOnlyOwnedVLESS(t *testing.T) {
	fixture := &subscriptionFixture{}
	client := newSubscriptionFixtureClient(t, fixture)
	subscription, err := client.EnsureSubscriptionClient(context.Background(), SubscriptionDesired{
		ClientEmail: "aimili-gateway-subscription",
		ClientUUID:  "subscription-client-id",
		InboundIDs:  []int64{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if subscription.ResourceName != "aimili-gateway-subscription" || subscription.ClientID != 42 || subscription.ClientUUID != "subscription-client-id" {
		t.Fatalf("unexpected subscription: %#v", subscription)
	}
	if got := fmt.Sprint(fixture.attached); got != "[1 2]" {
		t.Fatalf("attached inbound IDs = %s, want [1 2]", got)
	}
	if got := fmt.Sprint(subscription.InboundIDs); got != "[1 2]" {
		t.Fatalf("subscription inbound IDs = %s, want [1 2]", got)
	}
	if fixture.added == nil {
		t.Fatal("subscription client was not created")
	}
	if subscription.SubscriptionPath != "/sub-test/" || fixture.settingVerb != http.MethodPost {
		t.Fatalf("subscription settings path=%q method=%q", subscription.SubscriptionPath, fixture.settingVerb)
	}
}

func TestEnsureSubscriptionClientIsIdempotentAndPreservesClientIdentity(t *testing.T) {
	fixture := &subscriptionFixture{client: map[string]any{"id": 42, "email": "aimili-gateway-subscription", "subId": "stable-sub", "client": map[string]any{"id": "stable-client"}, "inboundIds": []any{1}}}
	client := newSubscriptionFixtureClient(t, fixture)
	subscription, err := client.EnsureSubscriptionClient(context.Background(), SubscriptionDesired{
		ClientEmail: "aimili-gateway-subscription", ClientUUID: "stable-client", InboundIDs: []int64{1, 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.added != nil {
		t.Fatal("existing subscription client was recreated")
	}
	if subscription.SubscriptionID != "stable-sub" || subscription.ClientUUID != "stable-client" {
		t.Fatalf("identity changed: %#v", subscription)
	}
}

func TestEnsureSubscriptionClientRejectsInvalidManagedName(t *testing.T) {
	client := newSubscriptionFixtureClient(t, &subscriptionFixture{})
	_, err := client.EnsureSubscriptionClient(context.Background(), SubscriptionDesired{ClientEmail: "test", ClientUUID: "id", InboundIDs: []int64{1}})
	var adapterError *AdapterError
	if err == nil || !strings.Contains(err.Error(), "invalid_request") || !errors.As(err, &adapterError) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubscriptionURLUsesConfiguredRelativePathWithoutLeakingSecrets(t *testing.T) {
	client := newSubscriptionFixtureClient(t, &subscriptionFixture{})
	path, err := client.SubscriptionURL(context.Background(), Subscription{ResourceName: managedSubscriptionEmail, SubscriptionID: "opaque-sub-id", SubscriptionPath: "/sub-test/"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/sub-test/opaque-sub-id" {
		t.Fatalf("subscription path = %q", path)
	}
	if _, err := client.SubscriptionURL(context.Background(), Subscription{ResourceName: managedSubscriptionEmail, SubscriptionID: "bad/id"}); err == nil {
		t.Fatal("path traversal token was accepted")
	}
}

func TestValidateSubscriptionCoverageRejectsPortSetDrift(t *testing.T) {
	if err := ValidateSubscriptionCoverage([]int64{1, 2}, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSubscriptionCoverage([]int64{1}, []int64{1, 2}); err == nil {
		t.Fatal("missing inbound was accepted")
	}
}

func TestDeleteManagedAggregateRemovesOnlyOwnedResources(t *testing.T) {
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{
				map[string]any{"tag": "direct", "protocol": "freedom"},
				map[string]any{"tag": "agw-jp-dc-socks", "protocol": "socks"},
			},
			"routing": map[string]any{
				"rules": []any{
					map[string]any{"type": "field", "inboundTag": []any{"agw-aggregate-vless-vless"}, "balancerTag": "agw-aggregate"},
					map[string]any{"type": "field", "domain": []any{"user.example"}, "outboundTag": "direct"},
				},
				"balancers": []any{map[string]any{"tag": "agw-aggregate", "selector": []any{"agw-jp-dc-socks", "aimili-socks"}, "strategy": map[string]any{"type": "leastPing"}}},
			},
			"observatory": map[string]any{"subjectSelector": []any{"agw-jp-dc-socks", "aimili-socks"}, "probeURL": "https://www.google.com/generate_204", "probeInterval": "30s", "enableConcurrency": true},
		},
		inbounds: []map[string]any{{"id": float64(9), "tag": "agw-aggregate-vless-vless", "remark": "Aimili Gateway aggregate VLESS", "protocol": "vless", "port": float64(21000), "settings": `{"clients":[{"id":"opaque","email":"aimili-gateway-aggregate","flow":"xtls-rprx-vision"}]}`}},
	}
	client := newXUIFixtureClient(t, fixture)
	err := client.DeleteManagedAggregate(context.Background(), ManagedAggregate{ResourceName: "agw-aggregate-vless", VLESSInboundID: 9, VLESSInboundTag: "agw-aggregate-vless-vless", VLESSPort: 21000})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.inbounds) != 0 || len(fixture.deletedClients) != 1 || fixture.deletedClients[0] != "aimili-gateway-aggregate" {
		t.Fatalf("aggregate resources remain: inbounds=%#v clients=%#v", fixture.inbounds, fixture.deletedClients)
	}
	rules := asObjectSlice(fixture.updatedXray["routing"].(map[string]any)["rules"])
	if len(rules) != 1 || stringValue(rules[0]["outboundTag"]) != "direct" {
		t.Fatalf("unmanaged routing changed: %#v", rules)
	}
	if len(asObjectSlice(fixture.updatedXray["routing"].(map[string]any)["balancers"])) != 0 {
		t.Fatalf("legacy balancer remains: %#v", fixture.updatedXray["routing"])
	}
	if _, exists := fixture.updatedXray["observatory"]; exists {
		t.Fatalf("legacy observatory remains: %#v", fixture.updatedXray["observatory"])
	}
}

func TestDeleteManagedAggregateTreatsFullyAbsentLegacyResourcesAsAlreadyRemoved(t *testing.T) {
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
			"routing": map[string]any{
				"rules": []any{map[string]any{"type": "field", "domain": []any{"user.example"}, "outboundTag": "direct"}},
			},
		},
	}
	client := newXUIFixtureClient(t, fixture)
	err := client.DeleteManagedAggregate(context.Background(), ManagedAggregate{ResourceName: "agw-aggregate-vless", VLESSInboundID: 9, VLESSInboundTag: "agw-aggregate-vless-vless", VLESSPort: 21000})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.deletedClients) != 0 || fixture.updatedXray != nil {
		t.Fatalf("already-absent cleanup wrote state: clients=%v updated=%#v", fixture.deletedClients, fixture.updatedXray)
	}
}

func TestDeleteManagedAggregateRejectsPartialLegacyResidueWithoutInbound(t *testing.T) {
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"routing": map[string]any{
				"rules":     []any{map[string]any{"type": "field", "inboundTag": []any{"agw-aggregate-vless-vless"}, "balancerTag": "agw-aggregate"}},
				"balancers": []any{map[string]any{"tag": "agw-aggregate", "selector": []any{"agw-jp-dc-socks"}, "strategy": map[string]any{"type": "leastPing"}}},
			},
		},
	}
	client := newXUIFixtureClient(t, fixture)
	err := client.DeleteManagedAggregate(context.Background(), ManagedAggregate{ResourceName: "agw-aggregate-vless", VLESSInboundID: 9, VLESSInboundTag: "agw-aggregate-vless-vless", VLESSPort: 21000})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "managed_resource_drift" || fixture.updatedXray != nil {
		t.Fatalf("partial residue was not rejected: err=%v updated=%#v", err, fixture.updatedXray)
	}
}

func TestDeleteManagedAggregateRejectsClientSharedWithUnmanagedInbound(t *testing.T) {
	fixture := &xuiFixture{
		initialXray: map[string]any{
			"routing": map[string]any{
				"rules":     []any{map[string]any{"type": "field", "inboundTag": []any{"agw-aggregate-vless-vless"}, "balancerTag": "agw-aggregate"}},
				"balancers": []any{map[string]any{"tag": "agw-aggregate", "selector": []any{"agw-jp-dc-socks"}, "strategy": map[string]any{"type": "leastPing"}}},
			},
			"observatory": map[string]any{"subjectSelector": []any{"agw-jp-dc-socks"}, "probeURL": "https://www.google.com/generate_204"},
		},
		inbounds: []map[string]any{
			{"id": float64(9), "tag": "agw-aggregate-vless-vless", "remark": "Aimili Gateway aggregate VLESS", "protocol": "vless", "port": float64(21000), "settings": `{"clients":[{"id":"opaque","email":"aimili-gateway-aggregate","flow":"xtls-rprx-vision"}]}`},
			{"id": float64(10), "tag": "user-vless", "remark": "User VLESS", "protocol": "vless", "port": float64(22000), "settings": `{"clients":[{"id":"opaque","email":"aimili-gateway-aggregate","flow":"xtls-rprx-vision"}]}`},
		},
	}
	client := newXUIFixtureClient(t, fixture)
	err := client.DeleteManagedAggregate(context.Background(), ManagedAggregate{ResourceName: "agw-aggregate-vless", VLESSInboundID: 9, VLESSInboundTag: "agw-aggregate-vless-vless", VLESSPort: 21000})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || adapterError.Code != "ownership_conflict" || len(fixture.deletedClients) != 0 || len(fixture.inbounds) != 2 || fixture.updatedXray != nil {
		t.Fatalf("unsafe shared client cleanup: err=%v clients=%v inbounds=%v updated=%#v", err, fixture.deletedClients, fixture.inbounds, fixture.updatedXray)
	}
}
