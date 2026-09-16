package domain

import (
	"errors"
	"strings"
	"time"
)

// FreesubBackupStatus is deliberately separate from ProxyGroupStatus: a
// freesub connection never owns an AimiliVPN slot or a regular egress group.
type FreesubBackupStatus string

const (
	FreesubBackupStandby        FreesubBackupStatus = "standby"
	FreesubBackupProvisioning   FreesubBackupStatus = "provisioning"
	FreesubBackupReady          FreesubBackupStatus = "ready"
	FreesubBackupDegraded       FreesubBackupStatus = "degraded"
	FreesubBackupRepairRequired FreesubBackupStatus = "repair_required"
	FreesubBackupWaitingManual  FreesubBackupStatus = "waiting_manual"
)

type FreesubBackupConnection struct {
	ID                 string
	CandidateID        string
	CountryCode        string
	Protocol           string
	CandidateIP        string
	ExitIP             string
	SocksPort          int
	PublicPort         int
	XUIInboundID       int64
	RuntimePID         int64
	Status             FreesubBackupStatus
	RepairAttempts     int
	FailureFingerprint string
	LastErrorCode      string
	CandidateConfig    []byte
	Version            int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastCheckedAt      time.Time
}

func (s FreesubBackupStatus) Valid() bool {
	switch s {
	case FreesubBackupStandby, FreesubBackupProvisioning, FreesubBackupReady,
		FreesubBackupDegraded, FreesubBackupRepairRequired, FreesubBackupWaitingManual:
		return true
	default:
		return false
	}
}

func (c FreesubBackupConnection) Validate() error {
	if c.ID != "agw-freesub" || strings.TrimSpace(c.CandidateID) == "" || len(c.CandidateID) > 256 {
		return errors.New("invalid freesub backup identity")
	}
	if len(c.CountryCode) != 2 || c.CountryCode != strings.ToUpper(c.CountryCode) {
		return errors.New("invalid freesub backup country")
	}
	if c.Protocol != "vless" && c.Protocol != "vmess" && c.Protocol != "trojan" && c.Protocol != "shadowsocks" {
		return errors.New("invalid freesub backup protocol")
	}
	if !c.Status.Valid() || c.RepairAttempts < 0 || c.RepairAttempts > 1 || c.Version < 1 {
		return errors.New("invalid freesub backup state")
	}
	return nil
}

func (c *FreesubBackupConnection) Transition(next FreesubBackupStatus) error {
	if c == nil || !next.Valid() {
		return errors.New("invalid freesub backup transition")
	}
	allowed := map[FreesubBackupStatus]map[FreesubBackupStatus]bool{
		FreesubBackupStandby:        {FreesubBackupProvisioning: true, FreesubBackupDegraded: true},
		FreesubBackupProvisioning:   {FreesubBackupReady: true, FreesubBackupDegraded: true, FreesubBackupRepairRequired: true},
		FreesubBackupReady:          {FreesubBackupDegraded: true, FreesubBackupProvisioning: true},
		FreesubBackupDegraded:       {FreesubBackupProvisioning: true, FreesubBackupRepairRequired: true, FreesubBackupWaitingManual: true},
		FreesubBackupRepairRequired: {FreesubBackupProvisioning: true, FreesubBackupWaitingManual: true},
		FreesubBackupWaitingManual:  {FreesubBackupProvisioning: true},
	}
	if !allowed[c.Status][next] {
		return errors.New("invalid freesub backup transition")
	}
	c.Status = next
	return nil
}

// BeginAutoReplacement atomically in memory marks the only permitted repair
// attempt. The persistent store must save the returned object before starting
// a new runtime, so restarts cannot reset the counter.
func (c *FreesubBackupConnection) BeginAutoReplacement(fingerprint string) error {
	if c == nil || c.Status != FreesubBackupDegraded || c.RepairAttempts != 0 || strings.TrimSpace(fingerprint) == "" {
		return errors.New("automatic replacement is not available")
	}
	c.RepairAttempts = 1
	c.FailureFingerprint = fingerprint
	c.Status = FreesubBackupProvisioning
	return nil
}
