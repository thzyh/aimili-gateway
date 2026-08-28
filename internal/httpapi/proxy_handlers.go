package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/orchestrator"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type proxyGroupResponse struct {
	ID                 string                  `json:"id"`
	CountryCode        string                  `json:"countryCode"`
	CountryName        string                  `json:"countryName"`
	ProxyType          domain.ProxyType        `json:"proxyType"`
	Status             domain.ProxyGroupStatus `json:"status"`
	EgressSource       domain.EgressSource     `json:"egressSource"`
	VLESSPort          int                     `json:"vlessPort"`
	MixedPort          int                     `json:"mixedPort"`
	ExitIP             string                  `json:"exitIp"`
	CandidateLatencyMS int                     `json:"candidateLatencyMs"`
	VLESSLatencyMS     int                     `json:"vlessLatencyMs"`
	SOCKSLatencyMS     int                     `json:"socksLatencyMs"`
	LastErrorCode      string                  `json:"lastErrorCode,omitempty"`
	Version            int64                   `json:"version"`
	LastCheckedAt      *time.Time              `json:"lastCheckedAt,omitempty"`
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
	groups, err := s.proxyManager.Pool(request.Context())
	if err != nil {
		writeProxyError(response, err)
		return
	}
	groups, valid := filterProxyGroups(groups, request)
	if !valid {
		writeAPIError(response, http.StatusBadRequest, "invalid_filter")
		return
	}
	result := make([]proxyGroupResponse, len(groups))
	for i, group := range groups {
		result[i] = safeProxyGroup(group)
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *server) handleReconcileProxyGroups(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request); !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_ = s.proxyManager.Reconcile(ctx)
	}()
	writeJSON(response, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *server) handleProxyGroupExport(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.proxyManager == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("protocol")))
	if protocol != "vless" && protocol != "socks5h" {
		writeAPIError(response, http.StatusBadRequest, "invalid_protocol")
		return
	}
	groups, err := s.proxyManager.List(request.Context())
	if err != nil {
		writeProxyError(response, err)
		return
	}
	groups, valid := filterProxyGroups(groups, request)
	if !valid {
		writeAPIError(response, http.StatusBadRequest, "invalid_filter")
		return
	}
	lines := make([]string, 0, len(groups))
	for _, group := range groups {
		if group.Status != domain.ProxyGroupReady {
			continue
		}
		connections, connectionErr := s.proxyManager.Connections(request.Context(), group.ID)
		if connectionErr != nil {
			writeProxyError(response, connectionErr)
			return
		}
		if protocol == "vless" {
			lines = append(lines, connections.VLESSURI)
		} else {
			lines = append(lines, connections.SOCKS5HURI)
		}
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="aimili-`+protocol+`.txt"`)
	response.WriteHeader(http.StatusOK)
	if len(lines) > 0 {
		_, _ = response.Write([]byte(strings.Join(lines, "\n") + "\n"))
	}
}

func filterProxyGroups(groups []domain.ProxyGroup, request *http.Request) ([]domain.ProxyGroup, bool) {
	country := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("country")))
	proxyType := domain.ProxyType(strings.ToLower(strings.TrimSpace(request.URL.Query().Get("proxyType"))))
	status := domain.ProxyGroupStatus(strings.ToLower(strings.TrimSpace(request.URL.Query().Get("status"))))
	if country != "" && (len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z') {
		return nil, false
	}
	if proxyType != "" && !proxyType.Valid() {
		return nil, false
	}
	if status != "" && !status.Valid() {
		return nil, false
	}
	result := make([]domain.ProxyGroup, 0, len(groups))
	for _, group := range groups {
		if country != "" && group.CountryCode != country {
			continue
		}
		if proxyType != "" && group.ProxyType != proxyType {
			continue
		}
		if status != "" && group.Status != status {
			continue
		}
		result = append(result, group)
	}
	return result, true
}

func (s *server) handleEnableProxyGroup(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authorizeMutation(response, request)
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
	if _, ok := s.authorizeMutation(response, request); !ok {
		return
	}
	group, err := s.proxyManager.Check(request.Context(), request.PathValue("id"))
	if err != nil {
		writeProxyError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, safeProxyGroup(group))
}

func (s *server) handleActivateProxyGroup(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authorizeMutation(response, request)
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
	group, err := s.proxyManager.Activate(request.Context(), request.PathValue("id"))
	if err != nil {
		writeProxyError(response, err)
		return
	}
	s.storeIdempotent(key, http.StatusOK, safeProxyGroup(group))
	writeJSON(response, http.StatusOK, safeProxyGroup(group))
}

func (s *server) handleRotateProxyGroup(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authorizeMutation(response, request)
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
	if _, ok := s.authorizeMutation(response, request); !ok {
		return
	}
	if err := s.proxyManager.Disable(request.Context(), request.PathValue("id")); err != nil {
		writeProxyError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *server) handleConnections(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	connections, err := s.proxyManager.Connections(request.Context(), request.PathValue("id"))
	if err != nil {
		writeProxyError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, connections)
}

func (s *server) handleAggregateConnections(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	manager, ok := s.proxyManager.(interface {
		AggregateConnections(context.Context) (orchestrator.Connections, error)
	})
	if !ok {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	connections, err := manager.AggregateConnections(request.Context())
	if err != nil {
		writeProxyError(response, err)
		return
	}
	if connections.VLESSURI == "" {
		writeAPIError(response, http.StatusConflict, "not_ready")
		return
	}
	writeJSON(response, http.StatusOK, connections)
}

type mixedPolicyResponse struct {
	Enabled     bool     `json:"enabled"`
	CIDRs       []string `json:"cidrs"`
	ApplyStatus string   `json:"applyStatus"`
}

func (s *server) handleGetMixedPolicy(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.proxyManager == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	policy, err := s.proxyManager.MixedPolicy(request.Context())
	if err != nil {
		writeProxyError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, safeMixedPolicy(policy))
}

func (s *server) handleSetMixedPolicy(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request); !ok {
		return
	}
	var input struct {
		Enabled bool     `json:"enabled"`
		CIDRs   []string `json:"cidrs"`
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
	if input.Enabled && len(prefixes) == 0 {
		writeAPIError(response, http.StatusBadRequest, "mixed_cidr_required")
		return
	}
	if err := s.proxyManager.SetMixedPolicy(request.Context(), store.MixedSourcePolicy{Enabled: input.Enabled, CIDRs: prefixes}); err != nil {
		writeProxyError(response, err)
		return
	}
	policy, err := s.proxyManager.MixedPolicy(request.Context())
	if err != nil {
		writeProxyError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, safeMixedPolicy(policy))
}

func safeMixedPolicy(policy store.MixedSourcePolicy) mixedPolicyResponse {
	return mixedPolicyResponse{Enabled: policy.Enabled, CIDRs: prefixStringsForResponse(policy.CIDRs), ApplyStatus: string(policy.ApplyStatus)}
}

func prefixStringsForResponse(prefixes []netip.Prefix) []string {
	result := make([]string, len(prefixes))
	for index, prefix := range prefixes {
		result[index] = prefix.String()
	}
	return result
}

func (s *server) authorizeMutation(response http.ResponseWriter, request *http.Request) (requestSession, bool) {
	session, ok := s.authorizeSessionMutation(response, request)
	if !ok {
		return requestSession{}, false
	}
	if s.proxyManager == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return requestSession{}, false
	}
	return session, true
}

func (s *server) authorizeSessionMutation(response http.ResponseWriter, request *http.Request) (requestSession, bool) {
	session, ok := s.authenticateOrWrite(response, request)
	if !ok {
		return requestSession{}, false
	}
	if !s.requireOrigin(request) || !validCSRF(request, session) {
		writeAPIError(response, http.StatusForbidden, "forbidden")
		return requestSession{}, false
	}
	return session, true
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
	result := proxyGroupResponse{ID: group.ID, CountryCode: group.CountryCode, CountryName: group.CountryName, ProxyType: group.ProxyType, Status: group.Status, EgressSource: group.EgressSource, VLESSPort: group.VLESSPort, MixedPort: group.MixedPort, ExitIP: group.ExitIP, CandidateLatencyMS: group.CandidateLatencyMS, VLESSLatencyMS: group.VLESSLatencyMS, SOCKSLatencyMS: group.SOCKSLatencyMS, LastErrorCode: group.LastErrorCode, Version: group.Version}
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
