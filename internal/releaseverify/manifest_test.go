package releaseverify

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

func TestVerifyUIAcceptsDetachedSignature(t *testing.T) {
	manifest, signature, archive, publicKey := signedUIFixture(t)
	parsed, err := VerifyUI(manifest, signature, archive, publicKey, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Kind != "ui" || parsed.APIVersion != "v1" || parsed.Archive.Bytes != int64(len(archive)) {
		t.Fatalf("verified manifest = %#v", parsed)
	}
}

func TestVerifyUIRejectsChangedManifest(t *testing.T) {
	manifest, signature, archive, publicKey := signedUIFixture(t)
	manifest[len(manifest)-2] ^= 1
	if _, err := VerifyUI(manifest, signature, archive, publicKey, "v1"); ErrorCode(err) != "invalid_signature" {
		t.Fatalf("changed manifest error = %v", err)
	}
}

func TestVerifyUIRejectsDigestOrSizeMismatch(t *testing.T) {
	manifest, signature, archive, publicKey := signedUIFixture(t)
	archive = append(archive, 'x')
	if _, err := VerifyUI(manifest, signature, archive, publicKey, "v1"); ErrorCode(err) != "invalid_payload" {
		t.Fatalf("changed payload error = %v", err)
	}
}

func TestParseUIRejectsUnknownFieldsAndUnsortedFiles(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown":  `{"schemaVersion":1,"kind":"ui","version":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commit":"abc","builtAt":"2026-09-05T00:00:00Z","apiVersion":"v1","archive":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":1},"files":[{"path":"index.html","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":1}],"extra":true}`,
		"unsorted": `{"schemaVersion":1,"kind":"ui","version":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commit":"abc","builtAt":"2026-09-05T00:00:00Z","apiVersion":"v1","archive":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":1},"files":[{"path":"index.html","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":1},{"path":"assets/a.js","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseUI([]byte(raw), "v1"); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func signedUIFixture(t *testing.T) ([]byte, []byte, []byte, ed25519.PublicKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("fixture archive")
	digest := sha256.Sum256(archive)
	fileDigest := sha256.Sum256([]byte("index"))
	manifest, err := json.Marshal(UIManifest{
		SchemaVersion: 1,
		Kind:          "ui",
		Version:       fmt.Sprintf("%x", digest),
		Commit:        "abc1234",
		BuiltAt:       "2026-09-05T00:00:00Z",
		APIVersion:    "v1",
		Archive:       Payload{SHA256: fmt.Sprintf("%x", digest), Bytes: int64(len(archive))},
		Files:         []File{{Path: "index.html", SHA256: fmt.Sprintf("%x", fileDigest), Bytes: 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manifest, ed25519.Sign(privateKey, manifest), archive, publicKey
}
