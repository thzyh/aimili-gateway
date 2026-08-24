package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/config"
	"github.com/thzyh/aimili-gateway/internal/httpapi"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type App struct {
	handler   http.Handler
	store     *store.Store
	closeOnce sync.Once
	closeErr  error
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	masterKey, err := readMasterKey(cfg.MasterKeyFile)
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
	mux.Handle("/", apiHandler)
	return &App{handler: mux, store: database}, nil
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

func readMasterKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("read gateway master key")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("gateway master key must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("gateway master key permissions are too broad")
	}
	masterKey, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read gateway master key")
	}
	if len(masterKey) != 32 {
		clear(masterKey)
		return nil, errors.New("gateway master key must be exactly 32 bytes")
	}
	return masterKey, nil
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
