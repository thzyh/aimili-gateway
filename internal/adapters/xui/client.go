package xui

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thzyh/aimili-gateway/internal/securefile"
)

const (
	xuiTimeout       = 20 * time.Second
	xuiResponseLimit = 1 << 20
)

type Client struct {
	baseURL     *url.URL
	credentials Credentials
	httpClient  *http.Client
	csrf        string
	mu          sync.Mutex
}

func ReadCredentialsFile(path string) (Credentials, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Credentials{}, errors.New("open 3x-ui credentials file")
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<10 {
		return Credentials{}, errors.New("invalid 3x-ui credentials file")
	}
	if !securefile.RestrictedPermissions(path, info.Mode()) {
		return Credentials{}, errors.New("3x-ui credentials file permissions are too broad")
	}
	file, err := os.Open(path)
	if err != nil {
		return Credentials{}, errors.New("read 3x-ui credentials file")
	}
	defer file.Close()
	var credentials Credentials
	decoder := json.NewDecoder(io.LimitReader(file, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil {
		return Credentials{}, errors.New("decode 3x-ui credentials file")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Credentials{}, errors.New("decode 3x-ui credentials file")
	}
	if strings.TrimSpace(credentials.Username) == "" || credentials.Password == "" {
		return Credentials{}, errors.New("3x-ui credentials are incomplete")
	}
	return credentials, nil
}

func NewClient(baseURL string, credentials Credentials) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid 3x-ui base URL")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("3x-ui base URL must use loopback")
	}
	if strings.TrimSpace(credentials.Username) == "" || credentials.Password == "" {
		return nil, errors.New("3x-ui automation credentials are required")
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, errors.New("initialize 3x-ui session")
	}
	return &Client{
		baseURL:     parsed,
		credentials: credentials,
		httpClient: &http.Client{
			Timeout:       xuiTimeout,
			Jar:           jar,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *Client) ProbeCapabilities(ctx context.Context) (Capabilities, error) {
	if _, err := c.Snapshot(ctx); err != nil {
		return Capabilities{}, err
	}
	return Capabilities{
		WriteEnabled: true,
		Capabilities: []string{"inbounds.read", "inbounds.write", "xray.read", "xray.write", "x25519.generate"},
	}, nil
}

func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.authenticate(ctx); err != nil {
		return Snapshot{}, err
	}
	return c.snapshot(ctx)
}

func (c *Client) EnsureManagedGroup(ctx context.Context, desired DesiredGroup) (ManagedGroup, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateDesiredGroup(desired); err != nil {
		return ManagedGroup{}, err
	}
	if err := c.authenticate(ctx); err != nil {
		return ManagedGroup{}, err
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return ManagedGroup{}, err
	}
	vlessTag := desired.ResourceName + "-vless"
	mixedTag := desired.ResourceName + "-mixed"
	for _, inbound := range snapshot.Inbounds {
		if inbound.Tag == vlessTag || inbound.Tag == mixedTag {
			if !strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
				return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
			}
			return ManagedGroup{}, &AdapterError{Code: "managed_resource_exists"}
		}
		if inbound.Port == desired.VLESSPort || inbound.Port == desired.MixedPort {
			return ManagedGroup{}, &AdapterError{Code: "port_conflict"}
		}
	}

	privateKey, publicKey, err := c.newX25519(ctx)
	if err != nil {
		return ManagedGroup{}, err
	}
	shortIDBytes := make([]byte, 4)
	if _, err := rand.Read(shortIDBytes); err != nil {
		return ManagedGroup{}, &AdapterError{Code: "random_failed"}
	}
	shortID := hex.EncodeToString(shortIDBytes)

	originalSetting := cloneObject(snapshot.XraySetting)
	setting, err := mergeManagedXray(snapshot.XraySetting, desired, vlessTag, mixedTag)
	if err != nil {
		return ManagedGroup{}, err
	}
	if err := c.updateXray(ctx, setting, snapshot.OutboundTestURL); err != nil {
		return ManagedGroup{}, err
	}
	if err := c.addInbound(ctx, vlessInbound(desired, vlessTag, privateKey, publicKey, shortID)); err != nil {
		_ = c.updateXray(ctx, originalSetting, snapshot.OutboundTestURL)
		return ManagedGroup{}, err
	}
	if err := c.addInbound(ctx, mixedInbound(desired, mixedTag)); err != nil {
		if rollbackErr := c.rollbackEnsure(ctx, originalSetting, snapshot.OutboundTestURL, desired.ResourceName, vlessTag, mixedTag); rollbackErr != nil {
			return ManagedGroup{}, &AdapterError{Code: "partial_inbound_create"}
		}
		return ManagedGroup{}, &AdapterError{Code: "inbound_create_failed"}
	}
	updated, err := c.inbounds(ctx)
	if err != nil {
		return ManagedGroup{}, err
	}
	managed := ManagedGroup{
		ResourceName:    desired.ResourceName,
		VLESSInboundTag: vlessTag,
		MixedInboundTag: mixedTag,
		OutboundTag:     desired.ResourceName + "-socks",
		Fingerprint:     fingerprintDesired(desired),
		PublicKey:       publicKey,
		ShortID:         shortID,
		ServerName:      desired.RealityServerName,
	}
	for _, inbound := range updated {
		switch inbound.Tag {
		case vlessTag:
			managed.VLESSInboundID = inbound.ID
		case mixedTag:
			managed.MixedInboundID = inbound.ID
		}
	}
	if managed.VLESSInboundID == 0 || managed.MixedInboundID == 0 {
		return ManagedGroup{}, &AdapterError{Code: "write_verification_failed"}
	}
	return managed, nil
}

func (c *Client) UpdateManagedGroup(ctx context.Context, desired DesiredGroup, managed ManagedGroup) (ManagedGroup, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateDesiredGroup(desired); err != nil {
		return ManagedGroup{}, err
	}
	if managed.ResourceName != desired.ResourceName || managed.VLESSInboundID <= 0 || managed.MixedInboundID <= 0 ||
		managed.VLESSInboundTag != desired.ResourceName+"-vless" || managed.MixedInboundTag != desired.ResourceName+"-mixed" {
		return ManagedGroup{}, &AdapterError{Code: "invalid_request"}
	}
	if err := c.authenticate(ctx); err != nil {
		return ManagedGroup{}, err
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return ManagedGroup{}, err
	}
	managed, err = resolveManagedInboundIDs(snapshot.Inbounds, desired, managed)
	if err != nil {
		return ManagedGroup{}, err
	}
	effectiveDesired := desired
	effectiveDesired.ResourceName = managed.ResourceName
	publicKey, shortID, serverName, err := c.currentRealityMaterial(ctx, desired, managed)
	if err != nil {
		return ManagedGroup{}, err
	}
	if err := c.verifyCurrentMixedAccount(ctx, desired, managed); err != nil {
		return ManagedGroup{}, err
	}
	setting, err := mergeManagedXray(snapshot.XraySetting, effectiveDesired, managed.VLESSInboundTag, managed.MixedInboundTag)
	if err != nil {
		return ManagedGroup{}, err
	}
	if err := c.updateXray(ctx, setting, snapshot.OutboundTestURL); err != nil {
		return ManagedGroup{}, err
	}
	managed.Fingerprint = fingerprintDesired(effectiveDesired)
	managed.PublicKey = publicKey
	managed.ShortID = shortID
	managed.ServerName = serverName
	return managed, nil
}

func resolveManagedInboundIDs(inbounds []Inbound, desired DesiredGroup, managed ManagedGroup) (ManagedGroup, error) {
	var vless, mixed, portVLESS, portMixed *Inbound
	for index := range inbounds {
		inbound := &inbounds[index]
		if inbound.ID == managed.VLESSInboundID && inbound.Tag != managed.VLESSInboundTag {
			return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
		}
		if inbound.ID == managed.MixedInboundID && inbound.Tag != managed.MixedInboundTag {
			return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
		}
		switch inbound.Tag {
		case managed.VLESSInboundTag:
			if vless != nil || inbound.Protocol != "vless" || inbound.Port != desired.VLESSPort || !strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
				return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
			}
			vless = inbound
		case managed.MixedInboundTag:
			if mixed != nil || inbound.Protocol != "mixed" || inbound.Port != desired.MixedPort || !strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
				return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
			}
			mixed = inbound
		}
		if inbound.Protocol == "vless" && inbound.Port == desired.VLESSPort && strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
			if portVLESS != nil {
				return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
			}
			portVLESS = inbound
		}
		if inbound.Protocol == "mixed" && inbound.Port == desired.MixedPort && strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
			if portMixed != nil {
				return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
			}
			portMixed = inbound
		}
	}
	if vless == nil && mixed == nil {
		vless, mixed = portVLESS, portMixed
		if vless == nil || mixed == nil {
			return ManagedGroup{}, &AdapterError{Code: "managed_resource_missing"}
		}
		vlessStem := strings.TrimSuffix(vless.Tag, "-vless")
		mixedStem := strings.TrimSuffix(mixed.Tag, "-mixed")
		if vlessStem == vless.Tag || mixedStem == mixed.Tag || vlessStem != mixedStem || !strings.HasPrefix(vlessStem, "agw-") {
			return ManagedGroup{}, &AdapterError{Code: "ownership_conflict"}
		}
		managed.ResourceName = vlessStem
		managed.VLESSInboundTag = vless.Tag
		managed.MixedInboundTag = mixed.Tag
		managed.OutboundTag = vlessStem + "-socks"
	} else if vless == nil || mixed == nil {
		return ManagedGroup{}, &AdapterError{Code: "managed_resource_missing"}
	}
	managed.VLESSInboundID = vless.ID
	managed.MixedInboundID = mixed.ID
	return managed, nil
}

func (c *Client) verifyCurrentMixedAccount(ctx context.Context, desired DesiredGroup, managed ManagedGroup) error {
	details, err := c.inboundDetails(ctx)
	if err != nil {
		return err
	}
	for _, inbound := range details {
		if inbound.ID != managed.MixedInboundID {
			continue
		}
		if inbound.Tag != managed.MixedInboundTag || inbound.Protocol != "mixed" || inbound.Port != desired.MixedPort || !strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
			return &AdapterError{Code: "ownership_conflict"}
		}
		settings, ok := decodeObject(inbound.Settings)
		if !ok {
			return &AdapterError{Code: "invalid_response"}
		}
		accounts := asObjectSlice(settings["accounts"])
		if stringValue(settings["auth"]) != "password" || len(accounts) != 1 ||
			stringValue(accounts[0]["user"]) != desired.MixedUsername || stringValue(accounts[0]["pass"]) != desired.MixedPassword {
			return &AdapterError{Code: "managed_resource_drift"}
		}
		return nil
	}
	return &AdapterError{Code: "managed_resource_missing"}
}

type inboundDetail struct {
	ID             int64          `json:"id"`
	Tag            string         `json:"tag"`
	Remark         string         `json:"remark"`
	Protocol       string         `json:"protocol"`
	Port           int            `json:"port"`
	Settings       any            `json:"settings"`
	StreamSettings any            `json:"streamSettings"`
	Raw            map[string]any `json:"-"`
}

func (c *Client) currentRealityMaterial(ctx context.Context, desired DesiredGroup, managed ManagedGroup) (string, string, string, error) {
	details, err := c.inboundDetails(ctx)
	if err != nil {
		return "", "", "", err
	}
	for _, inbound := range details {
		if inbound.ID != managed.VLESSInboundID {
			continue
		}
		if inbound.Tag != managed.VLESSInboundTag || inbound.Protocol != "vless" || inbound.Port != desired.VLESSPort || !strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
			return "", "", "", &AdapterError{Code: "ownership_conflict"}
		}
		settings, ok := decodeObject(inbound.Settings)
		if !ok {
			return "", "", "", &AdapterError{Code: "invalid_response"}
		}
		clients := asObjectSlice(settings["clients"])
		if len(clients) != 1 || stringValue(clients[0]["id"]) != desired.VLESSClientID || stringValue(clients[0]["flow"]) != "xtls-rprx-vision" {
			return "", "", "", &AdapterError{Code: "managed_resource_drift"}
		}
		stream, ok := decodeObject(inbound.StreamSettings)
		if !ok || stringValue(stream["network"]) != "tcp" || stringValue(stream["security"]) != "reality" {
			return "", "", "", &AdapterError{Code: "managed_resource_drift"}
		}
		reality, ok := decodeObject(stream["realitySettings"])
		if !ok || stringValue(reality["target"]) != desired.RealityTarget {
			return "", "", "", &AdapterError{Code: "managed_resource_drift"}
		}
		serverNames := stringValues(reality["serverNames"])
		shortIDs := stringValues(reality["shortIds"])
		clientSettings, ok := decodeObject(reality["settings"])
		publicKey := stringValue(clientSettings["publicKey"])
		if !ok || len(serverNames) != 1 || serverNames[0] != desired.RealityServerName || len(shortIDs) != 1 ||
			!validRealityValue(publicKey, 256) || !validRealityValue(shortIDs[0], 64) {
			return "", "", "", &AdapterError{Code: "managed_resource_drift"}
		}
		return publicKey, shortIDs[0], serverNames[0], nil
	}
	return "", "", "", &AdapterError{Code: "managed_resource_missing"}
}

func (c *Client) inboundDetails(ctx context.Context) ([]inboundDetail, error) {
	obj, err := c.call(ctx, http.MethodGet, "panel/api/inbounds/list", nil, false)
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if json.Unmarshal(obj, &raw) != nil {
		return nil, &AdapterError{Code: "invalid_response"}
	}
	result := make([]inboundDetail, 0, len(raw))
	for _, item := range raw {
		encoded, encodeErr := json.Marshal(item)
		if encodeErr != nil {
			return nil, &AdapterError{Code: "invalid_response"}
		}
		var detail inboundDetail
		if json.Unmarshal(encoded, &detail) != nil {
			return nil, &AdapterError{Code: "invalid_response"}
		}
		detail.Raw = item
		result = append(result, detail)
	}
	return result, nil
}

func decodeObject(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, typed != nil
	case string:
		var result map[string]any
		if json.Unmarshal([]byte(typed), &result) != nil || result == nil {
			return nil, false
		}
		return result, true
	default:
		return nil, false
	}
}

func stringValues(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		result = append(result, text)
	}
	return result
}

func validRealityValue(value string, limit int) bool {
	return value != "" && len(value) <= limit && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func (c *Client) rollbackEnsure(ctx context.Context, original map[string]any, probeURL, resourceName string, tags ...string) error {
	tagSet := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tagSet[tag] = true
	}
	inbounds, err := c.inbounds(ctx)
	if err != nil {
		return err
	}
	for _, inbound := range inbounds {
		if tagSet[inbound.Tag] && strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
			if _, err := c.call(ctx, http.MethodPost, "panel/api/inbounds/del/"+formatInt64(inbound.ID), map[string]any{}, false); err != nil {
				return err
			}
		}
	}
	if err := c.deleteManagedClient(ctx, resourceName); err != nil {
		return err
	}
	return c.updateXray(ctx, original, probeURL)
}

func (c *Client) DeleteManagedGroup(ctx context.Context, managed ManagedGroup) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !strings.HasPrefix(managed.ResourceName, "agw-") || managed.VLESSInboundID <= 0 || managed.MixedInboundID <= 0 {
		return &AdapterError{Code: "invalid_request"}
	}
	if err := c.authenticate(ctx); err != nil {
		return err
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return err
	}
	wanted := map[int64]string{
		managed.VLESSInboundID: managed.VLESSInboundTag,
		managed.MixedInboundID: managed.MixedInboundTag,
	}
	found := make(map[int64]bool, 2)
	for _, inbound := range snapshot.Inbounds {
		tag, expected := wanted[inbound.ID]
		if !expected {
			continue
		}
		if inbound.Tag != tag || !strings.HasPrefix(inbound.Remark, "Aimili Gateway ") {
			return &AdapterError{Code: "ownership_conflict"}
		}
		found[inbound.ID] = true
	}
	if len(found) != 2 {
		return &AdapterError{Code: "managed_resource_missing"}
	}
	for _, id := range []int64{managed.MixedInboundID, managed.VLESSInboundID} {
		if _, err := c.call(ctx, http.MethodPost, "panel/api/inbounds/del/"+formatInt64(id), map[string]any{}, false); err != nil {
			return &AdapterError{Code: "partial_delete"}
		}
	}
	if err := c.deleteManagedClient(ctx, managed.ResourceName); err != nil {
		return &AdapterError{Code: "partial_delete"}
	}
	setting := snapshot.XraySetting
	outbounds := make([]any, 0)
	for _, outbound := range asObjectSlice(setting["outbounds"]) {
		tag := stringValue(outbound["tag"])
		if tag != managed.OutboundTag && tag != "agw-blackhole" {
			outbounds = append(outbounds, outbound)
		}
	}
	setting["outbounds"] = outbounds
	routing, _ := setting["routing"].(map[string]any)
	if routing != nil {
		rules := make([]any, 0)
		for _, rule := range asObjectSlice(routing["rules"]) {
			if stringValue(rule["outboundTag"]) == managed.OutboundTag ||
				ruleContainsInbound(rule, managed.VLESSInboundTag) ||
				ruleContainsInbound(rule, managed.MixedInboundTag) {
				continue
			}
			rules = append(rules, rule)
		}
		routing["rules"] = rules
	}
	if err := c.updateXray(ctx, setting, snapshot.OutboundTestURL); err != nil {
		return &AdapterError{Code: "partial_delete"}
	}
	return nil
}

func (c *Client) authenticate(ctx context.Context) error {
	csrf, err := c.fetchCSRF(ctx)
	if err != nil {
		return err
	}
	c.csrf = csrf
	payload := map[string]string{
		"username":      c.credentials.Username,
		"password":      c.credentials.Password,
		"twoFactorCode": c.credentials.TwoFactorCode,
	}
	if _, err := c.call(ctx, http.MethodPost, "login", payload, false); err != nil {
		var adapterError *AdapterError
		if errors.As(err, &adapterError) && adapterError.Code == "upstream_rejected" {
			return &AdapterError{Code: "credentials_rejected"}
		}
		return err
	}
	csrf, err = c.fetchCSRF(ctx)
	if err != nil {
		return err
	}
	c.csrf = csrf
	return nil
}

func (c *Client) fetchCSRF(ctx context.Context) (string, error) {
	obj, err := c.call(ctx, http.MethodGet, "csrf-token", nil, false)
	if err != nil {
		return "", err
	}
	var token string
	if err := json.Unmarshal(obj, &token); err != nil || token == "" {
		return "", &AdapterError{Code: "invalid_response"}
	}
	return token, nil
}

func (c *Client) snapshot(ctx context.Context) (Snapshot, error) {
	obj, err := c.call(ctx, http.MethodPost, "panel/api/xray/", map[string]any{}, false)
	if err != nil {
		return Snapshot{}, err
	}
	envelopeRaw := obj
	if len(envelopeRaw) > 0 && envelopeRaw[0] == '"' {
		var encoded string
		if json.Unmarshal(envelopeRaw, &encoded) != nil {
			return Snapshot{}, &AdapterError{Code: "invalid_response"}
		}
		envelopeRaw = []byte(encoded)
	}
	var xrayEnvelope struct {
		XraySetting     json.RawMessage `json:"xraySetting"`
		OutboundTestURL string          `json:"outboundTestUrl"`
	}
	if err := json.Unmarshal(envelopeRaw, &xrayEnvelope); err != nil {
		return Snapshot{}, &AdapterError{Code: "invalid_response"}
	}
	settingRaw := xrayEnvelope.XraySetting
	if len(settingRaw) > 0 && settingRaw[0] == '"' {
		var encoded string
		if json.Unmarshal(settingRaw, &encoded) != nil {
			return Snapshot{}, &AdapterError{Code: "invalid_response"}
		}
		settingRaw = []byte(encoded)
	}
	var setting map[string]any
	if json.Unmarshal(settingRaw, &setting) != nil || setting == nil {
		return Snapshot{}, &AdapterError{Code: "invalid_response"}
	}
	inbounds, err := c.inbounds(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	outbounds := make([]Outbound, 0)
	for _, raw := range asObjectSlice(setting["outbounds"]) {
		outbounds = append(outbounds, Outbound{Tag: stringValue(raw["tag"]), Protocol: stringValue(raw["protocol"])})
	}
	return Snapshot{Inbounds: inbounds, Outbounds: outbounds, XraySetting: setting, OutboundTestURL: xrayEnvelope.OutboundTestURL}, nil
}

func (c *Client) inbounds(ctx context.Context) ([]Inbound, error) {
	details, err := c.inboundDetails(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Inbound, 0, len(details))
	for _, inbound := range details {
		result = append(result, Inbound{ID: inbound.ID, Tag: inbound.Tag, Remark: inbound.Remark, Protocol: inbound.Protocol, Port: inbound.Port})
	}
	return result, nil
}

func (c *Client) newX25519(ctx context.Context) (string, string, error) {
	obj, err := c.call(ctx, http.MethodGet, "panel/api/server/getNewX25519Cert", nil, false)
	if err != nil {
		return "", "", err
	}
	var keys struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
	}
	if json.Unmarshal(obj, &keys) != nil || keys.PrivateKey == "" || keys.PublicKey == "" {
		return "", "", &AdapterError{Code: "invalid_response"}
	}
	return keys.PrivateKey, keys.PublicKey, nil
}

func (c *Client) updateXray(ctx context.Context, setting map[string]any, probeURL string) error {
	encoded, err := json.Marshal(setting)
	if err != nil {
		return &AdapterError{Code: "invalid_request"}
	}
	if probeURL == "" {
		probeURL = "https://www.google.com/generate_204"
	}
	_, err = c.call(ctx, http.MethodPost, "panel/api/xray/update", url.Values{
		"xraySetting":     []string{string(encoded)},
		"outboundTestUrl": []string{probeURL},
	}, true)
	return err
}

func (c *Client) addInbound(ctx context.Context, inbound map[string]any) error {
	_, err := c.call(ctx, http.MethodPost, "panel/api/inbounds/add", inbound, false)
	return err
}

func (c *Client) updateInbound(ctx context.Context, id int64, inbound map[string]any) error {
	if id <= 0 || inbound == nil {
		return &AdapterError{Code: "invalid_request"}
	}
	_, err := c.call(ctx, http.MethodPost, "panel/api/inbounds/update/"+strconv.FormatInt(id, 10), inbound, false)
	return err
}

func (c *Client) call(ctx context.Context, method, path string, payload any, form bool) (json.RawMessage, error) {
	endpoint, err := url.JoinPath(c.baseURL.String(), path)
	if err != nil {
		return nil, &AdapterError{Code: "invalid_configuration"}
	}
	var body io.Reader
	if payload != nil {
		if form {
			body = strings.NewReader(payload.(url.Values).Encode())
		} else {
			encoded, encodeErr := json.Marshal(payload)
			if encodeErr != nil {
				return nil, &AdapterError{Code: "invalid_request"}
			}
			body = bytes.NewReader(encoded)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, &AdapterError{Code: "invalid_request"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	if payload != nil {
		if form {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			request.Header.Set("Content-Type", "application/json")
		}
		if c.csrf != "" {
			request.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &AdapterError{Code: "timeout"}
		}
		return nil, &AdapterError{Code: "connection_failed"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, xuiResponseLimit+1))
	if err != nil || len(raw) > xuiResponseLimit {
		return nil, &AdapterError{Code: "invalid_response"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return nil, &AdapterError{Code: "unsafe_redirect"}
		}
		return nil, &AdapterError{Code: "upstream_rejected"}
	}
	var envelope struct {
		Success bool            `json:"success"`
		Object  json.RawMessage `json:"obj"`
		Message string          `json:"msg"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Object == nil {
		return nil, &AdapterError{Code: "invalid_response"}
	}
	if !envelope.Success {
		message := strings.ToLower(envelope.Message)
		if strings.Contains(message, "two factor") || strings.Contains(message, "two-factor") || strings.Contains(message, "2fa") || strings.Contains(message, "totp") {
			return nil, &AdapterError{Code: "totp_incompatible"}
		}
		return nil, &AdapterError{Code: "upstream_rejected"}
	}
	return envelope.Object, nil
}

func validateDesiredGroup(desired DesiredGroup) error {
	if !strings.HasPrefix(desired.ResourceName, "agw-") || desired.SOCKSPort < 1 || desired.VLESSPort < 1 || desired.MixedPort < 1 ||
		desired.VLESSClientID == "" || desired.MixedUsername == "" || desired.MixedPassword == "" ||
		(desired.MixedSourceRestrictionEnabled && len(desired.MixedSourceCIDRs) == 0) ||
		desired.RealityTarget != "127.0.0.1:443" || strings.TrimSpace(desired.RealityServerName) == "" ||
		net.ParseIP(desired.RealityServerName) != nil || strings.ContainsAny(desired.RealityServerName, "/:") {
		return &AdapterError{Code: "invalid_request"}
	}
	for _, raw := range desired.MixedSourceCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return &AdapterError{Code: "invalid_request"}
		}
	}
	return nil
}

func mergeManagedXray(setting map[string]any, desired DesiredGroup, vlessTag, mixedTag string) (map[string]any, error) {
	outboundTag := desired.ResourceName + "-socks"
	outbounds := asObjectSlice(setting["outbounds"])
	keptOutbounds := make([]any, 0, len(outbounds)+2)
	for _, outbound := range outbounds {
		tag := stringValue(outbound["tag"])
		if tag == outboundTag {
			if !managedSocksOutboundMatches(outbound, desired.SOCKSPort) {
				return nil, &AdapterError{Code: "ownership_conflict"}
			}
			continue
		}
		if tag == "agw-blackhole" {
			if stringValue(outbound["protocol"]) != "blackhole" {
				return nil, &AdapterError{Code: "ownership_conflict"}
			}
			continue
		}
		keptOutbounds = append(keptOutbounds, outbound)
	}
	keptOutbounds = append(keptOutbounds,
		map[string]any{"tag": outboundTag, "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": desired.SOCKSPort}}}},
		map[string]any{"tag": "agw-blackhole", "protocol": "blackhole", "settings": map[string]any{}},
	)
	setting["outbounds"] = keptOutbounds

	routing, _ := setting["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{"domainStrategy": "AsIs"}
		setting["routing"] = routing
	}
	keptRules := make([]any, 0)
	for _, rule := range asObjectSlice(routing["rules"]) {
		outbound := stringValue(rule["outboundTag"])
		if outbound == outboundTag || ruleContainsInbound(rule, vlessTag) || ruleContainsInbound(rule, mixedTag) {
			continue
		}
		keptRules = append(keptRules, rule)
	}
	managedRules := []any{map[string]any{"type": "field", "inboundTag": []any{vlessTag}, "outboundTag": outboundTag}}
	if mixedSourceRestrictionEnabled(desired) {
		allowedSources := append(append([]string{}, desired.MixedSourceCIDRs...), "127.0.0.1/32", "::1/128")
		managedRules = append(managedRules,
			map[string]any{"type": "field", "inboundTag": []any{mixedTag}, "source": stringsToAny(allowedSources), "outboundTag": outboundTag},
			map[string]any{"type": "field", "inboundTag": []any{mixedTag}, "outboundTag": "agw-blackhole"},
		)
	} else {
		managedRules = append(managedRules, map[string]any{"type": "field", "inboundTag": []any{mixedTag}, "outboundTag": outboundTag})
	}
	routing["rules"] = append(managedRules, keptRules...)
	return setting, nil
}

func mergeAggregateXray(setting map[string]any, desired AggregateDesired) (map[string]any, error) {
	if !strings.HasPrefix(desired.ResourceName, "agw-") || desired.VLESSPort < 1 || desired.VLESSPort > 65535 || len(desired.OutboundTags) == 0 {
		return nil, &AdapterError{Code: "invalid_request"}
	}
	seen := map[string]bool{}
	selectors := make([]any, 0, len(desired.OutboundTags))
	for _, tag := range desired.OutboundTags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		selectors = append(selectors, tag)
	}
	if len(selectors) == 0 {
		return nil, &AdapterError{Code: "invalid_request"}
	}
	routing, _ := setting["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{"domainStrategy": "AsIs"}
		setting["routing"] = routing
	}
	balancers := make([]any, 0)
	for _, item := range asObjectSlice(routing["balancers"]) {
		if stringValue(item["tag"]) != "agw-aggregate" {
			balancers = append(balancers, item)
		}
	}
	balancers = append(balancers, map[string]any{"tag": "agw-aggregate", "selector": selectors})
	balancers[len(balancers)-1].(map[string]any)["strategy"] = map[string]any{"type": "leastPing"}
	routing["balancers"] = balancers
	setting["observatory"] = map[string]any{"subjectSelector": selectors, "probeURL": "https://www.google.com/generate_204", "probeInterval": "30s", "enableConcurrency": true}
	rules := make([]any, 0)
	aggregateTag := desired.ResourceName + "-vless"
	for _, rule := range asObjectSlice(routing["rules"]) {
		if stringValue(rule["balancerTag"]) == "agw-aggregate" || ruleContainsInbound(rule, aggregateTag) {
			continue
		}
		rules = append(rules, rule)
	}
	routing["rules"] = append([]any{map[string]any{"type": "field", "inboundTag": []any{aggregateTag}, "balancerTag": "agw-aggregate"}}, rules...)
	return setting, nil
}

func aggregateVLESSInbound(desired AggregateDesired, tag, privateKey, publicKey, shortID string) map[string]any {
	return map[string]any{
		"up": 0, "down": 0, "total": 0, "remark": "Aimili Gateway aggregate VLESS", "enable": true,
		"expiryTime": 0, "trafficReset": "never", "trafficResetDay": 1, "listen": "", "port": desired.VLESSPort,
		"protocol": "vless", "tag": tag,
		"settings":       mustJSONString(map[string]any{"clients": []any{map[string]any{"id": desired.VLESSClientID, "email": "aimili-gateway-aggregate", "flow": "xtls-rprx-vision", "enable": true}}, "decryption": "none"}),
		"streamSettings": mustJSONString(map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"show": false, "xver": 0, "target": desired.RealityTarget, "serverNames": []any{desired.RealityServerName}, "privateKey": privateKey, "shortIds": []any{shortID}, "settings": map[string]any{"publicKey": publicKey, "fingerprint": "chrome", "spiderX": "/"}}}),
		"sniffing":       mustJSONString(map[string]any{"enabled": true, "destOverride": []any{"http", "tls", "quic"}, "metadataOnly": false, "routeOnly": false}),
	}
}

func validateAggregateDesired(desired AggregateDesired) error {
	if !strings.HasPrefix(desired.ResourceName, "agw-") || desired.VLESSPort < 1 || desired.VLESSPort > 65535 || strings.TrimSpace(desired.VLESSClientID) == "" || desired.RealityTarget != "127.0.0.1:443" || strings.TrimSpace(desired.RealityServerName) == "" || net.ParseIP(desired.RealityServerName) != nil || strings.ContainsAny(desired.RealityServerName, "/:") {
		return &AdapterError{Code: "invalid_request"}
	}
	return nil
}

func (c *Client) EnsureAggregate(ctx context.Context, desired AggregateDesired) (ManagedAggregate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateAggregateDesired(desired); err != nil {
		return ManagedAggregate{}, err
	}
	if err := c.authenticate(ctx); err != nil {
		return ManagedAggregate{}, err
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return ManagedAggregate{}, err
	}
	tag := desired.ResourceName + "-vless"
	var current *Inbound
	for i := range snapshot.Inbounds {
		if snapshot.Inbounds[i].Tag == tag {
			current = &snapshot.Inbounds[i]
			break
		}
	}
	publicKey, shortID := "", ""
	if current != nil {
		if current.Protocol != "vless" || current.Port != desired.VLESSPort || !strings.HasPrefix(current.Remark, "Aimili Gateway") {
			return ManagedAggregate{}, &AdapterError{Code: "ownership_conflict"}
		}
		details, detailErr := c.inboundDetails(ctx)
		if detailErr != nil {
			return ManagedAggregate{}, detailErr
		}
		for _, inbound := range details {
			if inbound.ID != current.ID {
				continue
			}
			stream, ok := decodeObject(inbound.StreamSettings)
			if !ok {
				return ManagedAggregate{}, &AdapterError{Code: "invalid_response"}
			}
			reality, ok := decodeObject(stream["realitySettings"])
			if !ok {
				return ManagedAggregate{}, &AdapterError{Code: "invalid_response"}
			}
			names := stringValues(reality["serverNames"])
			ids := stringValues(reality["shortIds"])
			settings, ok := decodeObject(reality["settings"])
			if !ok || len(names) != 1 || len(ids) != 1 || names[0] != desired.RealityServerName {
				return ManagedAggregate{}, &AdapterError{Code: "managed_resource_drift"}
			}
			publicKey, shortID = stringValue(settings["publicKey"]), ids[0]
		}
	} else {
		privateKey, generatedPublicKey, keyErr := c.newX25519(ctx)
		if keyErr != nil {
			return ManagedAggregate{}, keyErr
		}
		publicKey = generatedPublicKey
		bytesID := make([]byte, 4)
		if _, keyErr = rand.Read(bytesID); keyErr != nil {
			return ManagedAggregate{}, &AdapterError{Code: "random_failed"}
		}
		shortID = hex.EncodeToString(bytesID)
		setting, mergeErr := mergeAggregateXray(cloneObject(snapshot.XraySetting), desired)
		if mergeErr != nil {
			return ManagedAggregate{}, mergeErr
		}
		if updateErr := c.updateXray(ctx, setting, snapshot.OutboundTestURL); updateErr != nil {
			return ManagedAggregate{}, updateErr
		}
		if addErr := c.addInbound(ctx, aggregateVLESSInbound(desired, tag, privateKey, publicKey, shortID)); addErr != nil {
			_ = c.updateXray(ctx, snapshot.XraySetting, snapshot.OutboundTestURL)
			return ManagedAggregate{}, addErr
		}
		updated, listErr := c.inbounds(ctx)
		if listErr != nil {
			return ManagedAggregate{}, listErr
		}
		for _, inbound := range updated {
			if inbound.Tag == tag {
				current = &inbound
				break
			}
		}
	}
	setting, mergeErr := mergeAggregateXray(cloneObject(snapshot.XraySetting), desired)
	if mergeErr != nil {
		return ManagedAggregate{}, mergeErr
	}
	if updateErr := c.updateXray(ctx, setting, snapshot.OutboundTestURL); updateErr != nil {
		return ManagedAggregate{}, updateErr
	}
	if current == nil {
		return ManagedAggregate{}, &AdapterError{Code: "write_verification_failed"}
	}
	return ManagedAggregate{ResourceName: desired.ResourceName, VLESSInboundID: current.ID, VLESSInboundTag: tag, VLESSPort: desired.VLESSPort, PublicKey: publicKey, ShortID: shortID, ServerName: desired.RealityServerName}, nil
}

func mixedSourceRestrictionEnabled(desired DesiredGroup) bool {
	return desired.MixedSourceRestrictionEnabled || len(desired.MixedSourceCIDRs) > 0
}

func managedSocksOutboundMatches(outbound map[string]any, port int) bool {
	if stringValue(outbound["protocol"]) != "socks" {
		return false
	}
	settings, _ := outbound["settings"].(map[string]any)
	servers := asObjectSlice(settings["servers"])
	if len(servers) != 1 || stringValue(servers[0]["address"]) != "127.0.0.1" {
		return false
	}
	value, ok := servers[0]["port"].(float64)
	if !ok {
		if integer, integerOK := servers[0]["port"].(int); integerOK {
			return integer == port
		}
		return false
	}
	return int(value) == port
}

func verifyLegacyMainChain(inbounds []Inbound, setting map[string]any, vlessPort, socksPort int) error {
	foundInbound := false
	for _, inbound := range inbounds {
		if inbound.Tag == "aimili-reality" {
			if inbound.Protocol != "vless" || inbound.Port != vlessPort || inbound.Remark != "Aimili Reality" {
				return &AdapterError{Code: "ownership_conflict"}
			}
			foundInbound = true
		}
	}
	if !foundInbound {
		return &AdapterError{Code: "managed_resource_missing"}
	}
	foundOutbound := false
	for _, outbound := range asObjectSlice(setting["outbounds"]) {
		if stringValue(outbound["tag"]) == "aimili-socks" {
			if !managedSocksOutboundMatches(outbound, socksPort) {
				return &AdapterError{Code: "ownership_conflict"}
			}
			foundOutbound = true
		}
	}
	if !foundOutbound {
		return &AdapterError{Code: "managed_resource_missing"}
	}
	routing, _ := setting["routing"].(map[string]any)
	for _, rule := range asObjectSlice(routing["rules"]) {
		if stringValue(rule["outboundTag"]) == "aimili-socks" && ruleContainsInbound(rule, "aimili-reality") {
			return nil
		}
	}
	return &AdapterError{Code: "managed_resource_missing"}
}

func (c *Client) EnsureLegacyMain(ctx context.Context, desired LegacyMainDesired) (LegacyMain, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if desired.VLESSPort != 8443 || desired.SOCKSPort != 7928 || desired.MixedPort < 1 || desired.MixedPort > 65535 || desired.MixedUsername == "" || desired.MixedPassword == "" ||
		desired.RealityTarget != "127.0.0.1:443" || strings.TrimSpace(desired.RealityServerName) == "" || net.ParseIP(desired.RealityServerName) != nil || strings.ContainsAny(desired.RealityServerName, "/:") ||
		(desired.MixedSourceRestrictionEnabled && len(desired.MixedSourceCIDRs) == 0) {
		return LegacyMain{}, &AdapterError{Code: "invalid_request"}
	}
	if err := c.authenticate(ctx); err != nil {
		return LegacyMain{}, err
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return LegacyMain{}, err
	}
	if err := verifyLegacyMainChain(snapshot.Inbounds, snapshot.XraySetting, desired.VLESSPort, desired.SOCKSPort); err != nil {
		return LegacyMain{}, err
	}
	originalSetting := cloneObject(snapshot.XraySetting)
	result := LegacyMain{VLESSPort: desired.VLESSPort, MixedPort: desired.MixedPort, OutboundTag: "aimili-socks"}
	var legacyOriginal, legacyMigrated map[string]any
	var legacyPrivateKey string
	details, err := c.inboundDetails(ctx)
	if err != nil {
		return LegacyMain{}, err
	}
	for _, inbound := range details {
		if inbound.Tag != "aimili-reality" {
			continue
		}
		result.VLESSInboundID = inbound.ID
		settings, ok := decodeObject(inbound.Settings)
		if !ok {
			return LegacyMain{}, &AdapterError{Code: "invalid_response"}
		}
		clients := asObjectSlice(settings["clients"])
		if len(clients) != 1 || stringValue(clients[0]["flow"]) != "xtls-rprx-vision" {
			return LegacyMain{}, &AdapterError{Code: "managed_resource_drift"}
		}
		result.ClientID = stringValue(clients[0]["id"])
		stream, ok := decodeObject(inbound.StreamSettings)
		if !ok || stringValue(stream["network"]) != "tcp" || stringValue(stream["security"]) != "reality" {
			return LegacyMain{}, &AdapterError{Code: "managed_resource_drift"}
		}
		reality, ok := decodeObject(stream["realitySettings"])
		if !ok {
			return LegacyMain{}, &AdapterError{Code: "managed_resource_drift"}
		}
		names, ids := stringValues(reality["serverNames"]), stringValues(reality["shortIds"])
		clientSettings, ok := decodeObject(reality["settings"])
		privateKey := stringValue(reality["privateKey"])
		if !ok || len(names) != 1 || len(ids) != 1 || !validRealityValue(privateKey, 256) ||
			!validRealityValue(stringValue(clientSettings["publicKey"]), 256) || !validRealityValue(ids[0], 64) {
			return LegacyMain{}, &AdapterError{Code: "managed_resource_drift"}
		}
		result.PublicKey, result.ShortID, result.ServerName = stringValue(clientSettings["publicKey"]), ids[0], names[0]
		legacyPrivateKey = privateKey
		currentTarget := stringValue(reality["target"])
		switch {
		case currentTarget == desired.RealityTarget && names[0] == desired.RealityServerName:
		case currentTarget == "www.microsoft.com:443" && names[0] == "www.microsoft.com":
			legacyOriginal = cloneObject(inbound.Raw)
			legacyMigrated = cloneObject(inbound.Raw)
			migratedStream := cloneObject(stream)
			migratedReality := cloneObject(reality)
			migratedReality["target"] = desired.RealityTarget
			migratedReality["serverNames"] = []any{desired.RealityServerName}
			migratedStream["realitySettings"] = migratedReality
			legacyMigrated["streamSettings"] = mustJSONString(migratedStream)
		default:
			return LegacyMain{}, &AdapterError{Code: "managed_resource_drift"}
		}
	}
	if result.VLESSInboundID == 0 || result.ClientID == "" || result.PublicKey == "" || result.ShortID == "" || result.ServerName == "" {
		return LegacyMain{}, &AdapterError{Code: "managed_resource_drift"}
	}
	mixedTag := "agw-main-mixed"
	for _, inbound := range snapshot.Inbounds {
		if inbound.Tag == mixedTag {
			if inbound.Protocol != "mixed" || inbound.Port != desired.MixedPort || !strings.HasPrefix(inbound.Remark, "Aimili Gateway") {
				return LegacyMain{}, &AdapterError{Code: "ownership_conflict"}
			}
			result.MixedInboundID = inbound.ID
		} else if inbound.Port == desired.MixedPort {
			return LegacyMain{}, &AdapterError{Code: "port_conflict"}
		}
	}
	if result.MixedInboundID != 0 {
		for _, inbound := range details {
			if inbound.ID != result.MixedInboundID {
				continue
			}
			settings, ok := decodeObject(inbound.Settings)
			if !ok {
				return LegacyMain{}, &AdapterError{Code: "invalid_response"}
			}
			accounts := asObjectSlice(settings["accounts"])
			if stringValue(settings["auth"]) != "password" || len(accounts) != 1 || stringValue(accounts[0]["user"]) != desired.MixedUsername || stringValue(accounts[0]["pass"]) != desired.MixedPassword {
				return LegacyMain{}, &AdapterError{Code: "managed_resource_drift"}
			}
		}
	}
	if desired.MixedSourceRestrictionEnabled {
		outbounds := make([]any, 0)
		foundBlackhole := false
		for _, outbound := range asObjectSlice(snapshot.XraySetting["outbounds"]) {
			if stringValue(outbound["tag"]) == "agw-blackhole" {
				if stringValue(outbound["protocol"]) != "blackhole" {
					return LegacyMain{}, &AdapterError{Code: "ownership_conflict"}
				}
				foundBlackhole = true
			}
			outbounds = append(outbounds, outbound)
		}
		if !foundBlackhole {
			outbounds = append(outbounds, map[string]any{"tag": "agw-blackhole", "protocol": "blackhole", "settings": map[string]any{}})
		}
		snapshot.XraySetting["outbounds"] = outbounds
	}
	routing, _ := snapshot.XraySetting["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{"domainStrategy": "AsIs"}
		snapshot.XraySetting["routing"] = routing
	}
	kept := make([]any, 0)
	for _, rule := range asObjectSlice(routing["rules"]) {
		if !ruleContainsInbound(rule, mixedTag) {
			kept = append(kept, rule)
		}
	}
	if desired.MixedSourceRestrictionEnabled {
		allowed := append(append([]string{}, desired.MixedSourceCIDRs...), "127.0.0.1/32", "::1/128")
		kept = append([]any{map[string]any{"type": "field", "inboundTag": []any{mixedTag}, "source": stringsToAny(allowed), "outboundTag": "aimili-socks"}, map[string]any{"type": "field", "inboundTag": []any{mixedTag}, "outboundTag": "agw-blackhole"}}, kept...)
	} else {
		kept = append([]any{map[string]any{"type": "field", "inboundTag": []any{mixedTag}, "outboundTag": "aimili-socks"}}, kept...)
	}
	routing["rules"] = kept
	if err := c.updateXray(ctx, snapshot.XraySetting, snapshot.OutboundTestURL); err != nil {
		return LegacyMain{}, err
	}
	if result.MixedInboundID == 0 {
		groupDesired := DesiredGroup{ResourceName: "agw-main", MixedPort: desired.MixedPort, MixedUsername: desired.MixedUsername, MixedPassword: desired.MixedPassword}
		if err := c.addInbound(ctx, mixedInbound(groupDesired, mixedTag)); err != nil {
			_ = c.updateXray(ctx, originalSetting, snapshot.OutboundTestURL)
			return LegacyMain{}, err
		}
		updated, listErr := c.inbounds(ctx)
		if listErr != nil {
			return LegacyMain{}, listErr
		}
		for _, inbound := range updated {
			if inbound.Tag == mixedTag {
				result.MixedInboundID = inbound.ID
			}
		}
	}
	if result.MixedInboundID == 0 {
		return LegacyMain{}, &AdapterError{Code: "write_verification_failed"}
	}
	if legacyMigrated != nil {
		if err := c.updateInbound(ctx, result.VLESSInboundID, legacyMigrated); err != nil {
			return LegacyMain{}, err
		}
		verified := false
		updatedDetails, verifyErr := c.inboundDetails(ctx)
		if verifyErr == nil {
			for _, inbound := range updatedDetails {
				if inbound.ID != result.VLESSInboundID {
					continue
				}
				settings, settingsOK := decodeObject(inbound.Settings)
				stream, streamOK := decodeObject(inbound.StreamSettings)
				reality, realityOK := decodeObject(stream["realitySettings"])
				clientSettings, clientSettingsOK := decodeObject(reality["settings"])
				clients := asObjectSlice(settings["clients"])
				names, ids := stringValues(reality["serverNames"]), stringValues(reality["shortIds"])
				verified = settingsOK && streamOK && realityOK && clientSettingsOK && len(clients) == 1 &&
					stringValue(clients[0]["id"]) == result.ClientID && stringValue(clients[0]["flow"]) == "xtls-rprx-vision" &&
					stringValue(reality["target"]) == desired.RealityTarget && len(names) == 1 && names[0] == desired.RealityServerName &&
					stringValue(reality["privateKey"]) == legacyPrivateKey && len(ids) == 1 && ids[0] == result.ShortID &&
					stringValue(clientSettings["publicKey"]) == result.PublicKey
				break
			}
		}
		if !verified {
			if rollbackErr := c.updateInbound(ctx, result.VLESSInboundID, legacyOriginal); rollbackErr != nil {
				return LegacyMain{}, &AdapterError{Code: "partial_inbound_update"}
			}
			return LegacyMain{}, &AdapterError{Code: "write_verification_failed"}
		}
		result.ServerName = desired.RealityServerName
	}
	return result, nil
}

func vlessInbound(desired DesiredGroup, tag, privateKey, publicKey, shortID string) map[string]any {
	return map[string]any{
		"up": 0, "down": 0, "total": 0, "remark": "Aimili Gateway " + desired.ResourceName + " VLESS",
		"enable": true, "expiryTime": 0, "trafficReset": "never", "trafficResetDay": 1,
		"listen": "", "port": desired.VLESSPort, "protocol": "vless", "tag": tag,
		"settings":       mustJSONString(map[string]any{"clients": []any{map[string]any{"id": desired.VLESSClientID, "email": managedClientEmail(desired.ResourceName), "flow": "xtls-rprx-vision", "enable": true}}, "decryption": "none"}),
		"streamSettings": mustJSONString(map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"show": false, "xver": 0, "target": desired.RealityTarget, "serverNames": []any{desired.RealityServerName}, "privateKey": privateKey, "shortIds": []any{shortID}, "settings": map[string]any{"publicKey": publicKey, "fingerprint": "chrome", "spiderX": "/"}}}),
		"sniffing":       mustJSONString(map[string]any{"enabled": true, "destOverride": []any{"http", "tls", "quic"}, "metadataOnly": false, "routeOnly": false}),
	}
}

func managedClientEmail(resourceName string) string {
	return "aimili-gateway-" + strings.TrimPrefix(resourceName, "agw-")
}

func (c *Client) deleteManagedClient(ctx context.Context, resourceName string) error {
	if !strings.HasPrefix(resourceName, "agw-") {
		return &AdapterError{Code: "invalid_request"}
	}
	_, err := c.call(ctx, http.MethodPost, "panel/api/clients/del/"+url.PathEscape(managedClientEmail(resourceName)), map[string]any{}, false)
	return err
}

func mixedInbound(desired DesiredGroup, tag string) map[string]any {
	return map[string]any{
		"up": 0, "down": 0, "total": 0, "remark": "Aimili Gateway " + desired.ResourceName + " mixed",
		"enable": true, "expiryTime": 0, "trafficReset": "never", "trafficResetDay": 1,
		"listen": "", "port": desired.MixedPort, "protocol": "mixed", "tag": tag,
		"settings":       mustJSONString(map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": desired.MixedUsername, "pass": desired.MixedPassword}}, "udp": true}),
		"streamSettings": "{}", "sniffing": mustJSONString(map[string]any{"enabled": true, "destOverride": []any{"http", "tls"}, "routeOnly": false}),
	}
}

func fingerprintDesired(desired DesiredGroup) string {
	encoded, _ := json.Marshal(desired)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func mustJSONString(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func cloneObject(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func asObjectSlice(value any) []map[string]any {
	raw, _ := value.([]any)
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func ruleContainsInbound(rule map[string]any, tag string) bool {
	values, _ := rule["inboundTag"].([]any)
	for _, value := range values {
		if stringValue(value) == tag {
			return true
		}
	}
	return false
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func formatInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
