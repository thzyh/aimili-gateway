package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
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

func TestRunGatewayWritesVerifiableLinuxAMD64ReleaseAssets(t *testing.T) {
	directory := t.TempDir()
	binary := buildGatewayBinary(t, "linux", "amd64")
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(directory, "signing.key")
	if err := os.WriteFile(privatePath, []byte(hex.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "release")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"gateway", "--binary", binary, "--private-key", privatePath, "--out", output,
		"--version", "v1.2.3", "--commit", "abc1234", "--built-at", "2026-09-05T08:00:00+08:00",
		"--min-database-schema", "11", "--max-database-schema", "13",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gateway code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"aimili-gateway", "manifest.json", "manifest.sig"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("release assets = %q, want %q", names, want)
	}
	manifestBody := mustRead(t, filepath.Join(output, "manifest.json"))
	signature := mustRead(t, filepath.Join(output, "manifest.sig"))
	releasedBinary := mustRead(t, filepath.Join(output, "aimili-gateway"))
	manifest, err := releaseverify.VerifyGateway(manifestBody, signature, releasedBinary, privateKey.Public().(ed25519.PublicKey), "v1", "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(releasedBinary)
	if manifest.SchemaVersion != 1 || manifest.Kind != "gateway" || manifest.Platform != "linux-amd64" || manifest.APIVersion != "v1" ||
		manifest.ImpactClass != "control-plane-only" || manifest.Binary.Path != "aimili-gateway" || manifest.Binary.SHA256 != hex.EncodeToString(digest[:]) ||
		manifest.Binary.Bytes != int64(len(releasedBinary)) || manifest.Version != "v1.2.3" || manifest.Commit != "abc1234" ||
		manifest.BuiltAt != "2026-09-05T00:00:00Z" || manifest.MinDatabaseSchema != 11 || manifest.MaxDatabaseSchema != 13 {
		encoded, _ := json.Marshal(manifest)
		t.Fatalf("gateway manifest = %s", encoded)
	}
}

func TestRunGatewayRejectsNonLinuxAMD64BinaryInputs(t *testing.T) {
	directory := t.TempDir()
	plainFile := filepath.Join(directory, "plain-input")
	if err := os.WriteFile(plainFile, []byte("not an executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(directory, "signing.key")
	if err := os.WriteFile(privatePath, []byte(hex.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, binary := range map[string]string{
		"plain file":  plainFile,
		"linux arm64": buildGatewayBinary(t, "linux", "arm64"),
	} {
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(directory, strings.ReplaceAll(name, " ", "-"))
			code := run([]string{
				"gateway", "--binary", binary, "--private-key", privatePath, "--out", output,
				"--version", "v1.2.3", "--commit", "abc1234", "--built-at", "2026-09-05T00:00:00Z",
				"--min-database-schema", "11", "--max-database-schema", "13",
			}, &bytes.Buffer{}, &bytes.Buffer{})
			if code == 0 {
				t.Fatal("non-linux-amd64 binary accepted")
			}
		})
	}
}

func TestRunGatewayRejectsInvalidMetadataAndUnsafePaths(t *testing.T) {
	directory := t.TempDir()
	binary := buildGatewayBinary(t, "linux", "amd64")
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(directory, "signing.key")
	if err := os.WriteFile(privatePath, []byte(hex.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"gateway", "--binary", binary, "--private-key", privatePath, "--out", filepath.Join(directory, "release"), "--version", "v1.2.3", "--commit", "abc1234", "--built-at", "2026-09-05T00:00:00Z", "--min-database-schema", "11", "--max-database-schema", "13"}
	for name, args := range map[string][]string{
		"version":         replaceGatewayArgument(base, "--version", "1.2.3"),
		"build time":      replaceGatewayArgument(base, "--built-at", "not-a-time"),
		"schema range":    replaceGatewayArgument(base, "--max-database-schema", "10"),
		"same key binary": replaceGatewayArgument(base, "--binary", privatePath),
		"key in output":   replaceGatewayArgument(base, "--private-key", filepath.Join(directory, "release", "signing.key")),
	} {
		t.Run(name, func(t *testing.T) {
			if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code == 0 {
				t.Fatalf("code=%d", code)
			}
		})
	}
	output := filepath.Join(directory, "occupied")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "existing"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := replaceGatewayArgument(base, "--out", output)
	if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatalf("occupied output code=%d", code)
	}
	if got := string(mustRead(t, filepath.Join(output, "existing"))); got != "keep" {
		t.Fatalf("existing output overwritten: %q", got)
	}
}

func replaceGatewayArgument(args []string, flag, value string) []string {
	result := append([]string(nil), args...)
	for index := range result[:len(result)-1] {
		if result[index] == flag {
			result[index+1] = value
			return result
		}
	}
	panic("missing gateway flag")
}

func mustRead(t *testing.T, filename string) []byte {
	t.Helper()
	body, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func buildGatewayBinary(t *testing.T, goos, goarch string) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "aimili-gateway")
	command := exec.Command("go", "build", "-o", binary, source)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s/%s: %v\n%s", goos, goarch, err, output)
	}
	return binary
}
