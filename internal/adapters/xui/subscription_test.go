package xui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type subscriptionFixture struct {
	client      map[string]any
	added       map[string]any
	attached    []int64
	settingPath int
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
            {"id":1,"tag":"aimili-reality","remark":"Aimili Reality","protocol":"vless","port":8443},
            {"id":2,"tag":"agw-jp-dc-vless","remark":"Aimili Gateway agw-jp-dc VLESS","protocol":"vless","port":20000},
            {"id":3,"tag":"agw-jp-dc-mixed","remark":"Aimili Gateway agw-jp-dc mixed","protocol":"mixed","port":30000},
            {"id":4,"tag":"user-vless","remark":"User VLESS","protocol":"vless","port":40000}
        ]}`)
	case "/panel/panel/api/setting/all":
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
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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
			"routing": map[string]any{"rules": []any{
				map[string]any{"type": "field", "inboundTag": []any{"agw-aggregate-vless-vless"}, "balancerTag": "agw-aggregate"},
				map[string]any{"type": "field", "domain": []any{"user.example"}, "outboundTag": "direct"},
			}},
		},
		inbounds: []map[string]any{{"id": float64(9), "tag": "agw-aggregate-vless-vless", "remark": "Aimili Gateway aggregate VLESS", "protocol": "vless", "port": float64(21000)}},
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
}
