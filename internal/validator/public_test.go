package validator

import (
	"encoding/json"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

func TestBuildPublicClientConfigSelectsTCPAndXHTTPReality(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    domain.ProtocolMode
		network string
		flow    string
		path    string
	}{
		{name: "tcp", mode: domain.ProtocolVLESSTCPRealityVision, network: "tcp", flow: "xtls-rprx-vision"},
		{name: "xhttp", mode: domain.ProtocolVLESSXHTTPReality, network: "xhttp", flow: "", path: "/opaque-test-path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := PublicTarget{
				Mode: test.mode, InboundAddress: "127.0.0.1:20000", ClientID: "test-client-id",
				PublicKey: "test-public-key", ShortID: "test-short-id", ServerName: "proxy.example.test", XHTTPPath: test.path,
			}
			encoded, err := buildPublicClientConfig(target, 19080, "ephemeral-user", "ephemeral-password")
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			outbound := document["outbounds"].([]any)[0].(map[string]any)
			stream := outbound["streamSettings"].(map[string]any)
			user := outbound["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)
			if outbound["protocol"] != "vless" || stream["network"] != test.network || user["flow"] != test.flow {
				t.Fatalf("wrong public config mode: protocol=%v network=%v flow=%v", outbound["protocol"], stream["network"], user["flow"])
			}
			if test.path != "" && stream["xhttpSettings"].(map[string]any)["path"] != test.path {
				t.Fatal("XHTTP path was not preserved")
			}
		})
	}
}

func TestBuildPublicClientConfigUsesHysteria2AuthAndTLSServerName(t *testing.T) {
	encoded, err := buildPublicClientConfig(PublicTarget{
		Mode: domain.ProtocolHysteria2QUICTLS, InboundAddress: "127.0.0.1:20001",
		Auth: "test-independent-auth", TLSServerName: "192.168.88.4",
	}, 19080, "ephemeral-user", "ephemeral-password")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	outbound := document["outbounds"].([]any)[0].(map[string]any)
	settings := outbound["settings"].(map[string]any)
	stream := outbound["streamSettings"].(map[string]any)
	hysteria := stream["hysteriaSettings"].(map[string]any)
	if outbound["protocol"] != "hysteria" || settings["version"] != float64(2) || settings["address"] != "127.0.0.1" || settings["port"] != float64(20001) || hysteria["auth"] != "test-independent-auth" {
		t.Fatal("Hysteria2 server contract is incomplete")
	}
	if stream["network"] != "hysteria" || stream["security"] != "tls" || stream["tlsSettings"].(map[string]any)["serverName"] != "192.168.88.4" {
		t.Fatal("Hysteria2 TLS contract is incomplete")
	}
	if _, exists := settings["servers"]; exists {
		t.Fatal("Hysteria2 used an incompatible multi-server wrapper")
	}
	if _, exists := settings["id"]; exists {
		t.Fatal("Hysteria2 reused a VLESS UUID")
	}
}

func TestBuildPublicClientConfigRejectsCrossProtocolOrUnsafeMaterial(t *testing.T) {
	tests := []PublicTarget{
		{Mode: domain.ProtocolVLESSXHTTPReality, InboundAddress: "127.0.0.1:20000", ClientID: "id", PublicKey: "key", ShortID: "short", ServerName: "proxy.example.test"},
		{Mode: domain.ProtocolHysteria2QUICTLS, InboundAddress: "127.0.0.1:20001", Auth: "", TLSServerName: "proxy.example.test"},
		{Mode: domain.ProtocolHysteria2QUICTLS, InboundAddress: "127.0.0.1:20001", Auth: "bad\nsecret", TLSServerName: "proxy.example.test"},
	}
	for _, target := range tests {
		if _, err := buildPublicClientConfig(target, 19080, "user", "password"); codeOf(err) != "invalid_configuration" {
			t.Fatalf("unsafe target accepted for mode %q: %v", target.Mode, err)
		}
	}
}
