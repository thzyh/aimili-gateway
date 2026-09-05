package updatefetch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var channelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type Config struct {
	ManifestOrigin      string   `json:"manifestOrigin"`
	RedirectHosts       []string `json:"redirectHosts"`
	Channels            []string `json:"channels"`
	PublicKeyFile       string   `json:"publicKeyFile"`
	CredentialFile      string   `json:"credentialFile,omitempty"`
	StagingRoot         string   `json:"stagingRoot"`
	MaxAssetBytes       int64    `json:"maxAssetBytes"`
	RequestDir          string   `json:"requestDir,omitempty"`
	ResultDir           string   `json:"resultDir,omitempty"`
	BinaryPath          string   `json:"binaryPath,omitempty"`
	PreviousPath        string   `json:"previousPath,omitempty"`
	GatewayConfigPath   string   `json:"gatewayConfigPath,omitempty"`
	DatabasePath        string   `json:"databasePath,omitempty"`
	HealthURL           string   `json:"healthUrl,omitempty"`
	UIRoot              string   `json:"uiRoot,omitempty"`
	AllowGatewayInstall bool     `json:"allowGatewayInstall"`
	FetcherUID          uint32   `json:"fetcherUid,omitempty"`
}

func LoadConfig(filename string) (Config, error) {
	body, err := os.ReadFile(filename)
	if err != nil {
		return Config{}, fmt.Errorf("read updater config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode updater config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("updater config contains trailing data")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	if credential := os.Getenv("AIMILI_UPDATE_CREDENTIAL_FILE"); credential != "" {
		config.CredentialFile = credential
	}
	return config, nil
}

func (c Config) ValidateSpool() error {
	if err := c.Validate(); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"requestDir": c.RequestDir, "resultDir": c.ResultDir, "binaryPath": c.BinaryPath,
		"previousPath": c.PreviousPath, "gatewayConfigPath": c.GatewayConfigPath,
		"databasePath": c.DatabasePath, "healthUrl": c.HealthURL, "uiRoot": c.UIRoot,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if path.Clean(filepath.ToSlash(c.RequestDir)) == path.Clean(filepath.ToSlash(c.ResultDir)) {
		return errors.New("requestDir and resultDir must differ")
	}
	return nil
}

func (c Config) Validate() error {
	origin, err := url.Parse(c.ManifestOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && path.Clean(origin.Path) != origin.Path) {
		return errors.New("manifestOrigin must be a canonical HTTPS URL")
	}
	if strings.TrimSpace(c.PublicKeyFile) == "" || strings.TrimSpace(c.StagingRoot) == "" || c.MaxAssetBytes < 1 || c.MaxAssetBytes > 512<<20 {
		return errors.New("publicKeyFile, stagingRoot and bounded maxAssetBytes are required")
	}
	if len(c.Channels) == 0 || len(c.Channels) > 8 {
		return errors.New("at least one bounded channel is required")
	}
	seenChannels := make(map[string]struct{}, len(c.Channels))
	for _, channel := range c.Channels {
		if !channelPattern.MatchString(channel) {
			return errors.New("channel is invalid")
		}
		if _, exists := seenChannels[channel]; exists {
			return errors.New("channel is duplicated")
		}
		seenChannels[channel] = struct{}{}
	}
	seenHosts := make(map[string]struct{}, len(c.RedirectHosts))
	for _, host := range c.RedirectHosts {
		normalized := strings.ToLower(strings.TrimSuffix(host, "."))
		if normalized == "" || strings.Contains(normalized, ":") || net.ParseIP(normalized) == nil && strings.ContainsAny(normalized, `/\\@?# `) {
			return errors.New("redirect host is invalid")
		}
		seenHosts[normalized] = struct{}{}
	}
	if _, allowed := seenHosts[strings.ToLower(strings.TrimSuffix(origin.Hostname(), "."))]; !allowed {
		return errors.New("origin host must be in redirectHosts")
	}
	return nil
}
