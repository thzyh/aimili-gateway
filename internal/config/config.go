package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
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
	ListenAddress          string `json:"listenAddress"`
	PublicOrigin           string `json:"publicOrigin"`
	DatabasePath           string `json:"databasePath"`
	MasterKeyFile          string `json:"masterKeyFile"`
	AimiliAddress          string `json:"aimiliAddress"`
	AimiliControlURL       string `json:"aimiliControlUrl"`
	AimiliControlTokenFile string `json:"aimiliControlTokenFile"`
	XUIBaseURL             string `json:"xuiBaseUrl"`
	ExpertModeURL          string `json:"expertModeUrl"`

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
	if err := validateLoopbackURL("xuiBaseUrl", c.XUIBaseURL); err != nil {
		return err
	}
	if err := validateExpertModeURL(c.ExpertModeURL); err != nil {
		return err
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
		{name: "GATEWAY_EXPERT_MODE_URL", target: &cfg.ExpertModeURL},
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

func validateExpertModeURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.HasPrefix(value, "/") || parsed.IsAbs() || parsed.Host != "" {
		return errors.New("expertModeUrl must be a relative absolute-path reference")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("expertModeUrl must not include query or fragment")
	}
	return nil
}
