package store

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/thzyh/aimili-gateway/internal/domain"
)

func TestProxyGroupRoundTripAndOptimisticVersion(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	group, err := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter)
	if err != nil {
		t.Fatal(err)
	}
	group.CountryName = "日本"
	group.AimiliSlot = 2
	group.VLESSPort = 20000
	group.MixedPort = 30000
	group.VLESSInboundID = 41
	group.MixedInboundID = 42
	group.RealityPublicKey = "public-key"
	group.RealityShortID = "short-id"
	group.RealityServerName = "www.microsoft.com"
	group.CreatedAt = now
	group.UpdatedAt = now
	if err := database.CreateProxyGroup(ctx, group); err != nil {
		t.Fatal(err)
	}

	actual, err := database.GetProxyGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.CountryCode != "JP" || actual.ProxyType != domain.ProxyTypeDatacenter || actual.Version != 1 ||
		actual.VLESSInboundID != 41 || actual.MixedInboundID != 42 || actual.RealityPublicKey != "public-key" ||
		actual.RealityShortID != "short-id" || actual.RealityServerName != "www.microsoft.com" {
		t.Fatalf("unexpected group: %#v", actual)
	}
	actual.Status = domain.ProxyGroupReady
	actual.CandidateID = "candidate-adopted"
	actual.CandidateIP = "198.51.100.20"
	actual.CountryCode = "KR"
	actual.CountryName = "韩国"
	actual.ProxyType = domain.ProxyTypeResidential
	actual.ExitIP = "203.0.113.7"
	actual.UpdatedAt = now.Add(time.Minute)
	if err := database.UpdateProxyGroup(ctx, actual, 1); err != nil {
		t.Fatal(err)
	}
	updated, err := database.GetProxyGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CandidateID != "candidate-adopted" || updated.CandidateIP != "198.51.100.20" || updated.CountryCode != "KR" || updated.ProxyType != domain.ProxyTypeResidential {
		t.Fatalf("candidate adoption was not persisted: %#v", updated)
	}
	if err := database.UpdateProxyGroup(ctx, actual, 1); !errors.Is(err, ErrProxyGroupChanged) {
		t.Fatalf("expected optimistic conflict, got %v", err)
	}
}

func TestProxyGroupUpdateCanRebindManagedResourceNameWithoutChangingStableID(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	group, err := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter)
	if err != nil {
		t.Fatal(err)
	}
	group.AimiliSlot = 2
	group.VLESSPort = 20000
	group.MixedPort = 30000
	group.CreatedAt = now
	group.UpdatedAt = now
	if err := database.CreateProxyGroup(ctx, group); err != nil {
		t.Fatal(err)
	}

	group.ResourceName = "agw-jp-dc-previous"
	group.UpdatedAt = now.Add(time.Minute)
	if err := database.UpdateProxyGroup(ctx, group, 1); err != nil {
		t.Fatal(err)
	}
	updated, err := database.GetProxyGroup(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != "agw-jp-dc" || updated.ResourceName != "agw-jp-dc-previous" || updated.Version != 2 {
		t.Fatalf("managed resource rebind was not persisted with a stable ID: %#v", updated)
	}
}

func TestProxyGroupCountryAndTypeAreUnique(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	group, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeResidential)
	group.AimiliSlot = 1
	group.VLESSPort = 20001
	group.MixedPort = 30001
	group.CreatedAt = time.Now().UTC()
	group.UpdatedAt = group.CreatedAt
	if err := database.CreateProxyGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	duplicate := group
	duplicate.ID = "agw-jp-res-duplicate"
	duplicate.ResourceName = duplicate.ID
	duplicate.AimiliSlot = 2
	duplicate.VLESSPort = 20002
	duplicate.MixedPort = 30002
	if err := database.CreateProxyGroup(ctx, duplicate); !errors.Is(err, ErrProxyGroupExists) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestProxyGroupsAllowMultipleCandidatesInOneCountryAndType(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "candidate-one")
	second, _ := domain.NewProxyGroupIdentity("JP", domain.ProxyTypeDatacenter, "candidate-two")
	for index, group := range []*domain.ProxyGroup{&first, &second} {
		group.AimiliSlot = index + 1
		group.VLESSPort = 20100 + index
		group.MixedPort = 30100 + index
		group.CandidateIP = "198.51.100.10"
		group.CandidateLatencyMS = 25 + index
		group.VLESSLatencyMS = 80 + index
		group.SOCKSLatencyMS = 70 + index
		group.CreatedAt = now
		group.UpdatedAt = now
		if err := database.CreateProxyGroup(ctx, *group); err != nil {
			t.Fatalf("create candidate %d: %v", index, err)
		}
	}
	groups, err := database.ListProxyGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[1].CandidateID != "candidate-two" || groups[1].SOCKSLatencyMS != 71 {
		t.Fatalf("unexpected stored candidates: %#v", groups)
	}
}

func TestCredentialEncryptionUsesPurposeContextAndNeverStoresPlaintext(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	key := bytes.Repeat([]byte{0x42}, 32)
	secret := []byte("credential-plaintext-marker")
	if err := database.PutCredential(ctx, "mixed-password", secret, key); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err := database.db.QueryRowContext(ctx, `SELECT ciphertext FROM encrypted_credentials WHERE purpose = 'mixed-password'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, secret) {
		t.Fatal("database ciphertext contains credential plaintext")
	}
	actual, err := database.GetCredential(ctx, "mixed-password", key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, secret) {
		t.Fatal("credential round trip changed plaintext")
	}
	if _, err := database.GetCredential(ctx, "vless-client", key); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("unexpected missing-purpose error: %v", err)
	}
	if _, err := decryptCredential("vless-client", ciphertext, key); err == nil {
		t.Fatal("ciphertext decrypted under a different purpose")
	}
}

func TestMixedCIDRsRejectFullInternetAndRoundTripSpecificNetworks(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	if err := database.ReplaceMixedCIDRs(ctx, []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}); err == nil {
		t.Fatal("full IPv4 internet CIDR was accepted")
	}
	wanted := []netip.Prefix{
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("2001:db8:1::/48"),
	}
	if err := database.ReplaceMixedCIDRs(ctx, wanted); err != nil {
		t.Fatal(err)
	}
	actual, err := database.ListMixedCIDRs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 2 || actual[0] != wanted[0] || actual[1] != wanted[1] {
		t.Fatalf("CIDR round trip = %#v", actual)
	}
}

func TestMixedSourcePolicyCanDisableRestrictionWithNoCIDRs(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := database.ReplaceMixedSourcePolicy(ctx, MixedSourcePolicy{
		Enabled:     false,
		ApplyStatus: MixedPolicyApplied,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	actual, err := database.GetMixedSourcePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Enabled || len(actual.CIDRs) != 0 || actual.ApplyStatus != MixedPolicyApplied || !actual.UpdatedAt.Equal(now) {
		t.Fatalf("disabled policy = %#v", actual)
	}
}

func TestMixedSourcePolicyRequiresSpecificCIDRsWhenEnabled(t *testing.T) {
	database := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	for name, prefixes := range map[string][]netip.Prefix{
		"empty":         nil,
		"IPv4 internet": {netip.MustParsePrefix("0.0.0.0/0")},
		"IPv6 internet": {netip.MustParsePrefix("::/0")},
	} {
		t.Run(name, func(t *testing.T) {
			err := database.ReplaceMixedSourcePolicy(ctx, MixedSourcePolicy{Enabled: true, CIDRs: prefixes, ApplyStatus: MixedPolicyPending, UpdatedAt: now})
			if err == nil {
				t.Fatal("invalid enabled policy was accepted")
			}
		})
	}
}

func TestAggregateConfigRoundTrip(t *testing.T) {
	database := openTestStore(t)
	want := AggregateConfig{ResourceName: "agw-aggregate-vless", VLESSInboundID: 41, VLESSPort: 21000, Enabled: true, UpdatedAt: time.Unix(1700000000, 0).UTC()}
	if err := database.SaveAggregateConfig(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := database.GetAggregateConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ResourceName != want.ResourceName || got.VLESSInboundID != want.VLESSInboundID || got.VLESSPort != want.VLESSPort || !got.Enabled || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("aggregate config=%#v", got)
	}
}

func TestMainEgressMetadataPersistsWithoutUsingASlot(t *testing.T) {
	database := openTestStore(t)
	want := MainEgress{ResourceName: "agw-main", CountryCode: "JP", CountryName: "日本", ProxyType: domain.ProxyTypeDatacenter, ExitIP: "203.0.113.20", VLESSInboundID: 1, MixedInboundID: 99, VLESSPort: 8443, MixedPort: 31000, Enabled: true, UpdatedAt: time.Unix(1700000000, 0).UTC()}
	if err := database.SaveMainEgress(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	var name string
	var vlessPort, mixedPort, enabled int
	if err := database.db.QueryRowContext(context.Background(), `SELECT resource_name, vless_port, mixed_port, enabled FROM main_egress WHERE id=1`).Scan(&name, &vlessPort, &mixedPort, &enabled); err != nil {
		t.Fatal(err)
	}
	if name != "agw-main" || vlessPort != 8443 || mixedPort != 31000 || enabled != 1 {
		t.Fatalf("main metadata=%q %d %d %d", name, vlessPort, mixedPort, enabled)
	}
}
