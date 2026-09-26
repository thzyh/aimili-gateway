package aimili

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoveryAndDynamicStandbys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer")
		}
		switch r.URL.Path {
		case "/control/v1/recovery":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"settings": map[string]any{"failureThreshold": 3}, "standbyTargetCount": 4, "activeTargetCount": 4, "ipQualityStatus": "not_implemented"}})
		case "/control/v1/standbys":
			rows := []DedicatedStandby{}
			for i := 0; i < 4; i++ {
				rows = append(rows, DedicatedStandby{Index: i, Target: "main", Status: "retry_wait"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": rows})
		case "/control/v1/standbys/3/retry":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/", []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.Recovery(context.Background())
	if err != nil || state.StandbyTargetCount != 4 {
		t.Fatalf("recovery: %#v %v", state, err)
	}
	rows, err := client.DedicatedStandbys(context.Background())
	if err != nil || len(rows) != 4 {
		t.Fatalf("standbys: %#v %v", rows, err)
	}
	if err := client.RetryDedicatedStandby(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if validDedicatedStandbys([]DedicatedStandby{{Index: 65, Status: "ready"}}) {
		t.Fatal("out of range ID accepted")
	}
}
