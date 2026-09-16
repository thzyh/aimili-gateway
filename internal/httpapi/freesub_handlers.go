package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
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
	writeJSON(response, http.StatusOK, safeFreesubBackup(connection))
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
	writeJSON(response, http.StatusOK, safeFreesubBackup(connection))
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
	writeJSON(response, http.StatusOK, safeFreesubBackup(connection))
}
