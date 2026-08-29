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

	"github.com/thzyh/aimili-gateway/internal/accountsync"
	"github.com/thzyh/aimili-gateway/internal/adapters"
	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/backendlogin"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"github.com/thzyh/aimili-gateway/internal/maintenance"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
	"github.com/thzyh/aimili-gateway/internal/protocoltxn"
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
	cancel    context.CancelFunc
	driftDone <-chan struct{}
}

type initialReconciler interface {
	Reconcile(context.Context) orchestrator.ReconcileResult
}

type accountChecker interface{ Check(context.Context) error }

func startAccountDriftChecks(ctx context.Context, checker accountChecker, initialDelay, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(initialDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		_ = checker.Check(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = checker.Check(ctx)
			}
		}
	}()
	return done
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	appContext, cancel := context.WithCancel(ctx)
	cfg = cfg.WithRuntimeDefaults()
	if err := cfg.Validate(); err != nil {
		cancel()
		return nil, err
	}
	masterKey, err := securefile.ReadMasterKey(cfg.MasterKeyFile)
	if err != nil {
		cancel()
		return nil, err
	}
	defer clear(masterKey)
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		cancel()
		return nil, err
	}
	xuiProbe, err := newXUIProbe(cfg.XUIBaseURL)
	if err != nil {
		cancel()
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
	runtime, err := newRuntimeServices(appContext, cfg, database, masterKey)
	if err != nil {
		cancel()
		_ = database.Close()
		return nil, err
	}
	dependencies.ProxyManager = runtime.proxy
	dependencies.Maintenance = runtime.maintenance
	dependencies.BackendLogin = runtime.backendLogin
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
	var driftDone <-chan struct{}
	if runtime.proxy != nil {
		startInitialReconcile(appContext, runtime.proxy)
	}
	if runtime.accounts != nil {
		driftDone = startAccountDriftChecks(appContext, runtime.accounts, accountCheckInitialDelay(), 6*time.Hour)
	}
	return &App{handler: mux, store: database, cancel: cancel, driftDone: driftDone}, nil
}

func startInitialReconcile(ctx context.Context, reconciler initialReconciler) {
	go func() {
		_ = reconciler.Reconcile(ctx)
	}()
}

func newMaintenanceConfig(ctx context.Context, maxOnline int, reconciler initialReconciler) maintenance.Config {
	return maintenance.Config{
		MaxOnline:       maxOnline,
		LifetimeContext: ctx,
		Reconcile: func(reconcileContext context.Context) {
			_ = reconciler.Reconcile(reconcileContext)
		},
	}
}

type runtimeServices struct {
	proxy        *orchestrator.Orchestrator
	accounts     *accountsync.Coordinator
	maintenance  *maintenance.Service
	backendLogin *backendlogin.Service
}

func newRuntimeServices(ctx context.Context, cfg config.Config, database *store.Store, masterKey []byte) (runtimeServices, error) {
	tokenExists := regularFileExists(cfg.AimiliControlTokenFile)
	legacyCredentialsExist := regularFileExists(cfg.XUICredentialsFile)
	if !tokenExists && !legacyCredentialsExist {
		return runtimeServices{}, nil
	}
	if !tokenExists {
		return runtimeServices{}, errors.New("Aimili control token is required when legacy 3x-ui credentials exist")
	}
	if legacyCredentialsExist {
		if err := accountsync.MigrateLegacyCredentials(ctx, database, cfg.XUICredentialsFile, masterKey); err != nil {
			return runtimeServices{}, err
		}
	}
	token, err := aimili.ReadTokenFile(cfg.AimiliControlTokenFile)
	if err != nil {
		return runtimeServices{}, err
	}
	aimiliClient, err := aimili.NewClient(cfg.AimiliControlURL, token)
	clear(token)
	if err != nil {
		return runtimeServices{}, err
	}
	unified, err := database.LoadUnifiedCredentials(ctx, masterKey)
	if err != nil {
		return runtimeServices{}, errors.New("unified credentials are not initialized")
	}
	defer clear(unified.Password)
	xuiCredentials := xui.Credentials{Username: unified.Username, Password: string(unified.Password)}
	xuiClient, err := xui.NewClient(cfg.XUIBaseURL, xuiCredentials)
	if err != nil {
		return runtimeServices{}, err
	}
	if err := ensureRuntimeCredentials(ctx, database, masterKey); err != nil {
		return runtimeServices{}, err
	}
	if err := seedMixedCIDRs(ctx, database, cfg.MixedSourceCIDRs); err != nil {
		return runtimeServices{}, err
	}
	publicHost := "localhost"
	if cfg.PublicOrigin != "" {
		parsed, parseErr := url.Parse(cfg.PublicOrigin)
		if parseErr != nil || parsed.Hostname() == "" {
			return runtimeServices{}, errors.New("resolve public proxy host")
		}
		publicHost = parsed.Hostname()
	}
	var protocolClient *protocoltxn.Client
	if regularDirectoryExists(cfg.ProtocolRequestDir) && regularDirectoryExists(cfg.ProtocolResultDir) {
		protocolClient, err = protocoltxn.New(protocoltxn.Config{
			RequestDir: cfg.ProtocolRequestDir, ResultDir: cfg.ProtocolResultDir,
			Timeout: time.Duration(cfg.ProtocolTimeoutSeconds) * time.Second, PollInterval: 100 * time.Millisecond,
		})
		if err != nil {
			return runtimeServices{}, err
		}
	}
	proxy, err := orchestrator.New(orchestrator.Config{
		MaxGroups: cfg.MaxProxyGroups, VLESSPortStart: cfg.VLESSPortStart, VLESSPortEnd: cfg.VLESSPortEnd,
		MixedPortStart: cfg.MixedPortStart, MixedPortEnd: cfg.MixedPortEnd, AggregateVLESSPort: cfg.AggregateVLESSPort, MainMixedPort: cfg.MainMixedPort, PublicHost: publicHost,
		XrayPath: cfg.XrayPath, ProbeHost: cfg.ProbeHost, ProtocolTransaction: protocolClient,
	}, database, aimiliClient, xuiClient, validator.New(20*time.Second), masterKey)
	if err != nil {
		return runtimeServices{}, err
	}
	accounts, err := accountsync.New(database, masterKey, aimiliClient, xuiClient, time.Now)
	if err != nil {
		return runtimeServices{}, err
	}
	maintenanceService, err := maintenance.New(newMaintenanceConfig(ctx, cfg.MaxProxyGroups, proxy), aimiliClient, xuiClient, proxy, accounts)
	if err != nil {
		return runtimeServices{}, err
	}
	result := runtimeServices{proxy: proxy, accounts: accounts, maintenance: maintenanceService}
	if cfg.AimiliBackendURL != "" && cfg.ExpertModeURL != "" {
		result.backendLogin, err = backendlogin.New(backendlogin.Config{
			AimiliPath: cfg.AimiliBackendURL, AimiliLocation: cfg.AimiliBackendURL,
			XUIPath: cfg.ExpertModeURL, XUILocation: cfg.ExpertModeURL,
		}, accounts, database, aimiliClient, xuiClient, masterKey)
		if err != nil {
			return runtimeServices{}, err
		}
	}
	return result, nil
}

func accountCheckInitialDelay() time.Duration {
	raw := make([]byte, 1)
	if _, err := rand.Read(raw); err != nil {
		return time.Minute
	}
	return time.Duration(30+int(raw[0])%91) * time.Second
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func regularDirectoryExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
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
		if a.cancel != nil {
			a.cancel()
		}
		if a.driftDone != nil {
			<-a.driftDone
		}
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
