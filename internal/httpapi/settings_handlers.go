package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/maintenance"
)

func (s *server) dedicatedStandbyService(response http.ResponseWriter) (DedicatedStandbyService, bool) {
	service, ok := s.maintenance.(DedicatedStandbyService)
	if !ok {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
	}
	return service, ok
}

func (s *server) handleDedicatedStandbys(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	service, ok := s.dedicatedStandbyService(response)
	if !ok {
		return
	}
	result, err := service.DedicatedStandbys(request.Context())
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleConfigureDedicatedStandbys(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeSessionMutation(response, request); !ok {
		return
	}
	service, ok := s.dedicatedStandbyService(response)
	if !ok {
		return
	}
	var input struct {
		Standbys []aimili.DedicatedStandbyConfig `json:"standbys"`
	}
	if decodeJSON(request, &input) != nil || len(input.Standbys) != 2 {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := service.ConfigureDedicatedStandbys(request.Context(), input.Standbys)
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleAssignDedicatedStandby(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeSessionMutation(response, request); !ok {
		return
	}
	service, ok := s.dedicatedStandbyService(response)
	if !ok {
		return
	}
	index, err := strconv.Atoi(request.PathValue("index"))
	if err != nil || index < 0 || index > 1 {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	var input struct {
		CandidateID string `json:"candidateId"`
	}
	if decodeJSON(request, &input) != nil || strings.TrimSpace(input.CandidateID) == "" || len(input.CandidateID) > 256 {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	result, callErr := service.AssignDedicatedStandby(request.Context(), index, strings.TrimSpace(input.CandidateID))
	writeMaintenanceResult(response, result, callErr)
}

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

func (s *server) capacityService(response http.ResponseWriter) (CapacityService, bool) {
	service, ok := s.maintenance.(CapacityService)
	if !ok {
		writeAPIError(response, http.StatusServiceUnavailable, "not_configured")
	}
	return service, ok
}

func (s *server) handleCapacity(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	service, ok := s.capacityService(response)
	if !ok {
		return
	}
	result, err := service.Capacity(request.Context())
	writeMaintenanceResult(response, result, err)
}

func (s *server) handleUpdateCapacity(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeSessionMutation(response, request); !ok {
		return
	}
	service, ok := s.capacityService(response)
	if !ok {
		return
	}
	var input struct {
		TargetValidNodeCount *int `json:"targetValidNodeCount"`
		MaxValidNodeCount    *int `json:"maxValidNodeCount"`
		RegularExitSlots     *int `json:"regularExitSlots"`
	}
	if decodeJSON(request, &input) != nil || input.TargetValidNodeCount == nil && input.MaxValidNodeCount == nil && input.RegularExitSlots == nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := service.UpdateCapacity(request.Context(), aimili.CapacityUpdate{
		TargetValidNodeCount: input.TargetValidNodeCount,
		MaxValidNodeCount:    input.MaxValidNodeCount,
		RegularExitSlots:     input.RegularExitSlots,
	})
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
	if country != "ALL" && (len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z') {
		writeAPIError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	key, hit, ok := s.idempotencyKey(response, request, session, country)
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
	if code == "repair_failed" || code == "check_failed" || code == "maintenance_busy" || code == "operation_busy" || code == "candidate_unavailable" || code == "candidate_in_use" || code == "capacity_limit_exceeded" || code == "capacity_below_active" || code == "capacity_upgrade_required" || code == "capacity_update_failed" {
		status = http.StatusConflict
	} else if code == "invalid_request" || code == "country_required" || code == "slot_not_found" || code == "standby_disabled" || code == "invalid_capacity" {
		status = http.StatusBadRequest
	}
	writeAPIError(response, status, code)
}
