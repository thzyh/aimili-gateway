package httpapi

import (
	"errors"
	"net/http"

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
	s.handleMaintenanceMutation(response, request, func() (any, error) { return s.maintenance.RefreshAimiliVPN(request.Context()) })
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
	code := "service_unavailable"
	var maintenanceError *maintenance.Error
	if errors.As(err, &maintenanceError) {
		code = maintenanceError.Code
	}
	status := http.StatusServiceUnavailable
	if code == "repair_failed" || code == "check_failed" {
		status = http.StatusConflict
	}
	writeAPIError(response, status, code)
}
