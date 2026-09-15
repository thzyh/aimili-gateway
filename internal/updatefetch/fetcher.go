package updatefetch

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/releaseverify"
	"github.com/thzyh/aimili-gateway/internal/updatetxn"
)

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
	return releaseverify.ErrorCode(err)
}

type PreflightResult struct {
	RunID      string          `json:"runId"`
	Kind       updatetxn.Kind  `json:"kind"`
	Version    string          `json:"version"`
	StagingDir string          `json:"stagingDir"`
	State      updatetxn.State `json:"state"`
}

type Fetcher struct {
	Config         Config
	Client         *http.Client
	AvailableBytes func(string) (uint64, error)
	Verify         func(updatetxn.Request, []byte, []byte, []byte) error
	Log            func(string)
}

func (f *Fetcher) Fetch(ctx context.Context, request updatetxn.Request) (PreflightResult, error) {
	if err := f.Config.Validate(); err != nil {
		return PreflightResult{}, &codedError{code: "invalid_config", err: err}
	}
	if err := updatetxn.ValidateRequest(request); err != nil || request.Action != updatetxn.ActionApply {
		return PreflightResult{}, &codedError{code: "invalid_request", err: updatetxn.ErrInvalidRequest}
	}
	if downloaded, err := Downloaded(f.Config.StagingRoot, request); err == nil {
		return downloaded, nil
	}
	available := f.AvailableBytes
	if available == nil {
		available = AvailableBytes
	}
	free, err := available(f.Config.StagingRoot)
	if err != nil {
		return PreflightResult{}, &codedError{code: "disk_check_failed", err: err}
	}
	required := uint64(f.Config.MaxAssetBytes)*3 + 128<<20
	if free < required {
		return PreflightResult{}, &codedError{code: "disk_full", err: errors.New("insufficient staging space")}
	}
	if err := os.MkdirAll(f.Config.StagingRoot, 0o700); err != nil {
		return PreflightResult{}, &codedError{code: "staging_failed", err: err}
	}
	staging := filepath.Join(f.Config.StagingRoot, request.RunID)
	if err := os.Mkdir(staging, 0o700); err != nil {
		return PreflightResult{}, &codedError{code: "staging_failed", err: err}
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()

	origin, _ := url.Parse(f.Config.ManifestOrigin)
	channel := f.Config.Channels[0]
	if f.Log != nil {
		f.Log(fmt.Sprintf("fetch host=%s channel=%s kind=%s version=%s", origin.Hostname(), channel, request.Kind, request.Version))
	}
	client := f.secureClient(origin.Hostname())
	credential, err := readCredential(f.Config.CredentialFile)
	if err != nil {
		return PreflightResult{}, &codedError{code: "credential_failed", err: err}
	}
	assetNames := []string{"manifest.json", "manifest.sig", "ui.tar.gz"}
	if request.Kind == updatetxn.KindGateway {
		assetNames[2] = "aimili-gateway"
	}
	assets := make([][]byte, len(assetNames))
	for index, name := range assetNames {
		assetURL := releaseAssetURL(origin, channel, request, name)
		body, err := download(ctx, client, assetURL.String(), credential, f.Config.MaxAssetBytes)
		if err != nil {
			return PreflightResult{}, err
		}
		assets[index] = body
		if err := writeExclusive(filepath.Join(staging, name), body); err != nil {
			return PreflightResult{}, &codedError{code: "staging_failed", err: err}
		}
	}
	verify := f.Verify
	if verify == nil {
		verify = f.verifyRelease
	}
	if err := verify(request, assets[0], assets[1], assets[2]); err != nil {
		return PreflightResult{}, err
	}
	result := PreflightResult{RunID: request.RunID, Kind: request.Kind, Version: request.Version, StagingDir: staging, State: updatetxn.StateValidating}
	marker, err := json.Marshal(result)
	if err != nil {
		return PreflightResult{}, err
	}
	if err := writeExclusive(filepath.Join(staging, "download.complete"), marker); err != nil {
		return PreflightResult{}, &codedError{code: "staging_failed", err: err}
	}
	keep = true
	return result, nil
}

func releaseAssetURL(origin *url.URL, channel string, request updatetxn.Request, name string) url.URL {
	assetURL := *origin
	if strings.EqualFold(origin.Hostname(), "github.com") {
		assetURL.Path = path.Join(origin.Path, request.Version, name)
	} else {
		assetURL.Path = path.Join(origin.Path, channel, string(request.Kind), request.Version, name)
	}
	return assetURL
}

func Downloaded(root string, request updatetxn.Request) (PreflightResult, error) {
	if updatetxn.ValidateRequest(request) != nil {
		return PreflightResult{}, updatetxn.ErrInvalidRequest
	}
	staging := filepath.Join(root, request.RunID)
	var result PreflightResult
	var trusted *uint32
	if uid := os.Geteuid(); uid >= 0 {
		v := uint32(uid)
		trusted = &v
	}
	if err := updatetxn.ReadTrustedStateFile(filepath.Join(staging, "download.complete"), trusted, &result); err != nil {
		return result, err
	}
	if result.RunID != request.RunID || result.Kind != request.Kind || result.Version != request.Version || result.State != updatetxn.StateValidating || result.StagingDir != staging {
		return result, updatetxn.ErrInvalidRequest
	}
	return result, nil
}

func (f *Fetcher) secureClient(originHost string) *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	if f.Client != nil {
		clone := *f.Client
		client = &clone
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		clone := transport.Clone()
		clone.Proxy = nil
		if clone.TLSClientConfig == nil {
			clone.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13}
		} else {
			clone.TLSClientConfig = clone.TLSClientConfig.Clone()
			clone.TLSClientConfig.MinVersion = tls.VersionTLS13
		}
		client.Transport = clone
	} else if client.Transport == nil {
		client.Transport = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}}
	}
	allowed := make(map[string]struct{}, len(f.Config.RedirectHosts)+1)
	allowed[strings.ToLower(strings.TrimSuffix(originHost, "."))] = struct{}{}
	for _, host := range f.Config.RedirectHosts {
		allowed[strings.ToLower(strings.TrimSuffix(host, "."))] = struct{}{}
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		host := strings.ToLower(strings.TrimSuffix(request.URL.Hostname(), "."))
		if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.Fragment != "" {
			return &codedError{code: "blocked_redirect", err: errors.New("redirect URL is not allowed")}
		}
		if _, ok := allowed[host]; !ok || len(via) >= 5 {
			return &codedError{code: "blocked_redirect", err: errors.New("redirect host is not allowed")}
		}
		return nil
	}
	return client
}

func (f *Fetcher) verifyRelease(request updatetxn.Request, manifest, signature, payload []byte) error {
	publicKey, err := readPublicKey(f.Config.PublicKeyFile)
	if err != nil {
		return &codedError{code: "public_key_invalid", err: err}
	}
	var kind, version string
	if request.Kind == updatetxn.KindUI {
		var verified releaseverify.UIManifest
		verified, err = releaseverify.VerifyUI(manifest, signature, payload, publicKey, "v1")
		kind, version = verified.Kind, verified.Version
	} else {
		var verified releaseverify.GatewayManifest
		verified, err = releaseverify.VerifyGateway(manifest, signature, payload, publicKey, "v1", "linux-amd64")
		kind, version = verified.Kind, verified.Version
	}
	if err == nil && (kind != string(request.Kind) || version != request.Version) {
		return &codedError{code: "request_manifest_mismatch", err: errors.New("signed release differs from request")}
	}
	return err
}

func download(ctx context.Context, client *http.Client, address, credential string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, &codedError{code: "download_failed", err: errors.New("create request")}
	}
	if credential != "" {
		request.Header.Set("Authorization", credential)
	}
	response, err := client.Do(request)
	if err != nil {
		if code := ErrorCode(err); code == "blocked_redirect" {
			return nil, &codedError{code: code, err: errors.New("redirect rejected")}
		}
		return nil, &codedError{code: "download_failed", err: errors.New("request failed")}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &codedError{code: "download_failed", err: fmt.Errorf("unexpected HTTP status %d", response.StatusCode)}
	}
	if response.ContentLength > limit {
		return nil, &codedError{code: "asset_too_large", err: errors.New("asset exceeds size limit")}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, &codedError{code: "download_failed", err: errors.New("read response")}
	}
	if int64(len(body)) > limit {
		return nil, &codedError{code: "asset_too_large", err: errors.New("asset exceeds size limit")}
	}
	return body, nil
}

func writeExclusive(filename string, body []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func readCredential(filename string) (string, error) {
	if strings.TrimSpace(filename) == "" {
		return "", nil
	}
	body, err := os.ReadFile(filename)
	if err != nil || len(body) > 8<<10 {
		return "", errors.New("credential is unavailable")
	}
	value := strings.TrimSpace(string(body))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("credential is invalid")
	}
	return value, nil
}

func readPublicKey(filename string) (ed25519.PublicKey, error) {
	body, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(string(body))
	if len(value) != ed25519.PublicKeySize*2 || value != strings.ToLower(value) {
		return nil, errors.New("public key is invalid")
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key is invalid")
	}
	return ed25519.PublicKey(raw), nil
}
