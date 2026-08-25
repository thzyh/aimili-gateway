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
