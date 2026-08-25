package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
	"github.com/thzyh/aimili-gateway/internal/validator"
)

const (
	credentialVLESSClientID = "vless-client-id"
	credentialMixedUsername = "mixed-username"
	credentialMixedPassword = "mixed-password"
)

type Config struct {
	MaxGroups      int
	VLESSPortStart int
	VLESSPortEnd   int
	MixedPortStart int
	MixedPortEnd   int
	PublicHost     string
	XrayPath       string
	ProbeHost      string
	ReadyTimeout   time.Duration
	PollInterval   time.Duration
	Now            func() time.Time
}

type EnableRequest struct {
	CountryCode string
	ProxyType   domain.ProxyType
}

type Country struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	ResidentialCount int    `json:"residentialCount"`
	DatacenterCount  int    `json:"datacenterCount"`
}

type Connections struct {
	VLESSURI   string `json:"vlessUri"`
	SOCKS5HURI string `json:"socks5hUri"`
}

type Error struct{ Code string }

func (e *Error) Error() string { return "proxy group operation failed: " + e.Code }

type groupStore interface {
	CreateProxyGroup(context.Context, domain.ProxyGroup) error
	GetProxyGroup(context.Context, string) (domain.ProxyGroup, error)
	ListProxyGroups(context.Context) ([]domain.ProxyGroup, error)
	UpdateProxyGroup(context.Context, domain.ProxyGroup, int64) error
	DeleteProxyGroup(context.Context, string) error
	GetCredential(context.Context, string, []byte) ([]byte, error)
	ListMixedCIDRs(context.Context) ([]netip.Prefix, error)
	ReplaceMixedCIDRs(context.Context, []netip.Prefix) error
}

type aimiliClient interface {
	Candidates(context.Context) ([]aimili.Candidate, error)
	CreateSlot(context.Context, aimili.CreateSlotRequest) (aimili.Slot, error)
	CheckSlot(context.Context, int) (aimili.SlotCheck, error)
	RotateSlot(context.Context, int) (aimili.Slot, error)
	DeleteSlot(context.Context, int) error
}

type xuiClient interface {
	EnsureManagedGroup(context.Context, xui.DesiredGroup) (xui.ManagedGroup, error)
	DeleteManagedGroup(context.Context, xui.ManagedGroup) error
}

type proxyValidator interface {
	ValidateSOCKS5H(context.Context, validator.SOCKSTarget) (validator.Result, error)
	ValidateVLESS(context.Context, validator.VLESSTarget) (validator.Result, error)
}

type Orchestrator struct {
	config    Config
	store     groupStore
	aimili    aimiliClient
	xui       xuiClient
	validator proxyValidator
	masterKey []byte
	locks     operationLocks
}

func New(config Config, database groupStore, aimiliAdapter aimiliClient, xuiAdapter xuiClient, validation proxyValidator, masterKey []byte) (*Orchestrator, error) {
	if config.MaxGroups < 1 || config.VLESSPortStart < 1 || config.VLESSPortEnd < config.VLESSPortStart ||
		config.MixedPortStart < 1 || config.MixedPortEnd < config.MixedPortStart || net.ParseIP(config.PublicHost) != nil ||
		strings.TrimSpace(config.PublicHost) == "" || strings.TrimSpace(config.XrayPath) == "" || strings.TrimSpace(config.ProbeHost) == "" ||
		database == nil || aimiliAdapter == nil || xuiAdapter == nil || validation == nil || len(masterKey) != 32 {
		return nil, errors.New("invalid orchestrator configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 60 * time.Second
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 2 * time.Second
	}
	return &Orchestrator{config: config, store: database, aimili: aimiliAdapter, xui: xuiAdapter, validator: validation, masterKey: append([]byte(nil), masterKey...)}, nil
}

func (o *Orchestrator) Countries(ctx context.Context) ([]Country, error) {
	candidates, err := o.aimili.Candidates(ctx)
	if err != nil {
		return nil, operationError(err)
	}
	byCode := make(map[string]*Country)
	for _, candidate := range candidates {
		code := strings.ToUpper(candidate.CountryCode)
		if len(code) != 2 || candidate.ProbeStatus != "available" {
			continue
		}
		country := byCode[code]
		if country == nil {
			country = &Country{Code: code, Name: candidate.CountryName}
			byCode[code] = country
		}
		switch candidate.ProxyType {
		case string(domain.ProxyTypeResidential):
			country.ResidentialCount++
		case string(domain.ProxyTypeDatacenter):
			country.DatacenterCount++
		}
	}
	result := make([]Country, 0, len(byCode))
	for _, country := range byCode {
		result = append(result, *country)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result, nil
}

func (o *Orchestrator) List(ctx context.Context) ([]domain.ProxyGroup, error) {
	return o.store.ListProxyGroups(ctx)
}

func (o *Orchestrator) Enable(ctx context.Context, request EnableRequest) (domain.ProxyGroup, error) {
	identity, err := domain.NewProxyGroupIdentity(request.CountryCode, request.ProxyType)
	if err != nil {
		return domain.ProxyGroup{}, &Error{Code: "invalid_request"}
	}
	unlock := o.locks.lock("all")
	defer unlock()
	groups, err := o.store.ListProxyGroups(ctx)
	if err != nil {
		return domain.ProxyGroup{}, &Error{Code: "storage_failed"}
	}
	if len(groups) >= o.config.MaxGroups {
		return domain.ProxyGroup{}, &Error{Code: "capacity_exceeded"}
	}
	cidrs, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return domain.ProxyGroup{}, err
	}
	vlessPort, mixedPort, ok := o.allocatePorts(groups)
	if !ok {
		return domain.ProxyGroup{}, &Error{Code: "port_capacity_exceeded"}
	}
	now := o.config.Now().UTC()
	identity.AimiliSlot = 0
	identity.VLESSPort = vlessPort
	identity.MixedPort = mixedPort
	identity.CreatedAt = now
	identity.UpdatedAt = now
	if err := o.store.CreateProxyGroup(ctx, identity); err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	group := identity
	slot, err := o.aimili.CreateSlot(ctx, aimili.CreateSlotRequest{Country: group.CountryCode, ProxyType: string(group.ProxyType)})
	if err != nil {
		_ = o.store.DeleteProxyGroup(ctx, group.ID)
		return domain.ProxyGroup{}, operationError(err)
	}
	group.AimiliSlot = slot.Number
	group.CountryName = slot.CountryName
	checked, err := o.waitForSlot(ctx, slot.Number)
	if err != nil || !checked.EgressOK || net.ParseIP(checked.ExitIP) == nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, xui.ManagedGroup{}, codeOr(err, "egress_unavailable"))
	}
	group.ExitIP = checked.ExitIP
	managed, err := o.xui.EnsureManagedGroup(ctx, xui.DesiredGroup{ResourceName: group.ResourceName, SOCKSPort: checked.Port, VLESSPort: group.VLESSPort, MixedPort: group.MixedPort, VLESSClientID: string(credentials.vlessID), MixedUsername: string(credentials.mixedUsername), MixedPassword: string(credentials.mixedPassword), MixedSourceCIDRs: prefixStrings(cidrs)})
	if err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, xui.ManagedGroup{}, errorCode(err))
	}
	group.ConfigFingerprint = managed.Fingerprint
	group.VLESSInboundID = managed.VLESSInboundID
	group.MixedInboundID = managed.MixedInboundID
	group.RealityPublicKey = managed.PublicKey
	group.RealityShortID = managed.ShortID
	group.RealityServerName = managed.ServerName
	if _, err = o.validateSOCKS(ctx, group, credentials); err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, errorCode(err))
	}
	if _, err = o.validateVLESS(ctx, group, credentials); err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, errorCode(err))
	}
	group.Status = domain.ProxyGroupReady
	group.LastCheckedAt = o.config.Now().UTC()
	group.LastErrorCode = ""
	group.UpdatedAt = group.LastCheckedAt
	if err := o.save(ctx, &group); err != nil {
		return domain.ProxyGroup{}, o.rollbackEnable(ctx, &group, managed, "storage_failed")
	}
	return group, nil
}

func (o *Orchestrator) Check(ctx context.Context, id string) (domain.ProxyGroup, error) {
	unlock := o.locks.lock(id)
	defer unlock()
	group, err := o.store.GetProxyGroup(ctx, id)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	credentialsErr := error(nil)
	_, credentials, credentialsErr := o.runtimeInputs(ctx)
	if credentialsErr != nil {
		return domain.ProxyGroup{}, credentialsErr
	}
	checked, err := o.aimili.CheckSlot(ctx, group.AimiliSlot)
	if err == nil && checked.EgressOK {
		group.ExitIP = checked.ExitIP
		_, err = o.validateSOCKS(ctx, group, credentials)
		if err == nil {
			_, err = o.validateVLESS(ctx, group, credentials)
		}
	}
	group.LastCheckedAt = o.config.Now().UTC()
	group.UpdatedAt = group.LastCheckedAt
	if err != nil || !checked.EgressOK {
		group.Status = domain.ProxyGroupDegraded
		group.LastErrorCode = codeOr(err, "egress_unavailable")
	} else {
		group.Status = domain.ProxyGroupReady
		group.LastErrorCode = ""
	}
	if saveErr := o.save(ctx, &group); saveErr != nil {
		return domain.ProxyGroup{}, saveErr
	}
	if err != nil {
		return group, operationError(err)
	}
	return group, nil
}

func (o *Orchestrator) Rotate(ctx context.Context, id string) (domain.ProxyGroup, error) {
	unlock := o.locks.lock(id)
	defer unlock()
	group, err := o.store.GetProxyGroup(ctx, id)
	if err != nil {
		return domain.ProxyGroup{}, operationError(err)
	}
	group.Status = domain.ProxyGroupRotating
	group.UpdatedAt = o.config.Now().UTC()
	if err = o.save(ctx, &group); err != nil {
		return domain.ProxyGroup{}, err
	}
	_, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return domain.ProxyGroup{}, err
	}
	slot, err := o.aimili.RotateSlot(ctx, group.AimiliSlot)
	if err == nil {
		slot, err = o.waitForSlot(ctx, group.AimiliSlot)
	}
	if err == nil && slot.EgressOK {
		group.ExitIP = slot.ExitIP
		_, err = o.validateSOCKS(ctx, group, credentials)
		if err == nil {
			_, err = o.validateVLESS(ctx, group, credentials)
		}
	}
	group.LastRotatedAt = o.config.Now().UTC()
	group.LastCheckedAt = group.LastRotatedAt
	group.UpdatedAt = group.LastRotatedAt
	if err != nil || !slot.EgressOK {
		group.Status = domain.ProxyGroupDegraded
		group.LastErrorCode = codeOr(err, "egress_unavailable")
	} else {
		group.Status = domain.ProxyGroupReady
		group.LastErrorCode = ""
	}
	if saveErr := o.save(ctx, &group); saveErr != nil {
		return domain.ProxyGroup{}, saveErr
	}
	if err != nil {
		return group, operationError(err)
	}
	return group, nil
}

func (o *Orchestrator) Disable(ctx context.Context, id string) error {
	unlock := o.locks.lock("all")
	defer unlock()
	group, err := o.store.GetProxyGroup(ctx, id)
	if err != nil {
		return operationError(err)
	}
	group.Status = domain.ProxyGroupDisabling
	group.UpdatedAt = o.config.Now().UTC()
	if err = o.save(ctx, &group); err != nil {
		return err
	}
	managed := managedFromGroup(group)
	xuiErr := o.xui.DeleteManagedGroup(ctx, managed)
	slotErr := o.aimili.DeleteSlot(ctx, group.AimiliSlot)
	if xuiErr != nil || slotErr != nil {
		group.Status = domain.ProxyGroupRepairRequired
		group.LastErrorCode = "disable_failed"
		group.UpdatedAt = o.config.Now().UTC()
		_ = o.save(ctx, &group)
		return &Error{Code: "repair_required"}
	}
	if err = o.store.DeleteProxyGroup(ctx, id); err != nil {
		return &Error{Code: "storage_failed"}
	}
	return nil
}

func (o *Orchestrator) Connections(ctx context.Context, id string) (Connections, error) {
	group, err := o.store.GetProxyGroup(ctx, id)
	if err != nil {
		return Connections{}, operationError(err)
	}
	if group.Status != domain.ProxyGroupReady {
		return Connections{}, &Error{Code: "not_ready"}
	}
	_, credentials, err := o.runtimeInputs(ctx)
	if err != nil {
		return Connections{}, err
	}
	vless := url.URL{Scheme: "vless", User: url.User(string(credentials.vlessID)), Host: net.JoinHostPort(o.config.PublicHost, fmt.Sprint(group.VLESSPort)), Fragment: group.ResourceName}
	query := vless.Query()
	query.Set("encryption", "none")
	query.Set("flow", "xtls-rprx-vision")
	query.Set("security", "reality")
	query.Set("sni", group.RealityServerName)
	query.Set("fp", "chrome")
	query.Set("pbk", group.RealityPublicKey)
	query.Set("sid", group.RealityShortID)
	query.Set("type", "tcp")
	vless.RawQuery = query.Encode()
	socks := url.URL{Scheme: "socks5h", User: url.UserPassword(string(credentials.mixedUsername), string(credentials.mixedPassword)), Host: net.JoinHostPort(o.config.PublicHost, fmt.Sprint(group.MixedPort))}
	return Connections{VLESSURI: vless.String(), SOCKS5HURI: socks.String()}, nil
}

func (o *Orchestrator) SetMixedCIDRs(ctx context.Context, prefixes []netip.Prefix) error {
	return o.store.ReplaceMixedCIDRs(ctx, prefixes)
}

type runtimeCredentials struct{ vlessID, mixedUsername, mixedPassword []byte }

func (o *Orchestrator) runtimeInputs(ctx context.Context) ([]netip.Prefix, runtimeCredentials, error) {
	cidrs, err := o.store.ListMixedCIDRs(ctx)
	if err != nil {
		return nil, runtimeCredentials{}, &Error{Code: "storage_failed"}
	}
	if len(cidrs) == 0 {
		return nil, runtimeCredentials{}, &Error{Code: "mixed_cidr_required"}
	}
	var result runtimeCredentials
	for _, item := range []struct {
		purpose string
		target  *[]byte
	}{{credentialVLESSClientID, &result.vlessID}, {credentialMixedUsername, &result.mixedUsername}, {credentialMixedPassword, &result.mixedPassword}} {
		value, getErr := o.store.GetCredential(ctx, item.purpose, o.masterKey)
		if getErr != nil {
			return nil, runtimeCredentials{}, &Error{Code: "credentials_not_configured"}
		}
		*item.target = value
	}
	return cidrs, result, nil
}
func (o *Orchestrator) allocatePorts(groups []domain.ProxyGroup) (int, int, bool) {
	usedV := map[int]bool{}
	usedM := map[int]bool{}
	for _, g := range groups {
		usedV[g.VLESSPort] = true
		usedM[g.MixedPort] = true
	}
	v, m := 0, 0
	for p := o.config.VLESSPortStart; p <= o.config.VLESSPortEnd; p++ {
		if !usedV[p] {
			v = p
			break
		}
	}
	for p := o.config.MixedPortStart; p <= o.config.MixedPortEnd; p++ {
		if !usedM[p] {
			m = p
			break
		}
	}
	return v, m, v > 0 && m > 0
}
func (o *Orchestrator) validateSOCKS(ctx context.Context, g domain.ProxyGroup, c runtimeCredentials) (validator.Result, error) {
	return o.validator.ValidateSOCKS5H(ctx, validator.SOCKSTarget{Address: net.JoinHostPort("127.0.0.1", fmt.Sprint(g.MixedPort)), Username: string(c.mixedUsername), Password: string(c.mixedPassword), ProbeHost: o.config.ProbeHost, ExpectedExitIP: g.ExitIP})
}
func (o *Orchestrator) validateVLESS(ctx context.Context, g domain.ProxyGroup, c runtimeCredentials) (validator.Result, error) {
	return o.validator.ValidateVLESS(ctx, validator.VLESSTarget{XrayPath: o.config.XrayPath, InboundAddress: net.JoinHostPort("127.0.0.1", fmt.Sprint(g.VLESSPort)), ClientID: string(c.vlessID), PublicKey: g.RealityPublicKey, ShortID: g.RealityShortID, ServerName: g.RealityServerName, ProbeHost: o.config.ProbeHost, ExpectedExitIP: g.ExitIP})
}

func (o *Orchestrator) waitForSlot(ctx context.Context, slot int) (aimili.SlotCheck, error) {
	waitContext, cancel := context.WithTimeout(ctx, o.config.ReadyTimeout)
	defer cancel()
	for {
		checked, err := o.aimili.CheckSlot(waitContext, slot)
		if err == nil && checked.EgressOK && net.ParseIP(checked.ExitIP) != nil {
			return checked, nil
		}
		if err != nil && waitContext.Err() == nil {
			return aimili.SlotCheck{}, err
		}
		timer := time.NewTimer(o.config.PollInterval)
		select {
		case <-waitContext.Done():
			timer.Stop()
			return aimili.SlotCheck{}, &Error{Code: "egress_unavailable"}
		case <-timer.C:
		}
	}
}
func (o *Orchestrator) save(ctx context.Context, g *domain.ProxyGroup) error {
	expected := g.Version
	if err := o.store.UpdateProxyGroup(ctx, *g, expected); err != nil {
		return operationError(err)
	}
	g.Version++
	return nil
}
func (o *Orchestrator) rollbackEnable(ctx context.Context, g *domain.ProxyGroup, managed xui.ManagedGroup, cause string) error {
	rollbackFailed := false
	if managed.ResourceName != "" {
		if err := o.xui.DeleteManagedGroup(ctx, managed); err != nil {
			rollbackFailed = true
		}
	}
	if err := o.aimili.DeleteSlot(ctx, g.AimiliSlot); err != nil {
		rollbackFailed = true
	}
	if !rollbackFailed {
		if err := o.store.DeleteProxyGroup(ctx, g.ID); err == nil {
			return &Error{Code: cause}
		}
	}
	g.Status = domain.ProxyGroupRepairRequired
	g.LastErrorCode = cause
	g.RecoveryState = "compensation_failed"
	g.UpdatedAt = o.config.Now().UTC()
	_ = o.save(ctx, g)
	return &Error{Code: "repair_required"}
}
func managedFromGroup(g domain.ProxyGroup) xui.ManagedGroup {
	return xui.ManagedGroup{ResourceName: g.ResourceName, VLESSInboundID: g.VLESSInboundID, MixedInboundID: g.MixedInboundID, VLESSInboundTag: g.ResourceName + "-vless", MixedInboundTag: g.ResourceName + "-mixed", OutboundTag: g.ResourceName + "-socks", Fingerprint: g.ConfigFingerprint, PublicKey: g.RealityPublicKey, ShortID: g.RealityShortID, ServerName: g.RealityServerName}
}
func prefixStrings(values []netip.Prefix) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.String()
	}
	return result
}
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var validationError *validator.Error
	if errors.As(err, &validationError) {
		return validationError.Code
	}
	var aimiliError *aimili.AdapterError
	if errors.As(err, &aimiliError) {
		return aimiliError.Code
	}
	var xuiError *xui.AdapterError
	if errors.As(err, &xuiError) {
		return xuiError.Code
	}
	if errors.Is(err, store.ErrProxyGroupExists) {
		return "already_exists"
	}
	if errors.Is(err, store.ErrProxyGroupNotFound) {
		return "not_found"
	}
	return "operation_failed"
}
func operationError(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: errorCode(err)}
}
func codeOr(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	code := errorCode(err)
	if code == "operation_failed" {
		return fallback
	}
	return code
}
