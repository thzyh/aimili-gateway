package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildUIBundleIsDeterministic(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": `<script src="/assets/index-a.js"></script>`, "assets/index-a.js": "ok"} {
		if err := os.WriteFile(filepath.Join(dist, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	first, err := buildUIBundle(dist, privateKey, "abc1234", "2026-09-05T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildUIBundle(dist, privateKey, "abc1234", "2026-09-05T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Archive, second.Archive) || !bytes.Equal(first.Manifest, second.Manifest) || !bytes.Equal(first.Signature, second.Signature) {
		t.Fatal("identical UI input produced different release assets")
	}
}

func TestRunRejectsPrivateKeyInsideOutputDirectory(t *testing.T) {
	dist := t.TempDir()
	output := t.TempDir()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(output, "signing.key")
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"ui", "--dist", dist, "--private-key", keyFile, "--out", output, "--commit", "abc1234", "--built-at", "2026-09-05T00:00:00Z"}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunKeygenCreatesMatchingKeysWithoutOverwrite(t *testing.T) {
	directory := t.TempDir()
	privatePath := filepath.Join(directory, "ui.key")
	publicPath := filepath.Join(directory, "ui.pub")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"keygen", "--private", privatePath, "--public", publicPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("keygen code=%d stderr=%q", code, stderr.String())
	}
	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	publicRaw, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := hex.DecodeString(string(bytes.TrimSpace(privateRaw)))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := hex.DecodeString(string(bytes.TrimSpace(publicRaw)))
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("release")
	if !ed25519.Verify(publicKey, message, ed25519.Sign(privateKey, message)) {
		t.Fatal("generated key pair does not match")
	}
	if code := run([]string{"keygen", "--private", privatePath, "--public", publicPath}, &bytes.Buffer{}, &bytes.Buffer{}); code == 0 {
		t.Fatal("keygen overwrote an existing key pair")
	}
}
