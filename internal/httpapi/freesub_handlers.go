package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/freesub"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type freesubBackupResponse struct {
	ID                 string                     `json:"id"`
	CandidateID        string                     `json:"candidateId,omitempty"`
	CountryCode        string                     `json:"countryCode,omitempty"`
	Protocol           string                     `json:"protocol,omitempty"`
	CandidateIP        string                     `json:"candidateIp,omitempty"`
	ExitIP             string                     `json:"exitIp,omitempty"`
	SocksPort          int                        `json:"socksPort,omitempty"`
	PublicPort         int                        `json:"publicPort,omitempty"`
	Status             domain.FreesubBackupStatus `json:"status"`
	RepairAttempts     int                        `json:"repairAttempts"`
	FailureFingerprint string                     `json:"failureFingerprint,omitempty"`
	LastErrorCode      string                     `json:"lastErrorCode,omitempty"`
	Version            int64                      `json:"version"`
	LastCheckedAt      *time.Time                 `json:"lastCheckedAt,omitempty"`
	RiskScore          *int                       `json:"riskScore,omitempty"`
	NativeIP           bool                       `json:"nativeIp,omitempty"`
	NativeLabel        string                     `json:"nativeLabel,omitempty"`
	ScenarioStars      freesubScenarioStars       `json:"scenarioStars,omitempty"`
	QualityCheckedAt   string                     `json:"qualityCheckedAt,omitempty"`
}

type freesubScenarioStars struct {
	TikTok               int `json:"tiktok"`
	CrossBorderEcommerce int `json:"crossBorderEcommerce"`
	SocialMedia          int `json:"socialMedia"`
	AI                   int `json:"ai"`
}

type freesubCandidateResponse struct {
	CandidateID   string               `json:"candidateId"`
	CountryCode   string               `json:"countryCode"`
	Protocol      string               `json:"protocol"`
	ExitIP        string               `json:"exitIp"`
	RiskScore     int                  `json:"riskScore"`
	NativeIP      bool                 `json:"nativeIp"`
	NativeLabel   string               `json:"nativeLabel"`
	ScenarioStars freesubScenarioStars `json:"scenarioStars"`
}

func safeFreesubBackup(connection domain.FreesubBackupConnection) freesubBackupResponse {
	result := freesubBackupResponse{
		ID: connection.ID, CandidateID: connection.CandidateID, CountryCode: connection.CountryCode,
		Protocol: connection.Protocol, CandidateIP: connection.CandidateIP, ExitIP: connection.ExitIP,
		SocksPort: connection.SocksPort, PublicPort: connection.PublicPort, Status: connection.Status,
		RepairAttempts: connection.RepairAttempts, FailureFingerprint: connection.FailureFingerprint,
		LastErrorCode: connection.LastErrorCode, Version: connection.Version,
	}
	if !connection.LastCheckedAt.IsZero() {
		checked := connection.LastCheckedAt
		result.LastCheckedAt = &checked
	}
	return result
}

func scenarioStars(candidate freesub.Candidate) freesubScenarioStars {
	return freesubScenarioStars{
		TikTok:               candidate.Quality.ScenarioStars["tiktok"],
		CrossBorderEcommerce: candidate.Quality.ScenarioStars["cross_border_ecommerce"],
		SocialMedia:          candidate.Quality.ScenarioStars["social_media"],
		AI:                   candidate.Quality.ScenarioStars["ai"],
	}
}

func safeFreesubCandidate(candidate freesub.Candidate) freesubCandidateResponse {
	return freesubCandidateResponse{
		CandidateID: candidate.CandidateID, CountryCode: candidate.Country, Protocol: candidate.Protocol,
		ExitIP: candidate.ExitIP, RiskScore: candidate.RiskScore, NativeIP: candidate.Quality.NativeIP,
		NativeLabel: candidate.Quality.NativeLabel, ScenarioStars: scenarioStars(candidate),
	}
}

func (s *server) freesubResponse(request *http.Request, connection domain.FreesubBackupConnection) freesubBackupResponse {
	result := safeFreesubBackup(connection)
	if s.freesubBackup == nil || connection.CandidateID == "" {
		return result
	}
	candidates, err := s.freesubBackup.Candidates(request.Context())
	if err != nil {
		return result
	}
	for _, candidate := range candidates {
		if candidate.CandidateID != connection.CandidateID {
			continue
		}
		risk := candidate.RiskScore
		result.RiskScore = &risk
		result.NativeIP = candidate.Quality.NativeIP
		result.NativeLabel = candidate.Quality.NativeLabel
		result.ScenarioStars = scenarioStars(candidate)
		result.QualityCheckedAt = candidate.Quality.CheckedAt
		break
	}
	return result
}

func (s *server) handleFreesubBackup(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	var connection domain.FreesubBackupConnection
	var err error
	if s.freesubBackup != nil {
		connection, err = s.freesubBackup.Summary(request.Context())
	} else {
		connection, err = s.store.GetFreesubBackup(request.Context())
	}
	if errors.Is(err, store.ErrFreesubBackupNotFound) {
		writeJSON(response, http.StatusOK, freesubBackupResponse{ID: "agw-freesub", Status: domain.FreesubBackupStandby})
		return
	}
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "freesub_backup_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, s.freesubResponse(request, connection))
}

func (s *server) handleFreesubCandidates(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authenticateOrWrite(response, request); !ok {
		return
	}
	if s.freesubBackup == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "freesub_backup_not_configured")
		return
	}
	candidates, err := s.freesubBackup.Candidates(request.Context())
	if err != nil {
		writeAPIError(response, http.StatusServiceUnavailable, "freesub_candidates_unavailable")
		return
	}
	result := make([]freesubCandidateResponse, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, safeFreesubCandidate(candidate))
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *server) handleCheckFreesubBackup(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request); !ok {
		return
	}
	if s.freesubBackup == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "freesub_backup_not_configured")
		return
	}
	connection, err := s.freesubBackup.Check(request.Context())
	if err != nil {
		writeAPIError(response, http.StatusConflict, "freesub_backup_check_failed")
		return
	}
	writeJSON(response, http.StatusOK, s.freesubResponse(request, connection))
}

func (s *server) handleReplaceFreesubBackup(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request); !ok {
		return
	}
	if s.freesubBackup == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "freesub_backup_not_configured")
		return
	}
	connection, err := s.freesubBackup.Replace(request.Context())
	if err != nil {
		writeAPIError(response, http.StatusConflict, "freesub_backup_replace_failed")
		return
	}
	writeJSON(response, http.StatusOK, s.freesubResponse(request, connection))
}

func (s *server) handleManualProvisionFreesubBackup(response http.ResponseWriter, request *http.Request) {
	if _, ok := s.authorizeMutation(response, request); !ok {
		return
	}
	if s.freesubBackup == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "freesub_backup_not_configured")
		return
	}
	var input struct {
		CandidateID string `json:"candidateId"`
	}
	if err := decodeJSON(request, &input); err != nil || strings.TrimSpace(input.CandidateID) == "" || len(input.CandidateID) > 256 {
		writeAPIError(response, http.StatusBadRequest, "invalid_freesub_candidate")
		return
	}
	connection, err := s.freesubBackup.ManualProvision(request.Context(), strings.TrimSpace(input.CandidateID))
	if err != nil {
		writeAPIError(response, http.StatusConflict, "freesub_backup_manual_provision_failed")
		return
	}
	writeJSON(response, http.StatusOK, s.freesubResponse(request, connection))
}
