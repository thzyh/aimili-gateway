package validator

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBuildVLESSClientConfigUsesOnlyLoopbackServerAndLocalSOCKS(t *testing.T) {
	target := VLESSTarget{
		InboundAddress: "127.0.0.1:20000",
		ClientID:       "test-client-id",
		PublicKey:      "test-public-key",
		ShortID:        "test-short-id",
		ServerName:     "www.microsoft.com",
	}
	encoded, err := buildVLESSClientConfig(target, 19080, "ephemeral-user", "ephemeral-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(`"address":"127.0.0.1"`),
		[]byte(`"port":20000`),
		[]byte(`"listen":"127.0.0.1"`),
		[]byte(`"port":19080`),
		[]byte("test-client-id"),
		[]byte("test-public-key"),
		[]byte("ephemeral-password"),
	} {
		if !bytes.Contains(encoded, marker) {
			t.Fatalf("generated config missing required field %q", marker)
		}
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	outbound := document["outbounds"].([]any)[0].(map[string]any)
	stream := outbound["streamSettings"].(map[string]any)
	reality := stream["realitySettings"].(map[string]any)
	if reality["password"] != "test-public-key" {
		t.Fatalf("Reality password = %#v", reality["password"])
	}
	if _, exists := reality["publicKey"]; exists {
		t.Fatal("legacy Reality publicKey field was emitted")
	}
}

func TestBuildVLESSClientConfigRejectsNonLoopbackServer(t *testing.T) {
	_, err := buildVLESSClientConfig(VLESSTarget{
		InboundAddress: "192.0.2.10:20000", ClientID: "id", PublicKey: "key",
		ShortID: "short", ServerName: "www.microsoft.com",
	}, 19080, "user", "password")
	if codeOf(err) != "invalid_configuration" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildVLESSClientConfigIncludesMLDSAVerifyOnlyWhenProvided(t *testing.T) {
	target := VLESSTarget{InboundAddress: "127.0.0.1:20000", ClientID: "id", PublicKey: "key", ShortID: "short", ServerName: "proxy.example.test", MLDSA65Verify: "verify-material"}
	encoded, err := buildVLESSClientConfig(target, 19080, "user", "password")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(encoded, &document) != nil {
		t.Fatal("invalid JSON")
	}
	outbound := document["outbounds"].([]any)[0].(map[string]any)
	reality := outbound["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)
	if reality["mldsa65Verify"] != "verify-material" {
		t.Fatalf("reality=%#v", reality)
	}
}

func TestBuildVLESSClientConfigRejectsUnsafeMLDSAVerify(t *testing.T) {
	_, err := buildVLESSClientConfig(VLESSTarget{InboundAddress: "127.0.0.1:20000", ClientID: "id", PublicKey: "key", ShortID: "short", ServerName: "proxy.example.test", MLDSA65Verify: "bad\nvalue"}, 19080, "user", "password")
	if err == nil {
		t.Fatal("unsafe ML-DSA verify was accepted")
	}
}
