package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/backendlogin"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/freesub"
	"github.com/thzyh/aimili-gateway/internal/maintenance"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
	"github.com/thzyh/aimili-gateway/internal/store"
)

const sessionCookieName = "aimili_gateway_session"

type Dependencies struct {
	Store         *store.Store
	MasterKey     []byte
	PublicOrigin  string
	TestOrigin    string
	Now           func() time.Time
	AimiliProbe   adapters.Prober
	XUIProbe      adapters.Prober
	ExpertModeURL string
	ProxyManager  ProxyManager
	FreesubBackup FreesubBackupManager
	Maintenance   MaintenanceService
	BackendLogin  BackendLoginService
	Updates       UpdateManager
}

type FreesubBackupManager interface {
	Summary(context.Context) (domain.FreesubBackupConnection, error)
	Candidates(context.Context) ([]freesub.Candidate, error)
	Check(context.Context) (domain.FreesubBackupConnection, error)
	Replace(context.Context) (domain.FreesubBackupConnection, error)
	ManualProvision(context.Context, string) (domain.FreesubBackupConnection, error)
}

type MaintenanceService interface {
	Summary(context.Context) (maintenance.Summary, error)
	AimiliVPN(context.Context) (maintenance.AimiliSummary, error)
	CandidateCountries(context.Context) ([]aimili.CandidateCountry, error)
	StartAimiliVPNRefresh(context.Context, string) (aimili.CountryRefresh, error)
	AimiliVPNRefresh(context.Context) (aimili.CountryRefresh, error)
	CheckAimiliVPN(context.Context) (maintenance.AimiliSummary, error)
	XUI(context.Context) (maintenance.XUISummary, error)
	CheckXUI(context.Context) (maintenance.XUISummary, error)
	RepairXUI(context.Context) (maintenance.XUISummary, error)
}

type DedicatedStandbyService interface {
	DedicatedStandbys(context.Context) ([]aimili.DedicatedStandby, error)
	ConfigureDedicatedStandbys(context.Context, []aimili.DedicatedStandbyConfig) ([]aimili.DedicatedStandby, error)
	AssignDedicatedStandby(context.Context, int, string) (aimili.DedicatedStandby, error)
}

type BackendLoginService interface {
	Login(context.Context, backendlogin.Target) (backendlogin.Session, error)
}

type ProxyManager interface {
	Countries(context.Context) ([]orchestrator.Country, error)
	List(context.Context) ([]domain.ProxyGroup, error)
	Pool(context.Context) ([]domain.ProxyGroup, error)
	Enable(context.Context, orchestrator.EnableRequest) (domain.ProxyGroup, error)
	Activate(context.Context, string) (domain.ProxyGroup, error)
	Check(context.Context, string) (domain.ProxyGroup, error)
	Rotate(context.Context, string) (domain.ProxyGroup, error)
	Disable(context.Context, string) error
	Connections(context.Context, string) (orchestrator.Connections, error)
	Subscription(context.Context) (orchestrator.SubscriptionResult, error)
	ReplaceCandidate(context.Context, string, string) (domain.ProxyGroup, error)
	CheckMain(context.Context) (store.MainEgress, error)
	CleanupLegacyAggregate(context.Context) (orchestrator.LegacyAggregateCleanup, error)
	SwitchProtocolModeExpected(context.Context, string, domain.ProtocolMode, domain.ProtocolMode) (domain.EgressProtocolMode, error)
	MixedPolicy(context.Context) (store.MixedSourcePolicy, error)
	SetMixedPolicy(context.Context, store.MixedSourcePolicy) error
	RotateMixedCredentials(context.Context) (time.Time, error)
	Reconcile(context.Context) orchestrator.ReconcileResult
}

type server struct {
	store               *store.Store
	masterKey           []byte
	allowedOrigin       string
	now                 func() time.Time
	limiter             *loginLimiter
	aimiliProbe         adapters.Prober
	xuiProbe            adapters.Prober
	expertModeURL       string
	proxyManager        ProxyManager
	freesubBackup       FreesubBackupManager
	maintenance         MaintenanceService
	backendLogin        BackendLoginService
	updates             UpdateManager
	idempotencyMu       sync.Mutex
	idempotency         map[string]cachedResponse
	idempotencyRequests map[string]string
}

func NewServer(dependencies Dependencies) http.Handler {
	if dependencies.Store == nil {
		panic("httpapi: store is required")
	}
	if len(dependencies.MasterKey) != 32 {
		panic("httpapi: 32-byte master key is required")
	}
	origin := dependencies.PublicOrigin
	if dependencies.TestOrigin != "" {
		origin = dependencies.TestOrigin
	}
	if origin == "" {
		panic("httpapi: allowed origin is required")
	}
	now := dependencies.Now
	if now == nil {
		now = time.Now
	}
	server := &server{
		store:               dependencies.Store,
		masterKey:           append([]byte(nil), dependencies.MasterKey...),
		allowedOrigin:       origin,
		now:                 now,
		limiter:             newLoginLimiter(),
		aimiliProbe:         dependencies.AimiliProbe,
		xuiProbe:            dependencies.XUIProbe,
		expertModeURL:       dependencies.ExpertModeURL,
		proxyManager:        dependencies.ProxyManager,
		freesubBackup:       dependencies.FreesubBackup,
		maintenance:         dependencies.Maintenance,
		backendLogin:        dependencies.BackendLogin,
		updates:             dependencies.Updates,
		idempotency:         make(map[string]cachedResponse),
		idempotencyRequests: make(map[string]string),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/auth/options", server.handleAuthOptions)
	mux.HandleFunc("POST /api/v1/auth/login", server.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", server.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/session", server.handleSession)
	mux.HandleFunc("GET /api/v1/overview", server.handleOverview)
	mux.HandleFunc("GET /api/v1/navigation", server.handleNavigation)
	mux.HandleFunc("GET /api/v1/countries", server.handleCountries)
	mux.HandleFunc("GET /api/v1/proxy-groups", server.handleProxyGroups)
	mux.HandleFunc("GET /api/v1/freesub/backup", server.handleFreesubBackup)
	mux.HandleFunc("GET /api/v1/freesub/candidates", server.handleFreesubCandidates)
	mux.HandleFunc("POST /api/v1/freesub/backup/check", server.handleCheckFreesubBackup)
	mux.HandleFunc("POST /api/v1/freesub/backup/replace", server.handleReplaceFreesubBackup)
	mux.HandleFunc("POST /api/v1/freesub/backup/manual-provision", server.handleManualProvisionFreesubBackup)
	mux.HandleFunc("GET /api/v1/proxy-groups/export", server.handleProxyGroupExport)
	mux.HandleFunc("GET /api/v1/proxy-groups/subscription", server.handleProxySubscription)
	mux.HandleFunc("POST /api/v1/proxy-groups", server.handleEnableProxyGroup)
	mux.HandleFunc("POST /api/v1/proxy-groups/reconcile", server.handleReconcileProxyGroups)
	mux.HandleFunc("POST /api/v1/proxy-groups/{id}/activate", server.handleActivateProxyGroup)
	mux.HandleFunc("POST /api/v1/proxy-groups/{id}/check", server.handleCheckProxyGroup)
	mux.HandleFunc("POST /api/v1/proxy-groups/{id}/rotate", server.handleRotateProxyGroup)
	mux.HandleFunc("POST /api/v1/proxy-groups/{id}/replace", server.handleReplaceProxyGroup)
	mux.HandleFunc("PUT /api/v1/proxy-groups/{id}/protocol-mode", server.handleProtocolMode)
	mux.HandleFunc("POST /api/v1/proxy-groups/agw-main/check", server.handleCheckMainProxyGroup)
	mux.HandleFunc("DELETE /api/v1/proxy-groups/{id}", server.handleDisableProxyGroup)
	mux.HandleFunc("GET /api/v1/proxy-groups/{id}/connections", server.handleConnections)
	mux.HandleFunc("GET /api/v1/proxy-groups/aggregate/connections", server.handleAggregateConnections)
	mux.HandleFunc("POST /api/v1/proxy-groups/legacy-aggregate/cleanup", server.handleCleanupLegacyAggregate)
	mux.HandleFunc("GET /api/v1/settings/mixed-source-policy", server.handleGetMixedPolicy)
	mux.HandleFunc("PUT /api/v1/settings/mixed-source-policy", server.handleSetMixedPolicy)
	mux.HandleFunc("POST /api/v1/settings/mixed-source-policy/authorize-current", server.handleAuthorizeCurrentMixedPolicy)
	mux.HandleFunc("POST /api/v1/settings/socks5h-credentials/rotate", server.handleRotateMixedCredentials)
	mux.HandleFunc("GET /api/v1/settings/summary", server.handleSettingsSummary)
	mux.HandleFunc("GET /api/v1/settings/aimilivpn", server.handleAimiliSettings)
	mux.HandleFunc("GET /api/v1/settings/aimilivpn/countries", server.handleAimiliCountries)
	mux.HandleFunc("GET /api/v1/settings/aimilivpn/refresh", server.handleAimiliRefreshStatus)
	mux.HandleFunc("POST /api/v1/settings/aimilivpn/refresh", server.handleRefreshAimiliSettings)
	mux.HandleFunc("POST /api/v1/settings/aimilivpn/check", server.handleCheckAimiliSettings)
	mux.HandleFunc("GET /api/v1/settings/aimilivpn/standbys", server.handleDedicatedStandbys)
	mux.HandleFunc("PUT /api/v1/settings/aimilivpn/standbys", server.handleConfigureDedicatedStandbys)
	mux.HandleFunc("POST /api/v1/settings/aimilivpn/standbys/{index}/assign", server.handleAssignDedicatedStandby)
	mux.HandleFunc("GET /api/v1/settings/3x-ui", server.handleXUISettings)
	mux.HandleFunc("POST /api/v1/settings/3x-ui/check", server.handleCheckXUISettings)
	mux.HandleFunc("POST /api/v1/settings/3x-ui/repair", server.handleRepairXUISettings)
	mux.HandleFunc("POST /api/v1/backends/aimilivpn/login", server.handleAimiliBackendLogin)
	mux.HandleFunc("POST /api/v1/backends/3x-ui/login", server.handleXUIBackendLogin)
	mux.HandleFunc("GET /api/v1/system/updates", server.handleListUpdates)
	mux.HandleFunc("POST /api/v1/system/updates/{kind}/{version}/apply", server.handleApplyUpdate)
	mux.HandleFunc("POST /api/v1/system/updates/{kind}/rollback", server.handleRollbackUpdate)
	mux.HandleFunc("GET /api/v1/system/updates/{runId}", server.handleUpdateStatus)
	return noStore(mux)
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(response, request)
	})
}

func decodeJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return &multipleJSONValuesError{}
		}
		return err
	}
	return nil
}

type multipleJSONValuesError struct{}

func (*multipleJSONValuesError) Error() string { return "multiple JSON values" }

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(response).Encode(value)
	}
}

func writeAPIError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"error": code})
}
