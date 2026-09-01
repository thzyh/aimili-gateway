package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type ProxyType string

const (
	ProxyTypeResidential ProxyType = "residential"
	ProxyTypeDatacenter  ProxyType = "datacenter"
)

type ProxyGroupStatus string

type EgressSource string

const (
	EgressSourceSlot EgressSource = "slot"
	EgressSourceMain EgressSource = "main"
)

func (s EgressSource) Valid() bool { return s == EgressSourceSlot || s == EgressSourceMain }

const (
	ProxyGroupStandby        ProxyGroupStatus = "standby"
	ProxyGroupProvisioning   ProxyGroupStatus = "provisioning"
	ProxyGroupReady          ProxyGroupStatus = "ready"
	ProxyGroupRotating       ProxyGroupStatus = "rotating"
	ProxyGroupDegraded       ProxyGroupStatus = "degraded"
	ProxyGroupRepairRequired ProxyGroupStatus = "repair_required"
	ProxyGroupDisabling      ProxyGroupStatus = "disabling"
)

var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

type ProxyGroup struct {
	ID                    string
	ResourceName          string
	CountryCode           string
	CountryName           string
	ProxyType             ProxyType
	CandidateID           string
	CandidateIP           string
	CandidateLatencyMS    int
	VLESSLatencyMS        int
	SOCKSLatencyMS        int
	Status                ProxyGroupStatus
	EgressSource          EgressSource
	AimiliSlot            int
	PublicPort            int
	MixedPort             int
	ProtocolMode          ProtocolMode
	DesiredProtocolMode   ProtocolMode
	ProtocolState         ProtocolState
	ProtocolLastErrorCode string
	ExitIP                string
	ExitIPCheckedAt       float64
	ConfigFingerprint     string
	PublicInboundID       int64
	MixedInboundID        int64
	RealityPublicKey      string
	RealityShortID        string
	RealityServerName     string
	RealityMLDSA65Verify  string
	LastErrorCode         string
	RecoveryState         string
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LastCheckedAt         time.Time
	LastRotatedAt         time.Time
	LastSeenAt            time.Time
}

func NewProxyGroupIdentity(country string, proxyType ProxyType, candidateIDs ...string) (ProxyGroup, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if !countryCodePattern.MatchString(country) || !proxyType.Valid() || len(candidateIDs) > 1 {
		return ProxyGroup{}, errors.New("invalid proxy group identity")
	}
	suffix := "res"
	if proxyType == ProxyTypeDatacenter {
		suffix = "dc"
	}
	name := "agw-" + strings.ToLower(country) + "-" + suffix
	candidateID := ""
	if len(candidateIDs) == 1 {
		candidateID = strings.TrimSpace(candidateIDs[0])
		if candidateID == "" || len(candidateID) > 256 {
			return ProxyGroup{}, errors.New("invalid proxy group candidate")
		}
		digest := sha256.Sum256([]byte(candidateID))
		name = fmt.Sprintf("%s-%x", name, digest[:5])
	}
	return ProxyGroup{
		ID:           name,
		ResourceName: name,
		CountryCode:  country,
		ProxyType:    proxyType,
		CandidateID:  candidateID,
		Status:       ProxyGroupProvisioning,
		Version:      1,
	}, nil
}

func (t ProxyType) Valid() bool {
	return t == ProxyTypeResidential || t == ProxyTypeDatacenter
}

func (s ProxyGroupStatus) Valid() bool {
	switch s {
	case ProxyGroupStandby, ProxyGroupProvisioning, ProxyGroupReady, ProxyGroupRotating, ProxyGroupDegraded, ProxyGroupRepairRequired, ProxyGroupDisabling:
		return true
	default:
		return false
	}
}

func (g *ProxyGroup) Transition(next ProxyGroupStatus) error {
	if g == nil || !next.Valid() {
		return errors.New("invalid proxy group transition")
	}
	allowed := map[ProxyGroupStatus]map[ProxyGroupStatus]bool{
		ProxyGroupProvisioning:   {ProxyGroupReady: true, ProxyGroupDegraded: true, ProxyGroupRepairRequired: true, ProxyGroupDisabling: true},
		ProxyGroupReady:          {ProxyGroupRotating: true, ProxyGroupDegraded: true, ProxyGroupRepairRequired: true, ProxyGroupDisabling: true},
		ProxyGroupRotating:       {ProxyGroupReady: true, ProxyGroupDegraded: true, ProxyGroupRepairRequired: true},
		ProxyGroupDegraded:       {ProxyGroupReady: true, ProxyGroupRotating: true, ProxyGroupRepairRequired: true, ProxyGroupDisabling: true},
		ProxyGroupRepairRequired: {ProxyGroupProvisioning: true, ProxyGroupDisabling: true},
		ProxyGroupDisabling:      {ProxyGroupRepairRequired: true},
	}
	if !allowed[g.Status][next] {
		return errors.New("invalid proxy group transition")
	}
	g.Status = next
	return nil
}
