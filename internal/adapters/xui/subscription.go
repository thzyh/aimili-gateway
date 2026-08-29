package xui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const managedSubscriptionEmail = "aimili-gateway-subscription"

// EnsureSubscriptionClient keeps one 3x-ui client associated with all
// Gateway-owned VLESS inbounds. Mixed and user-owned inbounds are never
// included, even if their IDs are present in the request.
func (c *Client) EnsureSubscriptionClient(ctx context.Context, desired SubscriptionDesired) (Subscription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateSubscriptionDesired(desired); err != nil {
		return Subscription{}, err
	}
	if err := c.authenticate(ctx); err != nil {
		return Subscription{}, err
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return Subscription{}, err
	}
	allowed := ownedVLESSIDs(snapshot.Inbounds, desired.InboundIDs)
	if len(allowed) == 0 {
		return Subscription{}, &AdapterError{Code: "managed_resource_missing"}
	}

	client, found, err := c.getSubscriptionClient(ctx, desired.ClientEmail)
	if err != nil {
		return Subscription{}, err
	}
	if !found {
		subID, randomErr := randomSubscriptionID()
		if randomErr != nil {
			return Subscription{}, randomErr
		}
		payload := map[string]any{
			"client": map[string]any{
				"id": desired.ClientUUID, "email": desired.ClientEmail,
				"flow": "xtls-rprx-vision", "enable": true, "subId": subID,
			},
			"inboundIds": allowed,
		}
		if _, err := c.call(ctx, http.MethodPost, "panel/api/clients/add", payload, false); err != nil {
			return Subscription{}, err
		}
		client, _, err = c.getSubscriptionClient(ctx, desired.ClientEmail)
		if err != nil {
			return Subscription{}, err
		}
	}
	if client.email != desired.ClientEmail || client.uuid == "" || client.uuid != desired.ClientUUID {
		return Subscription{}, &AdapterError{Code: "ownership_conflict"}
	}
	attachPath := "panel/api/clients/" + url.PathEscape(desired.ClientEmail) + "/attach"
	if _, err := c.call(ctx, http.MethodPost, attachPath, map[string]any{"inboundIds": allowed}, false); err != nil {
		return Subscription{}, err
	}
	client, _, err = c.getSubscriptionClient(ctx, desired.ClientEmail)
	if err != nil {
		return Subscription{}, err
	}
	if client.uuid != desired.ClientUUID || client.email != desired.ClientEmail {
		return Subscription{}, &AdapterError{Code: "ownership_conflict"}
	}
	path := c.readSubscriptionPath(ctx)
	return Subscription{
		ResourceName:     managedSubscriptionEmail,
		ClientID:         client.dbID,
		ClientEmail:      client.email,
		ClientUUID:       client.uuid,
		SubscriptionID:   client.subID,
		InboundIDs:       append([]int64(nil), allowed...),
		SubscriptionPath: path,
	}, nil
}

// SubscriptionURL returns a relative public path. The caller adds the
// configured PublicOrigin; no token is written to logs or persistent state.
func (c *Client) SubscriptionURL(ctx context.Context, subscription Subscription) (string, error) {
	_ = ctx
	if subscription.ResourceName != managedSubscriptionEmail || subscription.SubscriptionID == "" {
		return "", &AdapterError{Code: "invalid_request"}
	}
	if strings.ContainsAny(subscription.SubscriptionID, "/\\?#\x00\r\n") {
		return "", &AdapterError{Code: "invalid_response"}
	}
	path := subscription.SubscriptionPath
	if path == "" {
		path = "/sub/"
	}
	path, err := normalizeSubscriptionPath(path)
	if err != nil {
		return "", err
	}
	return path + url.PathEscape(subscription.SubscriptionID), nil
}

type subscriptionClient struct {
	dbID  int64
	email string
	uuid  string
	subID string
}

// ValidateSubscriptionCoverage is used before any legacy aggregate cleanup.
// Both sets are compared as exact sets so an incomplete subscription cannot
// cause the old entry point to be removed.
func ValidateSubscriptionCoverage(actual, expected []int64) error {
	set := func(values []int64) map[int64]bool {
		result := make(map[int64]bool, len(values))
		for _, value := range values {
			if value > 0 {
				result[value] = true
			}
		}
		return result
	}
	left, right := set(actual), set(expected)
	if len(left) != len(right) {
		return &AdapterError{Code: "subscription_incomplete"}
	}
	for id := range left {
		if !right[id] {
			return &AdapterError{Code: "subscription_incomplete"}
		}
	}
	return nil
}

// DeleteManagedAggregate removes only the historical Gateway aggregate
// resource. Callers must run backup and subscription coverage checks first.
func (c *Client) DeleteManagedAggregate(ctx context.Context, managed ManagedAggregate) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if managed.VLESSInboundID <= 0 || managed.VLESSInboundTag == "" || managed.VLESSPort < 1 ||
		(managed.ResourceName != "agw-aggregate-vless" && managed.ResourceName != "agw-aggregate") {
		return &AdapterError{Code: "invalid_request"}
	}
	if err := c.authenticate(ctx); err != nil {
		return err
	}
	snapshot, err := c.snapshot(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, inbound := range snapshot.Inbounds {
		if inbound.ID != managed.VLESSInboundID {
			continue
		}
		if inbound.Tag != managed.VLESSInboundTag || inbound.Protocol != "vless" || inbound.Port != managed.VLESSPort || inbound.Remark != "Aimili Gateway aggregate VLESS" {
			return &AdapterError{Code: "ownership_conflict"}
		}
		found = true
	}
	if !found {
		return &AdapterError{Code: "managed_resource_missing"}
	}
	if _, err := c.call(ctx, http.MethodPost, "panel/api/inbounds/del/"+formatInt64(managed.VLESSInboundID), map[string]any{}, false); err != nil {
		return &AdapterError{Code: "partial_delete"}
	}
	if _, err := c.call(ctx, http.MethodPost, "panel/api/clients/del/"+url.PathEscape("aimili-gateway-aggregate"), map[string]any{}, false); err != nil {
		return &AdapterError{Code: "partial_delete"}
	}
	setting := cloneObject(snapshot.XraySetting)
	routing, _ := setting["routing"].(map[string]any)
	if routing != nil {
		kept := make([]any, 0)
		for _, rule := range asObjectSlice(routing["rules"]) {
			if ruleContainsInbound(rule, managed.VLESSInboundTag) || stringValue(rule["balancerTag"]) == "agw-aggregate" {
				continue
			}
			kept = append(kept, rule)
		}
		routing["rules"] = kept
		balancers := make([]any, 0)
		for _, balancer := range asObjectSlice(routing["balancers"]) {
			if stringValue(balancer["tag"]) != "agw-aggregate" {
				balancers = append(balancers, balancer)
			}
		}
		routing["balancers"] = balancers
	}
	if observatory, ok := setting["observatory"].(map[string]any); ok {
		selectors := asStringSlice(observatory["subjectSelector"])
		kept := make([]any, 0, len(selectors))
		for _, selector := range selectors {
			if selector != "agw-aggregate" {
				kept = append(kept, selector)
			}
		}
		observatory["subjectSelector"] = kept
	}
	if err := c.updateXray(ctx, setting, snapshot.OutboundTestURL); err != nil {
		return &AdapterError{Code: "partial_delete"}
	}
	return nil
}

func asStringSlice(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func (c *Client) getSubscriptionClient(ctx context.Context, email string) (subscriptionClient, bool, error) {
	obj, err := c.call(ctx, http.MethodGet, "panel/api/clients/get/"+url.PathEscape(email), nil, false)
	if err != nil {
		var adapterError *AdapterError
		if errors.As(err, &adapterError) && adapterError.Code == "upstream_rejected" {
			return subscriptionClient{}, false, nil
		}
		return subscriptionClient{}, false, err
	}
	client, err := parseSubscriptionClient(obj)
	if err != nil {
		return subscriptionClient{}, false, err
	}
	return client, true, nil
}

func parseSubscriptionClient(obj json.RawMessage) (subscriptionClient, error) {
	var raw map[string]any
	if json.Unmarshal(obj, &raw) != nil || raw == nil {
		return subscriptionClient{}, &AdapterError{Code: "invalid_response"}
	}
	clientRaw := raw
	if nested, nestedOK := decodeObject(raw["client"]); nestedOK {
		clientRaw = nested
	}
	result := subscriptionClient{
		dbID:  integerValue(raw["dbId"]),
		email: stringValue(raw["email"]),
		uuid:  stringValue(raw["uuid"]),
		subID: stringValue(raw["subId"]),
	}
	if result.dbID == 0 {
		result.dbID = integerValue(raw["clientId"])
	}
	if result.dbID == 0 {
		result.dbID = integerValue(raw["id"])
	}
	if result.email == "" {
		result.email = stringValue(clientRaw["email"])
	}
	if result.uuid == "" {
		result.uuid = stringValue(clientRaw["uuid"])
	}
	if result.uuid == "" {
		if value := stringValue(clientRaw["id"]); value != "" {
			result.uuid = value
		}
	}
	if result.subID == "" {
		result.subID = stringValue(clientRaw["subId"])
	}
	if result.email == "" || result.uuid == "" || result.subID == "" {
		return subscriptionClient{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func ownedVLESSIDs(inbounds []Inbound, requested []int64) []int64 {
	byID := make(map[int64]Inbound, len(inbounds))
	for _, inbound := range inbounds {
		if inbound.Protocol != "vless" || !isOwnedSubscriptionInbound(inbound) {
			continue
		}
		byID[inbound.ID] = inbound
	}
	seen := make(map[int64]bool, len(requested))
	result := make([]int64, 0, len(requested))
	for _, id := range requested {
		if id > 0 && !seen[id] {
			if _, ok := byID[id]; ok {
				seen[id] = true
				result = append(result, id)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func isOwnedSubscriptionInbound(inbound Inbound) bool {
	if inbound.Tag == "aimili-reality" {
		return inbound.Remark == "Aimili Reality"
	}
	return strings.HasPrefix(inbound.Tag, "agw-") && strings.HasSuffix(inbound.Tag, "-vless") && strings.HasPrefix(inbound.Remark, "Aimili Gateway ")
}

func validateSubscriptionDesired(desired SubscriptionDesired) error {
	if desired.ClientEmail != managedSubscriptionEmail || strings.TrimSpace(desired.ClientUUID) == "" || len(desired.ClientUUID) > 128 || strings.ContainsAny(desired.ClientUUID, " \t\r\n") || len(desired.InboundIDs) == 0 {
		return &AdapterError{Code: "invalid_request"}
	}
	return nil
}

func randomSubscriptionID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", &AdapterError{Code: "random_failed"}
	}
	return hex.EncodeToString(value), nil
}

func (c *Client) readSubscriptionPath(ctx context.Context) string {
	obj, err := c.call(ctx, http.MethodGet, "panel/api/setting/all", nil, false)
	if err != nil {
		return "/sub/"
	}
	var settings map[string]any
	if json.Unmarshal(obj, &settings) != nil || settings == nil {
		return "/sub/"
	}
	path, err := normalizeSubscriptionPath(stringValue(settings["subPath"]))
	if err != nil {
		return "/sub/"
	}
	return path
}

func normalizeSubscriptionPath(value string) (string, error) {
	if value == "" {
		return "/sub/", nil
	}
	if !strings.HasPrefix(value, "/") || !strings.HasSuffix(value, "/") || strings.ContainsAny(value, "?#\\\x00\r\n") || strings.Contains(value, "..") {
		return "", &AdapterError{Code: "invalid_response"}
	}
	return value, nil
}

func integerValue(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		var result int64
		_, _ = typed.Int64()
		result, _ = typed.Int64()
		return result
	default:
		return 0
	}
}
