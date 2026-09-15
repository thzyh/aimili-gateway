package releaseverify

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Payload struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type UIManifest struct {
	SchemaVersion int     `json:"schemaVersion"`
	Kind          string  `json:"kind"`
	Version       string  `json:"version"`
	Commit        string  `json:"commit"`
	BuiltAt       string  `json:"builtAt"`
	APIVersion    string  `json:"apiVersion"`
	Archive       Payload `json:"archive"`
	Files         []File  `json:"files"`
}

type UICompatibility struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

type GatewayManifest struct {
	SchemaVersion     int              `json:"schemaVersion"`
	Kind              string           `json:"kind"`
	Version           string           `json:"version"`
	Commit            string           `json:"commit"`
	BuiltAt           string           `json:"builtAt"`
	Platform          string           `json:"platform"`
	APIVersion        string           `json:"apiVersion"`
	ImpactClass       string           `json:"impactClass"`
	Binary            File             `json:"binary"`
	MinDatabaseSchema int              `json:"minDatabaseSchema"`
	MaxDatabaseSchema int              `json:"maxDatabaseSchema"`
	UICompatibility   *UICompatibility `json:"uiCompatibility,omitempty"`
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.code + ": " + e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}

func VerifyUI(manifestBody, signature, archive []byte, publicKey ed25519.PublicKey, apiVersion string) (UIManifest, error) {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, manifestBody, signature) {
		return UIManifest{}, &codedError{code: "invalid_signature", err: errors.New("UI manifest signature is invalid")}
	}
	manifest, err := ParseUI(manifestBody, apiVersion)
	if err != nil {
		return UIManifest{}, err
	}
	digest := sha256.Sum256(archive)
	if int64(len(archive)) != manifest.Archive.Bytes || fmt.Sprintf("%x", digest) != manifest.Archive.SHA256 {
		return UIManifest{}, &codedError{code: "invalid_payload", err: errors.New("UI archive does not match manifest")}
	}
	return manifest, nil
}

func VerifyGateway(manifestBody, signature, binary []byte, publicKey ed25519.PublicKey, apiVersion, platform string) (GatewayManifest, error) {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, manifestBody, signature) {
		return GatewayManifest{}, &codedError{code: "invalid_signature", err: errors.New("Gateway manifest signature is invalid")}
	}
	manifest, err := ParseGateway(manifestBody, apiVersion, platform)
	if err != nil {
		return GatewayManifest{}, err
	}
	digest := sha256.Sum256(binary)
	if int64(len(binary)) != manifest.Binary.Bytes || fmt.Sprintf("%x", digest) != manifest.Binary.SHA256 {
		return GatewayManifest{}, &codedError{code: "invalid_payload", err: errors.New("Gateway binary does not match manifest")}
	}
	return manifest, nil
}

func ParseGateway(body []byte, apiVersion, platform string) (GatewayManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest GatewayManifest
	if err := decoder.Decode(&manifest); err != nil {
		return GatewayManifest{}, &codedError{code: "invalid_manifest", err: err}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return GatewayManifest{}, &codedError{code: "invalid_manifest", err: errors.New("manifest contains trailing data")}
	}
	if manifest.ImpactClass != "control-plane-only" {
		return GatewayManifest{}, &codedError{code: "manual_staged_deploy_required", err: errors.New("Gateway release has runtime impact")}
	}
	if manifest.UICompatibility != nil {
		return GatewayManifest{}, &codedError{code: "unsupported_ui_compatibility", err: errors.New("UI compatibility declarations are not supported")}
	}
	if manifest.SchemaVersion != 1 || manifest.Kind != "gateway" || manifest.APIVersion != apiVersion || manifest.Platform != platform ||
		!validVersion(manifest.Version) || strings.TrimSpace(manifest.Commit) == "" || manifest.Binary.Path != "aimili-gateway" ||
		!validDigest(manifest.Binary.SHA256) || manifest.Binary.Bytes < 1 || manifest.MinDatabaseSchema < 1 ||
		manifest.MaxDatabaseSchema < manifest.MinDatabaseSchema {
		return GatewayManifest{}, &codedError{code: "incompatible_manifest", err: errors.New("Gateway manifest metadata is incompatible")}
	}
	if _, err := time.Parse(time.RFC3339, manifest.BuiltAt); err != nil {
		return GatewayManifest{}, &codedError{code: "invalid_manifest", err: errors.New("Gateway build time is invalid")}
	}
	return manifest, nil
}

func ParseUI(body []byte, apiVersion string) (UIManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest UIManifest
	if err := decoder.Decode(&manifest); err != nil {
		return UIManifest{}, &codedError{code: "invalid_manifest", err: err}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return UIManifest{}, &codedError{code: "invalid_manifest", err: errors.New("manifest contains trailing data")}
	}
	if manifest.SchemaVersion != 1 || manifest.Kind != "ui" || manifest.APIVersion != apiVersion || !validDigest(manifest.Version) || manifest.Version != manifest.Archive.SHA256 || manifest.Archive.Bytes < 1 || strings.TrimSpace(manifest.Commit) == "" {
		return UIManifest{}, &codedError{code: "incompatible_manifest", err: errors.New("UI manifest metadata is incompatible")}
	}
	if _, err := time.Parse(time.RFC3339, manifest.BuiltAt); err != nil {
		return UIManifest{}, &codedError{code: "invalid_manifest", err: errors.New("UI build time is invalid")}
	}
	seenIndex := false
	previous := ""
	for _, file := range manifest.Files {
		if file.Path <= previous || path.Clean(file.Path) != file.Path || strings.Contains(file.Path, `\`) || (file.Path != "index.html" && !strings.HasPrefix(file.Path, "assets/")) || !validDigest(file.SHA256) || file.Bytes < 0 {
			return UIManifest{}, &codedError{code: "invalid_manifest", err: errors.New("UI file list is invalid")}
		}
		seenIndex = seenIndex || file.Path == "index.html"
		previous = file.Path
	}
	if !seenIndex {
		return UIManifest{}, &codedError{code: "invalid_manifest", err: errors.New("UI index is missing")}
	}
	return manifest, nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validVersion(value string) bool {
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if !strings.HasPrefix(value, "v") || len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

// CompareVersions accepts only the same canonical release version grammar as manifests.
func CompareVersions(left, right string) (int, error) {
	if !validVersion(left) || !validVersion(right) {
		return 0, errors.New("non-canonical version")
	}
	l, r := strings.Split(left[1:], "."), strings.Split(right[1:], ".")
	for i := range l {
		lv, _ := strconv.ParseUint(l[i], 10, 64)
		rv, _ := strconv.ParseUint(r[i], 10, 64)
		if lv < rv {
			return -1, nil
		}
		if lv > rv {
			return 1, nil
		}
	}
	return 0, nil
}
