package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
)

const recentReauthentication = 5 * time.Minute

type proxyGroupResponse struct {
	ID            string                  `json:"id"`
	CountryCode   string                  `json:"countryCode"`
	CountryName   string                  `json:"countryName"`
	ProxyType     domain.ProxyType        `json:"proxyType"`
	Status        domain.ProxyGroupStatus `json:"status"`
	VLESSPort     int                     `json:"vlessPort"`
	MixedPort     int                     `json:"mixedPort"`
	ExitIP        string                  `json:"exitIp"`
	LastErrorCode string                  `json:"lastErrorCode,omitempty"`
	Version       int64                   `json:"version"`
	LastCheckedAt *time.Time              `json:"lastCheckedAt,omitempty"`
}

type cachedResponse struct {
	status int
	body   []byte
}

func (s *server) handleCountries(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.proxyManager == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	result, err := s.proxyManager.Countries(request.Context())
	if err != nil {
		writeProxyError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *server) handleProxyGroups(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.proxyManager == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	groups, err := s.proxyManager.List(request.Context())
	if err != nil {
		writeProxyError(response, err)
		return
	}
	result := make([]proxyGroupResponse, len(groups))
	for i, group := range groups {
		result[i] = safeProxyGroup(group)
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *server) handleEnableProxyGroup(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authorizeMutation(response, request, true)
	if !ok {
		return
	}
	var input struct {
		CountryCode string           `json:"countryCode"`
		ProxyType   domain.ProxyType `json:"proxyType"`
	}
	if decodeJSON(request, &input) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	identity, err := domain.NewProxyGroupIdentity(input.CountryCode, input.ProxyType)
	if err != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	key, hit, ok := s.idempotencyKey(response, request, session)
	if !ok {
		return
	}
	if hit != nil {
		writeCached(response, *hit)
		return
	}
	group, err := s.proxyManager.Enable(request.Context(), orchestrator.EnableRequest{CountryCode: identity.CountryCode, ProxyType: identity.ProxyType})
	if err != nil {
		writeProxyError(response, err)
		return
	}
	s.storeIdempotent(key, http.StatusCreated, safeProxyGroup(group))
	writeJSON(response, http.StatusCreated, safeProxyGroup(group))
}

func (s *server) handleCheckProxyGroup(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request, false); !ok {
		return
	}
	group, err := s.proxyManager.Check(request.Context(), request.PathValue("id"))
	if err != nil {
		writeProxyError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, safeProxyGroup(group))
}

func (s *server) handleRotateProxyGroup(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authorizeMutation(response, request, true)
	if !ok {
		return
	}
	key, hit, ok := s.idempotencyKey(response, request, session)
	if !ok {
		return
	}
	if hit != nil {
		writeCached(response, *hit)
		return
	}
	group, err := s.proxyManager.Rotate(request.Context(), request.PathValue("id"))
	if err != nil {
		writeProxyError(response, err)
		return
	}
	s.storeIdempotent(key, http.StatusOK, safeProxyGroup(group))
	writeJSON(response, http.StatusOK, safeProxyGroup(group))
}

func (s *server) handleDisableProxyGroup(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request, true); !ok {
		return
	}
	if err := s.proxyManager.Disable(request.Context(), request.PathValue("id")); err != nil {
		writeProxyError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *server) handleConnections(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authenticateOrWrite(response, request)
	if !ok {
		return
	}
	if !s.recentlyReauthenticated(session) {
		writeAPIError(response, http.StatusPreconditionRequired, "reauthentication_required")
		return
	}
	connections, err := s.proxyManager.Connections(request.Context(), request.PathValue("id"))
	if err != nil {
		writeProxyError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, connections)
}

func (s *server) handleMixedCIDRs(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request, true); !ok {
		return
	}
	var input struct {
		CIDRs []string `json:"cidrs"`
	}
	if decodeJSON(request, &input) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	prefixes := make([]netip.Prefix, 0, len(input.CIDRs))
	for _, raw := range input.CIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() {
			writeAPIError(response, http.StatusBadRequest, "invalid_cidr")
			return
		}
		prefixes = append(prefixes, prefix)
	}
	if err := s.proxyManager.SetMixedCIDRs(request.Context(), prefixes); err != nil {
		writeProxyError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *server) authorizeMutation(response http.ResponseWriter, request *http.Request, requireRecent bool) (requestSession, bool) {
	session, ok := s.authenticateOrWrite(response, request)
	if !ok {
		return requestSession{}, false
	}
	if !s.requireOrigin(request) || !validCSRF(request, session) {
		writeAPIError(response, http.StatusForbidden, "forbidden")
		return requestSession{}, false
	}
	if requireRecent && !s.recentlyReauthenticated(session) {
		writeAPIError(response, http.StatusPreconditionRequired, "reauthentication_required")
		return requestSession{}, false
	}
	if s.proxyManager == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return requestSession{}, false
	}
	return session, true
}
func (s *server) recentlyReauthenticated(session requestSession) bool {
	return session.stored.ReauthenticatedAt != nil && !s.now().UTC().Before(session.stored.ReauthenticatedAt.UTC()) && s.now().UTC().Sub(session.stored.ReauthenticatedAt.UTC()) <= recentReauthentication
}
func (s *server) idempotencyKey(response http.ResponseWriter, request *http.Request, session requestSession) (string, *cachedResponse, bool) {
	raw := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if raw == "" || len(raw) > 128 {
		writeAPIError(response, http.StatusPreconditionRequired, "idempotency_key_required")
		return "", nil, false
	}
	key := idempotencyCacheKey(session.stored.ID, request.Method, request.URL.Path, raw)
	s.idempotencyMu.Lock()
	defer s.idempotencyMu.Unlock()
	if cached, exists := s.idempotency[key]; exists {
		return key, &cached, true
	}
	return key, nil, true
}

func idempotencyCacheKey(sessionID int64, method, path, raw string) string {
	return strings.Join([]string{strconv.FormatInt(sessionID, 10), method, path, raw}, "\x00")
}
func (s *server) storeIdempotent(key string, status int, value any) {
	body, _ := json.Marshal(value)
	s.idempotencyMu.Lock()
	s.idempotency[key] = cachedResponse{status: status, body: body}
	s.idempotencyMu.Unlock()
}
func writeCached(response http.ResponseWriter, cached cachedResponse) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(cached.status)
	_, _ = response.Write(append(cached.body, '\n'))
}
func safeProxyGroup(group domain.ProxyGroup) proxyGroupResponse {
	result := proxyGroupResponse{ID: group.ID, CountryCode: group.CountryCode, CountryName: group.CountryName, ProxyType: group.ProxyType, Status: group.Status, VLESSPort: group.VLESSPort, MixedPort: group.MixedPort, ExitIP: group.ExitIP, LastErrorCode: group.LastErrorCode, Version: group.Version}
	if !group.LastCheckedAt.IsZero() {
		checked := group.LastCheckedAt
		result.LastCheckedAt = &checked
	}
	return result
}
func writeProxyError(response http.ResponseWriter, err error) {
	var operationError *orchestrator.Error
	if !errors.As(err, &operationError) {
		writeAPIError(response, http.StatusInternalServerError, "internal_error")
		return
	}
	status := http.StatusConflict
	switch operationError.Code {
	case "invalid_request":
		status = http.StatusBadRequest
	case "not_found":
		status = http.StatusNotFound
	case "not_configured", "credentials_not_configured":
		status = http.StatusServiceUnavailable
	case "operation_failed", "storage_failed":
		status = http.StatusInternalServerError
	}
	writeAPIError(response, status, operationError.Code)
}
