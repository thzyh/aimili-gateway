package httpapi

import (
	"net/http"

	"github.com/thzyh/aimili-gateway/internal/adapters"
)

func (s *server) handleOverview(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	services := make([]adapters.ProbeResult, 0, 2)
	if s.aimiliProbe != nil {
		services = append(services, s.aimiliProbe.Probe(request.Context()))
	}
	if s.xuiProbe != nil {
		services = append(services, s.xuiProbe.Probe(request.Context()))
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"gateway": map[string]adapters.Health{
			"health": adapters.HealthHealthy,
		},
		"services":            services,
		"expertModeAvailable": s.expertModeURL != "",
	})
}

func (s *server) handleNavigation(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{
		"expertModeUrl": s.expertModeURL,
	})
}
