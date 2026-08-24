package auth

import (
	"bytes"
	"testing"
)

func TestSealRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	sealed, err := Seal(key, []byte("local test secret"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != "local test secret" {
		t.Fatalf("opened plaintext = %q", opened)
	}
}

func TestSealUsesUniqueNonces(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	first, err := Seal(key, []byte("same plaintext"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Seal(key, []byte("same plaintext"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("ciphertexts are identical")
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	sealed, err := Seal(key, []byte("local test secret"))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := Open(key, sealed); err == nil {
		t.Fatal("tampering accepted")
	}
}

func TestSecretboxRejectsInvalidInputs(t *testing.T) {
	if _, err := Seal([]byte("short"), []byte("local test secret")); err == nil {
		t.Fatal("short seal key accepted")
	}
	key := bytes.Repeat([]byte{1}, 32)
	if _, err := Open(key, []byte{1}); err == nil {
		t.Fatal("truncated ciphertext accepted")
	}
	if _, err := Open(key, []byte{2, 0, 0, 0, 0}); err == nil {
		t.Fatal("unknown ciphertext version accepted")
	}
}
