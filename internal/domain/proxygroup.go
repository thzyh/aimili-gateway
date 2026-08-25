package domain

import (
	"errors"
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

const (
	ProxyGroupProvisioning   ProxyGroupStatus = "provisioning"
	ProxyGroupReady          ProxyGroupStatus = "ready"
	ProxyGroupRotating       ProxyGroupStatus = "rotating"
	ProxyGroupDegraded       ProxyGroupStatus = "degraded"
	ProxyGroupRepairRequired ProxyGroupStatus = "repair_required"
	ProxyGroupDisabling      ProxyGroupStatus = "disabling"
)

var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

type ProxyGroup struct {
	ID                string
	ResourceName      string
	CountryCode       string
	CountryName       string
	ProxyType         ProxyType
	Status            ProxyGroupStatus
	AimiliSlot        int
	VLESSPort         int
	MixedPort         int
	ExitIP            string
	ConfigFingerprint string
	LastErrorCode     string
	RecoveryState     string
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastCheckedAt     time.Time
	LastRotatedAt     time.Time
}

func NewProxyGroupIdentity(country string, proxyType ProxyType) (ProxyGroup, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if !countryCodePattern.MatchString(country) || !proxyType.Valid() {
		return ProxyGroup{}, errors.New("invalid proxy group identity")
	}
	suffix := "res"
	if proxyType == ProxyTypeDatacenter {
		suffix = "dc"
	}
	name := "agw-" + strings.ToLower(country) + "-" + suffix
	return ProxyGroup{
		ID:           name,
		ResourceName: name,
		CountryCode:  country,
		ProxyType:    proxyType,
		Status:       ProxyGroupProvisioning,
		Version:      1,
	}, nil
}

func (t ProxyType) Valid() bool {
	return t == ProxyTypeResidential || t == ProxyTypeDatacenter
}

func (s ProxyGroupStatus) Valid() bool {
	switch s {
	case ProxyGroupProvisioning, ProxyGroupReady, ProxyGroupRotating, ProxyGroupDegraded, ProxyGroupRepairRequired, ProxyGroupDisabling:
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
