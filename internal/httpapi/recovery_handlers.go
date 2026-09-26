package httpapi

import (
	"context"
	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"net/http"
	"strconv"
)

type RecoveryService interface {
	Recovery(context.Context) (aimili.Recovery, error)
	UpdateRecovery(context.Context, aimili.RecoverySettings) (aimili.Recovery, error)
	RetryDedicatedStandby(context.Context, int) error
}

func (s *server) handleRecovery(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if _, ok := s.authenticateOrWrite(w, r); !ok {
			return
		}
	} else if _, ok := s.authorizeSessionMutation(w, r); !ok {
		return
	}
	service, ok := s.maintenance.(RecoveryService)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured")
		return
	}
	if r.Method == http.MethodGet {
		result, err := service.Recovery(r.Context())
		writeMaintenanceResult(w, result, err)
		return
	}
	var input struct {
		Settings *aimili.RecoverySettings `json:"settings"`
	}
	if decodeJSON(r, &input) != nil || input.Settings == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := service.UpdateRecovery(r.Context(), *input.Settings)
	writeMaintenanceResult(w, result, err)
}

func (s *server) handleRetryDedicatedStandby(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeSessionMutation(w, r); !ok {
		return
	}
	service, ok := s.maintenance.(RecoveryService)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "not_configured")
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	var input struct{}
	if err != nil || index < 0 || index > 64 || decodeJSON(r, &input) != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	writeMaintenanceResult(w, struct{}{}, service.RetryDedicatedStandby(r.Context(), index))
}
