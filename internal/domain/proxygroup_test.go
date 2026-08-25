package domain

import "testing"

func TestNewProxyGroupIdentityNormalizesCountryAndType(t *testing.T) {
	group, err := NewProxyGroupIdentity("jp", ProxyTypeDatacenter)
	if err != nil {
		t.Fatal(err)
	}
	if group.ID != "agw-jp-dc" || group.ResourceName != "agw-jp-dc" || group.CountryCode != "JP" {
		t.Fatalf("unexpected identity: %#v", group)
	}
	if group.Status != ProxyGroupProvisioning || group.Version != 1 {
		t.Fatalf("unexpected initial state: %#v", group)
	}
}

func TestNewProxyGroupIdentityRejectsOpenEndedValues(t *testing.T) {
	tests := []struct {
		country  string
		typeName ProxyType
	}{
		{country: "JPN", typeName: ProxyTypeResidential},
		{country: "1P", typeName: ProxyTypeResidential},
		{country: "JP", typeName: ProxyType("mobile")},
	}
	for _, test := range tests {
		if _, err := NewProxyGroupIdentity(test.country, test.typeName); err == nil {
			t.Fatalf("accepted country=%q type=%q", test.country, test.typeName)
		}
	}
}

func TestProxyGroupTransitionAllowsOnlyDeclaredLifecycleEdges(t *testing.T) {
	group, err := NewProxyGroupIdentity("JP", ProxyTypeResidential)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Transition(ProxyGroupReady); err != nil {
		t.Fatal(err)
	}
	if err := group.Transition(ProxyGroupRotating); err != nil {
		t.Fatal(err)
	}
	if err := group.Transition(ProxyGroupReady); err != nil {
		t.Fatal(err)
	}
	if err := group.Transition(ProxyGroupProvisioning); err == nil {
		t.Fatal("ready group transitioned back to provisioning")
	}
}
