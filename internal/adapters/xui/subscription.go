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
	"strconv"
	"strings"
	"unicode"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

const managedSubscriptionEmail = "aimili-gateway-subscription"

// EnsureSubscriptionClient keeps one 3x-ui client associated with all
// Gateway-owned public inbounds. Mixed and user-owned inbounds are never
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
	allowed := ownedPublicIDs(snapshot.Inbounds, desired.InboundIDs)
	if len(allowed) == 0 {
		return Subscription{}, &AdapterError{Code: "managed_resource_missing"}
	}
	if desired.Aliases != nil {
		if err := validateSubscriptionAliasesDesired(desired.Aliases, allowed); err != nil {
			return Subscription{}, err
		}
	}
	if err := c.ensureSubscriptionSortOrder(ctx, allowed); err != nil {
		return Subscription{}, err
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
	if client.subID == "" {
		if desired.SubscriptionID == "" {
			return Subscription{}, &AdapterError{Code: "subscription_incomplete"}
		}
		updatePath := "panel/api/clients/update/" + url.PathEscape(desired.ClientEmail)
		if _, err := c.call(ctx, http.MethodPost, updatePath, map[string]any{
			"id": desired.ClientUUID, "email": desired.ClientEmail, "subId": desired.SubscriptionID,
			"flow": "xtls-rprx-vision", "enable": true,
		}, false); err != nil {
			return Subscription{}, err
		}
		client, found, err = c.getSubscriptionClient(ctx, desired.ClientEmail)
		if err != nil {
			return Subscription{}, err
		}
		if !found {
			return Subscription{}, &AdapterError{Code: "subscription_incomplete"}
		}
	}
	if desired.SubscriptionID != "" && client.subID != desired.SubscriptionID {
		return Subscription{}, &AdapterError{Code: "ownership_conflict"}
	}
	if !sameInboundIDs(client.inboundIDs, allowed) {
		attachPath := "panel/api/clients/" + url.PathEscape(desired.ClientEmail) + "/attach"
		if _, err := c.call(ctx, http.MethodPost, attachPath, map[string]any{"inboundIds": allowed}, false); err != nil {
			return Subscription{}, err
		}
	}
	client, _, err = c.getSubscriptionClient(ctx, desired.ClientEmail)
	if err != nil {
		return Subscription{}, err
	}
	if client.uuid != desired.ClientUUID || client.email != desired.ClientEmail {
		return Subscription{}, &AdapterError{Code: "ownership_conflict"}
	}
	if desired.Aliases != nil {
		if err := c.setSubscriptionAliases(ctx, desired.ClientEmail, desired.Aliases); err != nil {
			return Subscription{}, err
		}
		client, _, err = c.getSubscriptionClient(ctx, desired.ClientEmail)
		if err != nil {
			return Subscription{}, err
		}
		if client.uuid != desired.ClientUUID || client.email != desired.ClientEmail {
			return Subscription{}, &AdapterError{Code: "ownership_conflict"}
		}
		if err := ValidateSubscriptionAliases(client.aliases, desired.Aliases); err != nil {
			return Subscription{}, err
		}
	}
	profiles, err := c.publicProfiles(ctx, allowed, client)
	if err != nil {
		return Subscription{}, err
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
		PublicProfiles:   profiles,
		Aliases:          copySubscriptionAliases(client.aliases),
	}, nil
}

// VerifySubscriptionClient reads the exclusive client and proves its current
// associated aliases without changing associations or aliases.
func (c *Client) VerifySubscriptionClient(ctx context.Context, desired SubscriptionDesired) (Subscription, error) {
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
	allowed := ownedPublicIDs(snapshot.Inbounds, desired.InboundIDs)
	if !sameInboundIDs(allowed, desired.InboundIDs) {
		return Subscription{}, &AdapterError{Code: "managed_resource_missing"}
	}
	if !hasSubscriptionSortOrder(snapshot.Inbounds, allowed) {
		return Subscription{}, &AdapterError{Code: "managed_resource_drift"}
	}
	if desired.Aliases != nil {
		if err := validateSubscriptionAliasesDesired(desired.Aliases, allowed); err != nil {
			return Subscription{}, err
		}
	}
	client, found, err := c.getSubscriptionClient(ctx, desired.ClientEmail)
	if err != nil {
		return Subscription{}, err
	}
	if !found || client.uuid != desired.ClientUUID || client.email != desired.ClientEmail || !sameInboundIDs(client.inboundIDs, allowed) {
		return Subscription{}, &AdapterError{Code: "subscription_incomplete"}
	}
	if desired.Aliases != nil {
		if err := ValidateSubscriptionAliases(client.aliases, desired.Aliases); err != nil {
			return Subscription{}, err
		}
	}
	profiles, err := c.publicProfiles(ctx, allowed, client)
	if err != nil {
		return Subscription{}, err
	}
	return Subscription{ResourceName: managedSubscriptionEmail, ClientID: client.dbID, ClientEmail: client.email, ClientUUID: client.uuid,
		SubscriptionID: client.subID, InboundIDs: append([]int64(nil), allowed...), SubscriptionPath: c.readSubscriptionPath(ctx),
		PublicProfiles: profiles, Aliases: copySubscriptionAliases(client.aliases)}, nil
}

// RepairSubscriptionAliases corrects aliases on the exclusive Gateway
// subscription client only after proving that its identity and inbound set are
// already exact. It never creates a client or changes inbound associations.
func (c *Client) RepairSubscriptionAliases(ctx context.Context, desired SubscriptionDesired) (Subscription, error) {
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
	allowed := ownedPublicIDs(snapshot.Inbounds, desired.InboundIDs)
	if !sameInboundIDs(allowed, desired.InboundIDs) {
		return Subscription{}, &AdapterError{Code: "managed_resource_missing"}
	}
	if !hasSubscriptionSortOrder(snapshot.Inbounds, allowed) {
		return Subscription{}, &AdapterError{Code: "managed_resource_drift"}
	}
	if err := validateSubscriptionAliasesDesired(desired.Aliases, allowed); err != nil {
		return Subscription{}, err
	}
	client, found, err := c.getSubscriptionClient(ctx, desired.ClientEmail)
	if err != nil {
		return Subscription{}, err
	}
	if !found || !sameInboundIDs(client.inboundIDs, allowed) {
		return Subscription{}, &AdapterError{Code: "subscription_incomplete"}
	}
	if client.uuid != desired.ClientUUID || client.email != desired.ClientEmail {
		return Subscription{}, &AdapterError{Code: "ownership_conflict"}
	}
	if err := ValidateSubscriptionAliases(client.aliases, desired.Aliases); err != nil {
		if err := c.setSubscriptionAliases(ctx, desired.ClientEmail, desired.Aliases); err != nil {
			return Subscription{}, err
		}
		client, found, err = c.getSubscriptionClient(ctx, desired.ClientEmail)
		if err != nil {
			return Subscription{}, err
		}
		if !found || !sameInboundIDs(client.inboundIDs, allowed) {
			return Subscription{}, &AdapterError{Code: "subscription_incomplete"}
		}
		if client.uuid != desired.ClientUUID || client.email != desired.ClientEmail {
			return Subscription{}, &AdapterError{Code: "ownership_conflict"}
		}
		if err := ValidateSubscriptionAliases(client.aliases, desired.Aliases); err != nil {
			return Subscription{}, err
		}
	}
	profiles, err := c.publicProfiles(ctx, allowed, client)
	if err != nil {
		return Subscription{}, err
	}
	return Subscription{ResourceName: managedSubscriptionEmail, ClientID: client.dbID, ClientEmail: client.email, ClientUUID: client.uuid,
		SubscriptionID: client.subID, InboundIDs: append([]int64(nil), allowed...), SubscriptionPath: c.readSubscriptionPath(ctx),
		PublicProfiles: profiles, Aliases: copySubscriptionAliases(client.aliases)}, nil
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
	dbID       int64
	email      string
	uuid       string
	subID      string
	auth       string
	inboundIDs []int64
	aliases    map[int64]string
}

// ValidateSubscriptionAliases rejects a response that does not exactly match
// the aliases requested for the exclusive Gateway subscription client.
func ValidateSubscriptionAliases(actual, expected map[int64]string) error {
	if len(actual) != len(expected) {
		return &AdapterError{Code: "subscription_incomplete"}
	}
	for id, alias := range expected {
		if id < 1 || alias == "" || actual[id] != alias {
			return &AdapterError{Code: "subscription_incomplete"}
		}
	}
	return nil
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
	details, err := c.inboundDetails(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, inbound := range details {
		settings, settingsOK := decodeObject(inbound.Settings)
		clients := asObjectSlice(settings["clients"])
		containsAggregateClient := false
		for _, client := range clients {
			if stringValue(client["email"]) == "aimili-gateway-aggregate" {
				containsAggregateClient = true
			}
		}
		if inbound.ID != managed.VLESSInboundID && containsAggregateClient {
			return &AdapterError{Code: "ownership_conflict"}
		}
		if inbound.ID != managed.VLESSInboundID {
			continue
		}
		if inbound.Tag != managed.VLESSInboundTag || inbound.Protocol != "vless" || inbound.Port != managed.VLESSPort || inbound.Remark != "Aimili Gateway aggregate VLESS" {
			return &AdapterError{Code: "ownership_conflict"}
		}
		if !settingsOK || len(clients) != 1 || !containsAggregateClient || stringValue(clients[0]["flow"]) != "xtls-rprx-vision" {
			return &AdapterError{Code: "ownership_conflict"}
		}
		found = true
	}
	if !found {
		routing, _ := snapshot.XraySetting["routing"].(map[string]any)
		for _, rule := range asObjectSlice(routing["rules"]) {
			if ruleContainsInbound(rule, managed.VLESSInboundTag) || stringValue(rule["balancerTag"]) == "agw-aggregate" {
				return &AdapterError{Code: "managed_resource_drift"}
			}
		}
		for _, balancer := range asObjectSlice(routing["balancers"]) {
			if stringValue(balancer["tag"]) == "agw-aggregate" {
				return &AdapterError{Code: "managed_resource_drift"}
			}
		}
		if _, exists := snapshot.XraySetting["observatory"]; exists {
			return &AdapterError{Code: "managed_resource_drift"}
		}
		return nil
	}
	setting := cloneObject(snapshot.XraySetting)
	routing, _ := setting["routing"].(map[string]any)
	if routing == nil {
		return &AdapterError{Code: "managed_resource_drift"}
	}
	keptRules := make([]any, 0)
	ownedRules := 0
	for _, rule := range asObjectSlice(routing["rules"]) {
		if ruleContainsInbound(rule, managed.VLESSInboundTag) || stringValue(rule["balancerTag"]) == "agw-aggregate" {
			inboundTags := stringValues(rule["inboundTag"])
			if len(rule) != 3 || stringValue(rule["type"]) != "field" || stringValue(rule["balancerTag"]) != "agw-aggregate" || len(inboundTags) != 1 || inboundTags[0] != managed.VLESSInboundTag {
				return &AdapterError{Code: "ownership_conflict"}
			}
			ownedRules++
			continue
		}
		keptRules = append(keptRules, rule)
	}
	if ownedRules != 1 {
		return &AdapterError{Code: "managed_resource_drift"}
	}
	routing["rules"] = keptRules
	keptBalancers := make([]any, 0)
	ownedBalancers := 0
	var aggregateSelectors []string
	for _, balancer := range asObjectSlice(routing["balancers"]) {
		if stringValue(balancer["tag"]) == "agw-aggregate" {
			selectors := stringValues(balancer["selector"])
			strategy, strategyOK := decodeObject(balancer["strategy"])
			if len(selectors) == 0 || !strategyOK || stringValue(strategy["type"]) != "leastPing" {
				return &AdapterError{Code: "ownership_conflict"}
			}
			for _, selector := range selectors {
				if selector != "aimili-socks" && !(strings.HasPrefix(selector, "agw-") && strings.HasSuffix(selector, "-socks")) {
					return &AdapterError{Code: "ownership_conflict"}
				}
			}
			ownedBalancers++
			aggregateSelectors = selectors
			continue
		}
		keptBalancers = append(keptBalancers, balancer)
	}
	if ownedBalancers != 1 {
		return &AdapterError{Code: "managed_resource_drift"}
	}
	routing["balancers"] = keptBalancers
	observatory, ok := setting["observatory"].(map[string]any)
	if !ok || stringValue(observatory["probeURL"]) != "https://www.google.com/generate_204" {
		return &AdapterError{Code: "ownership_conflict"}
	}
	observatorySelectors := asStringSlice(observatory["subjectSelector"])
	if len(observatorySelectors) != len(aggregateSelectors) {
		return &AdapterError{Code: "ownership_conflict"}
	}
	selectorSet := make(map[string]bool, len(aggregateSelectors))
	for _, selector := range aggregateSelectors {
		selectorSet[selector] = true
	}
	for _, selector := range observatorySelectors {
		if !selectorSet[selector] {
			return &AdapterError{Code: "ownership_conflict"}
		}
	}
	delete(setting, "observatory")
	if _, err := c.call(ctx, http.MethodPost, "panel/api/inbounds/del/"+formatInt64(managed.VLESSInboundID), map[string]any{}, false); err != nil {
		return &AdapterError{Code: "partial_delete"}
	}
	if _, err := c.call(ctx, http.MethodPost, "panel/api/clients/del/"+url.PathEscape("aimili-gateway-aggregate"), map[string]any{}, false); err != nil {
		return &AdapterError{Code: "partial_delete"}
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
		auth:  stringValue(raw["auth"]),
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
	if result.auth == "" {
		result.auth = stringValue(clientRaw["auth"])
	}
	result.inboundIDs = integerValues(raw["inboundIds"])
	if len(result.inboundIDs) == 0 {
		result.inboundIDs = integerValues(clientRaw["inboundIds"])
	}
	aliases, err := parseSubscriptionAliases(raw["inboundAliases"])
	if err != nil {
		return subscriptionClient{}, err
	}
	result.aliases = aliases
	if result.email == "" || result.uuid == "" {
		return subscriptionClient{}, &AdapterError{Code: "invalid_response"}
	}
	return result, nil
}

func (c *Client) setSubscriptionAliases(ctx context.Context, email string, aliases map[int64]string) error {
	items := make([]map[string]any, 0, len(aliases))
	ids := make([]int64, 0, len(aliases))
	for id := range aliases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		items = append(items, map[string]any{"inboundId": id, "alias": aliases[id]})
	}
	path := "panel/api/clients/" + url.PathEscape(email) + "/inboundAliases"
	_, err := c.call(ctx, http.MethodPost, path, map[string]any{"aliases": items}, false)
	return err
}

func parseSubscriptionAliases(value any) (map[int64]string, error) {
	if value == nil {
		return nil, nil
	}
	if mapping, ok := value.(map[string]any); ok {
		result := make(map[int64]string, len(mapping))
		for key, value := range mapping {
			id, err := strconv.ParseInt(key, 10, 64)
			name, nameOK := value.(string)
			if err != nil || id < 1 || strconv.FormatInt(id, 10) != key || !nameOK {
				return nil, &AdapterError{Code: "invalid_response"}
			}
			if name == "" {
				continue
			}
			result[id] = name
		}
		return result, nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, &AdapterError{Code: "invalid_response"}
	}
	result := make(map[int64]string, len(raw))
	for _, value := range raw {
		alias, ok := decodeObject(value)
		if !ok {
			return nil, &AdapterError{Code: "invalid_response"}
		}
		id := integerValue(alias["inboundId"])
		name := stringValue(alias["alias"])
		if id < 1 {
			return nil, &AdapterError{Code: "invalid_response"}
		}
		if name == "" {
			continue
		}
		if _, exists := result[id]; exists {
			return nil, &AdapterError{Code: "invalid_response"}
		}
		result[id] = name
	}
	return result, nil
}

func validateSubscriptionAliasesDesired(aliases map[int64]string, allowed []int64) error {
	if len(aliases) != len(allowed) {
		return &AdapterError{Code: "invalid_request"}
	}
	owned := make(map[int64]bool, len(allowed))
	for _, id := range allowed {
		owned[id] = true
	}
	for id, alias := range aliases {
		if !owned[id] || !validSubscriptionAlias(alias) {
			return &AdapterError{Code: "invalid_request"}
		}
	}
	return nil
}

func validSubscriptionAlias(alias string) bool {
	if alias == "" || strings.TrimSpace(alias) != alias || len([]rune(alias)) > 96 {
		return false
	}
	return !strings.ContainsFunc(alias, unicode.IsControl)
}

func copySubscriptionAliases(aliases map[int64]string) map[int64]string {
	if aliases == nil {
		return nil
	}
	result := make(map[int64]string, len(aliases))
	for id, alias := range aliases {
		result[id] = alias
	}
	return result
}

func ownedPublicIDs(inbounds []Inbound, requested []int64) []int64 {
	byID := make(map[int64]Inbound, len(inbounds))
	for _, inbound := range inbounds {
		if (inbound.Protocol != "vless" && inbound.Protocol != "hysteria") || !isOwnedSubscriptionInbound(inbound) {
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
	return result
}

func hasSubscriptionSortOrder(inbounds []Inbound, ordered []int64) bool {
	byID := make(map[int64]Inbound, len(inbounds))
	for _, inbound := range inbounds {
		byID[inbound.ID] = inbound
	}
	for index, id := range ordered {
		inbound, ok := byID[id]
		if !ok || inbound.SubSortIndex != index+1 {
			return false
		}
	}
	return true
}

// ensureSubscriptionSortOrder updates only Gateway-owned public inbounds and
// keeps every other inbound field intact. 3x-ui uses subSortIndex, then the
// database ID, to order links in subscription responses.
func (c *Client) ensureSubscriptionSortOrder(ctx context.Context, ordered []int64) error {
	details, err := c.inboundDetails(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]inboundDetail, len(details))
	for _, detail := range details {
		byID[detail.ID] = detail
	}
	changed := false
	for index, id := range ordered {
		detail, ok := byID[id]
		if !ok || !isOwnedSubscriptionInbound(Inbound{ID: detail.ID, Tag: detail.Tag, Remark: detail.Remark, Protocol: detail.Protocol, Port: detail.Port}) {
			return &AdapterError{Code: "managed_resource_missing"}
		}
		want := index + 1
		if detail.SubSortIndex == want {
			continue
		}
		payload := cloneObject(detail.Raw)
		payload["subSortIndex"] = want
		if err := c.updateInbound(ctx, id, payload); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	updated, err := c.inbounds(ctx)
	if err != nil {
		return err
	}
	if !hasSubscriptionSortOrder(updated, ordered) {
		return &AdapterError{Code: "write_verification_failed"}
	}
	return nil
}

func sameInboundIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	a := append([]int64(nil), left...)
	b := append([]int64(nil), right...)
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func integerValues(value any) []int64 {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]int64, 0, len(raw))
	for _, item := range raw {
		if id := integerValue(item); id > 0 {
			result = append(result, id)
		}
	}
	return result
}

func (c *Client) publicProfiles(ctx context.Context, allowed []int64, client subscriptionClient) ([]PublicProfile, error) {
	details, err := c.inboundDetails(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]inboundDetail, len(details))
	for _, detail := range details {
		byID[detail.ID] = detail
	}
	profiles := make([]PublicProfile, 0, len(allowed))
	for _, id := range allowed {
		detail, ok := byID[id]
		if !ok || !isOwnedSubscriptionInbound(Inbound{ID: detail.ID, Tag: detail.Tag, Remark: detail.Remark, Protocol: detail.Protocol, Port: detail.Port}) {
			return nil, &AdapterError{Code: "managed_resource_missing"}
		}
		profile, err := publicProfile(detail, client)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func publicProfile(detail inboundDetail, client subscriptionClient) (PublicProfile, error) {
	profile := PublicProfile{InboundID: detail.ID}
	settings, settingsOK := decodeObject(detail.Settings)
	stream, streamOK := decodeObject(detail.StreamSettings)
	if !settingsOK || !streamOK {
		return PublicProfile{}, &AdapterError{Code: "invalid_response"}
	}
	if detail.Protocol == "hysteria" {
		hysteria, ok := decodeObject(stream["hysteriaSettings"])
		tls, tlsOK := decodeObject(stream["tlsSettings"])
		if !ok || !tlsOK || stringValue(stream["network"]) != "hysteria" || stringValue(stream["security"]) != "tls" || integerValue(hysteria["version"]) != 2 || integerValue(settings["version"]) != 2 {
			return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
		}
		profile.ServerName = stringValue(tls["serverName"])
		for _, candidate := range asObjectSlice(settings["clients"]) {
			if stringValue(candidate["email"]) == client.email && stringValue(candidate["auth"]) != "" {
				profile.Auth = stringValue(candidate["auth"])
			}
		}
		if profile.Auth == "" || profile.ServerName == "" || (client.auth != "" && profile.Auth != client.auth) {
			return PublicProfile{}, &AdapterError{Code: "ownership_conflict"}
		}
		profile.Mode = domain.ProtocolHysteria2QUICTLS
		return profile, nil
	}
	if detail.Protocol != "vless" || stringValue(stream["security"]) != "reality" {
		return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
	}
	profile.ClientID = client.uuid
	reality, ok := decodeObject(stream["realitySettings"])
	clientSettings, clientSettingsOK := decodeObject(reality["settings"])
	serverNames := stringValues(reality["serverNames"])
	shortIDs := stringValues(reality["shortIds"])
	serverName, shortID := "", ""
	if detail.Tag == "aimili-reality" {
		var namesOK, idsOK bool
		serverName, namesOK = selectLegacyRealityServerName(serverNames, "")
		shortID, idsOK = selectLegacyRealityShortID(shortIDs)
		if !namesOK || !idsOK {
			return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
		}
	} else if len(serverNames) == 1 && len(shortIDs) == 1 {
		serverName, shortID = serverNames[0], shortIDs[0]
	} else {
		return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
	}
	if !ok || !clientSettingsOK {
		return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
	}
	profile.PublicKey = stringValue(clientSettings["publicKey"])
	profile.MLDSA65Verify = stringValue(clientSettings["mldsa65Verify"])
	profile.ShortID = shortID
	profile.ServerName = serverName
	if profile.PublicKey == "" || profile.ShortID == "" || profile.ServerName == "" {
		return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
	}
	switch stringValue(stream["network"]) {
	case "tcp":
		profile.Mode = domain.ProtocolVLESSTCPRealityVision
	case "xhttp":
		xhttp, ok := decodeObject(stream["xhttpSettings"])
		profile.XHTTPPath = stringValue(xhttp["path"])
		if !ok || !strings.HasPrefix(profile.XHTTPPath, "/") || strings.ContainsAny(profile.XHTTPPath, "?#\\\x00\r\n") {
			return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
		}
		profile.Mode = domain.ProtocolVLESSXHTTPReality
	default:
		return PublicProfile{}, &AdapterError{Code: "managed_resource_drift"}
	}
	return profile, nil
}

func isOwnedSubscriptionInbound(inbound Inbound) bool {
	if inbound.Tag == "aimili-reality" {
		return inbound.Remark == "Aimili Reality"
	}
	return strings.HasPrefix(inbound.Tag, "agw-") && strings.HasSuffix(inbound.Tag, "-vless") && strings.HasPrefix(inbound.Remark, "Aimili Gateway ")
}

func validateSubscriptionDesired(desired SubscriptionDesired) error {
	if desired.ClientEmail != managedSubscriptionEmail || strings.TrimSpace(desired.ClientUUID) == "" || len(desired.ClientUUID) > 128 || strings.ContainsAny(desired.ClientUUID, " \t\r\n") ||
		len(desired.SubscriptionID) > 256 || strings.ContainsAny(desired.SubscriptionID, "/\\?#\x00\r\n\t ") || len(desired.InboundIDs) == 0 {
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
	obj, err := c.call(ctx, http.MethodPost, "panel/api/setting/all", map[string]any{}, false)
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
