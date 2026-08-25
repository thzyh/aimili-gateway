package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters"
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
}

type server struct {
	store         *store.Store
	masterKey     []byte
	allowedOrigin string
	now           func() time.Time
	limiter       *loginLimiter
	aimiliProbe   adapters.Prober
	xuiProbe      adapters.Prober
	expertModeURL string
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
		store:         dependencies.Store,
		masterKey:     append([]byte(nil), dependencies.MasterKey...),
		allowedOrigin: origin,
		now:           now,
		limiter:       newLoginLimiter(),
		aimiliProbe:   dependencies.AimiliProbe,
		xuiProbe:      dependencies.XUIProbe,
		expertModeURL: dependencies.ExpertModeURL,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/auth/options", server.handleAuthOptions)
	mux.HandleFunc("POST /api/v1/auth/login", server.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", server.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/session", server.handleSession)
	mux.HandleFunc("POST /api/v1/auth/reauth", server.handleReauthenticate)
	mux.HandleFunc("GET /api/v1/overview", server.handleOverview)
	mux.HandleFunc("GET /api/v1/navigation", server.handleNavigation)
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
