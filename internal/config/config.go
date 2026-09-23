package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultListenAddress    = "127.0.0.1:9080"
	defaultAimiliAddress    = "127.0.0.1:8787"
	defaultAimiliControlURL = "http://127.0.0.1:8790/"
	defaultXUIBaseURL       = "http://127.0.0.1:2001/"
)

type Config struct {
	ListenAddress          string   `json:"listenAddress"`
	PublicOrigin           string   `json:"publicOrigin"`
	RealityServerName      string   `json:"realityServerName"`
	DatabasePath           string   `json:"databasePath"`
	MasterKeyFile          string   `json:"masterKeyFile"`
	AimiliAddress          string   `json:"aimiliAddress"`
	AimiliControlURL       string   `json:"aimiliControlUrl"`
	AimiliControlTokenFile string   `json:"aimiliControlTokenFile"`
	XUIBaseURL             string   `json:"xuiBaseUrl"`
	XUICredentialsFile     string   `json:"xuiCredentialsFile"`
	AimiliBackendURL       string   `json:"aimiliBackendUrl"`
	ExpertModeURL          string   `json:"expertModeUrl"`
	MaxProxyGroups         int      `json:"maxProxyGroups"`
	VLESSPortStart         int      `json:"vlessPortStart"`
	VLESSPortEnd           int      `json:"vlessPortEnd"`
	MixedPortStart         int      `json:"mixedPortStart"`
	MixedPortEnd           int      `json:"mixedPortEnd"`
	AggregateVLESSPort     int      `json:"aggregateVlessPort"`
	MainMixedPort          int      `json:"mainMixedPort"`
	XrayPath               string   `json:"xrayPath"`
	ProbeHost              string   `json:"probeHost"`
	MixedSourceCIDRs       []string `json:"mixedSourceCidrs"`
	ProtocolRequestDir     string   `json:"protocolRequestDir"`
	ProtocolResultDir      string   `json:"protocolResultDir"`
	ProtocolTimeoutSeconds int      `json:"protocolTimeoutSeconds"`
	ExternalUIRoot         string   `json:"externalUiRoot"`
	UpdateRequestDir       string   `json:"updateRequestDir"`
	UpdateResultDir        string   `json:"updateResultDir"`
	UpdateEnabled          bool     `json:"updateEnabled"`
	UpdateCatalogFile      string   `json:"updateCatalogFile"`

	localTest bool
}

func Load(path string) (Config, error) {
	cfg := Config{
		ListenAddress:          defaultListenAddress,
		DatabasePath:           filepath.FromSlash("data/aimili-gateway.db"),
		MasterKeyFile:          filepath.FromSlash("data/master.key"),
		AimiliAddress:          defaultAimiliAddress,
		AimiliControlURL:       defaultAimiliControlURL,
		AimiliControlTokenFile: filepath.FromSlash("data/aimili-control.token"),
		XUIBaseURL:             defaultXUIBaseURL,
		XUICredentialsFile:     filepath.FromSlash("data/xui-automation.json"),
		MaxProxyGroups:         1,
		VLESSPortStart:         20000,
		VLESSPortEnd:           20999,
		MixedPortStart:         30000,
		MixedPortEnd:           30999,
		AggregateVLESSPort:     21000,
		MainMixedPort:          31000,
		XrayPath:               filepath.FromSlash("/usr/local/x-ui/bin/xray-linux-amd64"),
		ProbeHost:              "api.ipify.org",
		ProtocolRequestDir:     filepath.FromSlash("data/protocol-spool/requests"),
		ProtocolResultDir:      filepath.FromSlash("data/protocol-spool/results"),
		ProtocolTimeoutSeconds: 180,
		localTest:              path == "",
	}

	if path != "" {
		if err := decodeFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	applyEnvironment(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) WithRuntimeDefaults() Config {
	if c.MaxProxyGroups == 0 {
		c.MaxProxyGroups = 1
	}
	if c.VLESSPortStart == 0 {
		c.VLESSPortStart = 20000
	}
	if c.VLESSPortEnd == 0 {
		c.VLESSPortEnd = 20999
	}
	if c.MixedPortStart == 0 {
		c.MixedPortStart = 30000
	}
	if c.MixedPortEnd == 0 {
		c.MixedPortEnd = 30999
	}
	if c.AggregateVLESSPort == 0 {
		c.AggregateVLESSPort = 21000
	}
	if c.MainMixedPort == 0 {
		c.MainMixedPort = 31000
	}
	if strings.TrimSpace(c.XrayPath) == "" {
		c.XrayPath = filepath.FromSlash("/usr/local/x-ui/bin/xray-linux-amd64")
	}
	if strings.TrimSpace(c.ProbeHost) == "" {
		c.ProbeHost = "api.ipify.org"
	}
	if strings.TrimSpace(c.ProtocolRequestDir) == "" {
		base := filepath.Dir(c.DatabasePath)
		c.ProtocolRequestDir = filepath.Join(base, "protocol-spool", "requests")
	}
	if strings.TrimSpace(c.ProtocolResultDir) == "" {
		base := filepath.Dir(c.DatabasePath)
		c.ProtocolResultDir = filepath.Join(base, "protocol-spool", "results")
	}
	if c.ProtocolTimeoutSeconds == 0 {
		c.ProtocolTimeoutSeconds = 180
	}
	return c
}

func (c Config) Validate() error {
	if err := validateLoopbackEndpoint(c.ListenAddress); err != nil {
		return fmt.Errorf("listenAddress must use a loopback endpoint: %w", err)
	}
	if err := validateLoopbackEndpoint(c.AimiliAddress); err != nil {
		return fmt.Errorf("aimiliAddress must use a loopback endpoint: %w", err)
	}
	if err := validateLoopbackURL("aimiliControlUrl", c.AimiliControlURL); err != nil {
		return err
	}
	if strings.TrimSpace(c.AimiliControlTokenFile) == "" {
		return errors.New("aimiliControlTokenFile is required")
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		return errors.New("databasePath is required")
	}
	if strings.TrimSpace(c.MasterKeyFile) == "" {
		return errors.New("masterKeyFile is required")
	}
	if err := validatePublicOrigin(c.PublicOrigin, c.localTest); err != nil {
		return err
	}
	if c.RealityServerName != "" {
		if strings.TrimSpace(c.RealityServerName) != c.RealityServerName || net.ParseIP(c.RealityServerName) != nil || strings.ContainsAny(c.RealityServerName, "/:?@# ") {
			return errors.New("realityServerName must be a DNS name")
		}
	}
	if err := validateLoopbackURL("xuiBaseUrl", c.XUIBaseURL); err != nil {
		return err
	}
	if strings.TrimSpace(c.XUICredentialsFile) == "" {
		return errors.New("xuiCredentialsFile is required")
	}
	if err := validateBackendURL("aimiliBackendUrl", c.AimiliBackendURL); err != nil {
		return err
	}
	if err := validateBackendURL("expertModeUrl", c.ExpertModeURL); err != nil {
		return err
	}
	if (c.AimiliBackendURL == "") != (c.ExpertModeURL == "") {
		return errors.New("aimiliBackendUrl and expertModeUrl must be configured together")
	}
	if c.MaxProxyGroups < 1 || c.MaxProxyGroups > 64 || c.VLESSPortStart < 1 || c.VLESSPortEnd > 65535 ||
		c.VLESSPortEnd < c.VLESSPortStart || c.MixedPortStart < 1 || c.MixedPortEnd > 65535 || c.MixedPortEnd < c.MixedPortStart ||
		!(c.VLESSPortEnd < c.MixedPortStart || c.MixedPortEnd < c.VLESSPortStart) {
		return errors.New("invalid proxy group capacity or port ranges")
	}
	if c.AggregateVLESSPort < 1 || c.AggregateVLESSPort > 65535 || c.MainMixedPort < 1 || c.MainMixedPort > 65535 || c.AggregateVLESSPort == c.MainMixedPort ||
		(c.AggregateVLESSPort >= c.VLESSPortStart && c.AggregateVLESSPort <= c.VLESSPortEnd) ||
		(c.MainMixedPort >= c.MixedPortStart && c.MainMixedPort <= c.MixedPortEnd) {
		return errors.New("aggregate and main ports must be reserved outside managed ranges")
	}
	if strings.TrimSpace(c.XrayPath) == "" || strings.TrimSpace(c.ProbeHost) == "" || net.ParseIP(c.ProbeHost) != nil || strings.ContainsAny(c.ProbeHost, "/:") {
		return errors.New("xrayPath and a DNS probeHost are required")
	}
	requestDir := pathpkg.Clean(filepath.ToSlash(c.ProtocolRequestDir))
	resultDir := pathpkg.Clean(filepath.ToSlash(c.ProtocolResultDir))
	if c.ProtocolTimeoutSeconds < 1 || c.ProtocolTimeoutSeconds > 600 || requestDir == resultDir ||
		pathpkg.Base(requestDir) != "requests" || pathpkg.Base(resultDir) != "results" || pathpkg.Dir(requestDir) != pathpkg.Dir(resultDir) {
		return errors.New("protocol spool must use sibling requests and results directories")
	}
	if value := strings.TrimSpace(c.ExternalUIRoot); value != "" && !filepath.IsAbs(value) && !pathpkg.IsAbs(filepath.ToSlash(value)) {
		return errors.New("externalUiRoot must be an absolute path")
	}
	if c.UpdateEnabled && (c.UpdateCatalogFile != "/etc/aimili-gateway/update-catalog.json" || c.UpdateRequestDir == "") {
		return errors.New("enabled updater requires fixed trusted catalog and spool")
	}
	if (strings.TrimSpace(c.UpdateRequestDir) == "") != (strings.TrimSpace(c.UpdateResultDir) == "") {
		return errors.New("updateRequestDir and updateResultDir must be configured together")
	}
	if c.UpdateRequestDir != "" {
		requestDir := pathpkg.Clean(filepath.ToSlash(c.UpdateRequestDir))
		resultDir := pathpkg.Clean(filepath.ToSlash(c.UpdateResultDir))
		if pathpkg.Base(requestDir) != "requests" || pathpkg.Base(resultDir) != "results" || pathpkg.Dir(requestDir) != pathpkg.Dir(resultDir) {
			return errors.New("update spool must use sibling requests and results directories")
		}
	}
	for _, raw := range c.MixedSourceCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return errors.New("mixedSourceCidrs must contain canonical non-global prefixes")
		}
	}
	return nil
}

func decodeFile(path string, cfg *Config) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("config must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("config permissions must not allow group or world access")
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode config: multiple JSON values")
		}
		return fmt.Errorf("decode config: %w", err)
	}
	return nil
}

func applyEnvironment(cfg *Config) {
	overrides := []struct {
		name   string
		target *string
	}{
		{name: "GATEWAY_LISTEN_ADDRESS", target: &cfg.ListenAddress},
		{name: "GATEWAY_PUBLIC_ORIGIN", target: &cfg.PublicOrigin},
		{name: "GATEWAY_DATABASE_PATH", target: &cfg.DatabasePath},
		{name: "GATEWAY_MASTER_KEY_FILE", target: &cfg.MasterKeyFile},
		{name: "GATEWAY_AIMILI_ADDRESS", target: &cfg.AimiliAddress},
		{name: "GATEWAY_AIMILI_CONTROL_URL", target: &cfg.AimiliControlURL},
		{name: "GATEWAY_AIMILI_CONTROL_TOKEN_FILE", target: &cfg.AimiliControlTokenFile},
		{name: "GATEWAY_XUI_BASE_URL", target: &cfg.XUIBaseURL},
		{name: "GATEWAY_XUI_CREDENTIALS_FILE", target: &cfg.XUICredentialsFile},
		{name: "GATEWAY_AIMILI_BACKEND_URL", target: &cfg.AimiliBackendURL},
		{name: "GATEWAY_EXPERT_MODE_URL", target: &cfg.ExpertModeURL},
		{name: "GATEWAY_XRAY_PATH", target: &cfg.XrayPath},
		{name: "GATEWAY_PROBE_HOST", target: &cfg.ProbeHost},
		{name: "GATEWAY_PROTOCOL_REQUEST_DIR", target: &cfg.ProtocolRequestDir},
		{name: "GATEWAY_PROTOCOL_RESULT_DIR", target: &cfg.ProtocolResultDir},
		{name: "GATEWAY_EXTERNAL_UI_ROOT", target: &cfg.ExternalUIRoot},
		{name: "GATEWAY_UPDATE_REQUEST_DIR", target: &cfg.UpdateRequestDir},
		{name: "GATEWAY_UPDATE_RESULT_DIR", target: &cfg.UpdateResultDir},
	}
	for _, override := range overrides {
		if value := os.Getenv(override.name); value != "" {
			*override.target = value
		}
	}
}

func validateLoopbackEndpoint(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("invalid host and port")
	}
	if port == "" {
		return errors.New("port is required")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("host is not loopback")
	}
	return nil
}

func validatePublicOrigin(value string, localTest bool) error {
	if value == "" && localTest {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("publicOrigin must be an exact HTTPS origin")
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("publicOrigin must not include credentials, path, query, or fragment")
	}
	return nil
}

func validateLoopbackURL(field, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an HTTP URL", field)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must not include credentials, query, or fragment", field)
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("%s host must be loopback", field)
		}
	}
	return nil
}

func validateBackendURL(field, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.HasPrefix(value, "/") || parsed.IsAbs() || parsed.Host != "" {
		return fmt.Errorf("%s must be a relative absolute-path reference", field)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || strings.HasPrefix(value, "//") || !strings.HasSuffix(value, "/") {
		return fmt.Errorf("%s must be an exact directory path without query or fragment", field)
	}
	return nil
}
