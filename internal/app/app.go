package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
	"github.com/thzyh/aimili-gateway/internal/securefile"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
	"github.com/thzyh/aimili-gateway/internal/webassets"
)

type App struct {
	handler   http.Handler
	store     *store.Store
	closeOnce sync.Once
	closeErr  error
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	cfg = cfg.WithRuntimeDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	masterKey, err := securefile.ReadMasterKey(cfg.MasterKeyFile)
	if err != nil {
		return nil, err
	}
	defer clear(masterKey)
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	xuiProbe, err := newXUIProbe(cfg.XUIBaseURL)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	dependencies := httpapi.Dependencies{
		Store:         database,
		MasterKey:     masterKey,
		PublicOrigin:  cfg.PublicOrigin,
		AimiliProbe:   aimili.New(cfg.AimiliAddress),
		XUIProbe:      xuiProbe,
		ExpertModeURL: cfg.ExpertModeURL,
	}
	proxyManager, err := newProxyManager(ctx, cfg, database, masterKey)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	dependencies.ProxyManager = proxyManager
	if cfg.PublicOrigin == "" {
		dependencies.TestOrigin = "http://" + cfg.ListenAddress
	}
	apiHandler := httpapi.NewServer(dependencies)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(response).Encode(map[string]string{"status": "ok"})
	})
	mux.Handle("/api/v1/", apiHandler)
	mux.Handle("/", webassets.Handler())
	return &App{handler: mux, store: database}, nil
}

func newProxyManager(ctx context.Context, cfg config.Config, database *store.Store, masterKey []byte) (*orchestrator.Orchestrator, error) {
	tokenExists := regularFileExists(cfg.AimiliControlTokenFile)
	xuiCredentialsExist := regularFileExists(cfg.XUICredentialsFile)
	if !tokenExists && !xuiCredentialsExist {
		return nil, nil
	}
	if tokenExists != xuiCredentialsExist {
		return nil, errors.New("Aimili control token and 3x-ui automation credentials must be configured together")
	}
	token, err := aimili.ReadTokenFile(cfg.AimiliControlTokenFile)
	if err != nil {
		return nil, err
	}
	aimiliClient, err := aimili.NewClient(cfg.AimiliControlURL, token)
	clear(token)
	if err != nil {
		return nil, err
	}
	xuiCredentials, err := xui.ReadCredentialsFile(cfg.XUICredentialsFile)
	if err != nil {
		return nil, err
	}
	xuiClient, err := xui.NewClient(cfg.XUIBaseURL, xuiCredentials)
	if err != nil {
		return nil, err
	}
	if err := ensureRuntimeCredentials(ctx, database, masterKey); err != nil {
		return nil, err
	}
	if err := seedMixedCIDRs(ctx, database, cfg.MixedSourceCIDRs); err != nil {
		return nil, err
	}
	publicHost := "localhost"
	if cfg.PublicOrigin != "" {
		parsed, parseErr := url.Parse(cfg.PublicOrigin)
		if parseErr != nil || parsed.Hostname() == "" {
			return nil, errors.New("resolve public proxy host")
		}
		publicHost = parsed.Hostname()
	}
	return orchestrator.New(orchestrator.Config{
		MaxGroups: cfg.MaxProxyGroups, VLESSPortStart: cfg.VLESSPortStart, VLESSPortEnd: cfg.VLESSPortEnd,
		MixedPortStart: cfg.MixedPortStart, MixedPortEnd: cfg.MixedPortEnd, PublicHost: publicHost,
		XrayPath: cfg.XrayPath, ProbeHost: cfg.ProbeHost,
	}, database, aimiliClient, xuiClient, validator.New(20*time.Second), masterKey)
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func ensureRuntimeCredentials(ctx context.Context, database *store.Store, masterKey []byte) error {
	items := []struct {
		purpose  string
		generate func() ([]byte, error)
	}{
		{purpose: "vless-client-id", generate: generateUUID},
		{purpose: "mixed-username", generate: func() ([]byte, error) { value, err := randomHex(8); return []byte("agw-" + value), err }},
		{purpose: "mixed-password", generate: func() ([]byte, error) { value, err := randomHex(24); return []byte(value), err }},
	}
	for _, item := range items {
		value, err := database.GetCredential(ctx, item.purpose, masterKey)
		if err == nil {
			clear(value)
			continue
		}
		if !errors.Is(err, store.ErrCredentialNotFound) {
			return err
		}
		value, err = item.generate()
		if err != nil {
			return errors.New("generate proxy credential")
		}
		if err = database.PutCredential(ctx, item.purpose, value, masterKey); err != nil {
			clear(value)
			return err
		}
		clear(value)
	}
	return nil
}

func generateUUID() ([]byte, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return []byte(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])), nil
}

func randomHex(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func seedMixedCIDRs(ctx context.Context, database *store.Store, values []string) error {
	existing, err := database.ListMixedCIDRs(ctx)
	if err != nil || len(existing) > 0 || len(values) == 0 {
		return err
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, raw := range values {
		prefix, parseErr := netip.ParsePrefix(raw)
		if parseErr != nil {
			return errors.New("parse configured mixed CIDR")
		}
		prefixes = append(prefixes, prefix)
	}
	return database.ReplaceMixedCIDRs(ctx, prefixes)
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.store != nil {
			a.closeErr = a.store.Close()
		}
	})
	return a.closeErr
}

func newXUIProbe(baseURL string) (adapters.Prober, error) {
	if baseURL == "" {
		return unavailableProbe{service: "3x-ui", errorCode: "not_configured"}, nil
	}
	probe, err := xui.New(baseURL)
	if err != nil {
		return nil, errors.New("configure 3x-ui probe")
	}
	return probe, nil
}

type unavailableProbe struct {
	service   string
	errorCode string
}

func (p unavailableProbe) Probe(context.Context) adapters.ProbeResult {
	return adapters.ProbeResult{
		Service:      p.service,
		Health:       adapters.HealthUnavailable,
		Capabilities: []string{},
		ErrorCode:    p.errorCode,
		CheckedAt:    time.Now().UTC(),
	}
}
