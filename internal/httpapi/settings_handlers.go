package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/maintenance"
)

func (s *server) handleSettingsSummary(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.maintenance == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	result, err := s.maintenance.Summary(request.Context())
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleAimiliSettings(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.maintenance == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	result, err := s.maintenance.AimiliVPN(request.Context())
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleRefreshAimiliSettings(response http.ResponseWriter, request *http.Request) {
	session, ok := s.authorizeSessionMutation(response, request)
	if !ok {
		return
	}
	if s.maintenance == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	var input struct {
		Country string `json:"country"`
	}
	if decodeJSON(request, &input) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	country := strings.ToUpper(strings.TrimSpace(input.Country))
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
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
	result, err := s.maintenance.StartAimiliVPNRefresh(request.Context(), country)
	if err != nil {
		writeMaintenanceError(response, err)
		return
	}
	s.storeIdempotent(key, http.StatusAccepted, result)
	writeJSON(response, http.StatusAccepted, result)
}

func (s *server) handleAimiliCountries(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.maintenance == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	result, err := s.maintenance.CandidateCountries(request.Context())
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleAimiliRefreshStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.maintenance == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	result, err := s.maintenance.AimiliVPNRefresh(request.Context())
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleCheckAimiliSettings(response http.ResponseWriter, request *http.Request) {
	s.handleMaintenanceMutation(response, request, func() (any, error) { return s.maintenance.CheckAimiliVPN(request.Context()) })
}

func (s *server) handleXUISettings(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.maintenance == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	result, err := s.maintenance.XUI(request.Context())
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleCheckXUISettings(response http.ResponseWriter, request *http.Request) {
	s.handleMaintenanceMutation(response, request, func() (any, error) { return s.maintenance.CheckXUI(request.Context()) })
}

func (s *server) handleRepairXUISettings(response http.ResponseWriter, request *http.Request) {
	s.handleMaintenanceMutation(response, request, func() (any, error) { return s.maintenance.RepairXUI(request.Context()) })
}

func (s *server) handleMaintenanceMutation(response http.ResponseWriter, request *http.Request, operation func() (any, error)) {
	if _, ok := s.authorizeSessionMutation(response, request); !ok {
		return
	}
	if s.maintenance == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
		return
	}
	if !emptyRequestBody(request) {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := operation()
	writeMaintenanceResult(response, result, err)
}

func writeMaintenanceResult(response http.ResponseWriter, result any, err error) {
	if err == nil {
		writeJSON(response, http.StatusOK, result)
		return
	}
	writeMaintenanceError(response, err)
}

func writeMaintenanceError(response http.ResponseWriter, err error) {
	code := "service_unavailable"
	var maintenanceError *maintenance.Error
	if errors.As(err, &maintenanceError) {
		code = maintenanceError.Code
	}
	status := http.StatusServiceUnavailable
	if code == "repair_failed" || code == "check_failed" || code == "maintenance_busy" {
		status = http.StatusConflict
	} else if code == "invalid_request" || code == "country_required" {
		status = http.StatusBadRequest
	}
	writeAPIError(response, status, code)
}
