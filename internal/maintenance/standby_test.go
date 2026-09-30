package maintenance

import (
	"context"
	"testing"

	"github.com/thzyh/aimili-gateway/internal/adapters/aimili"
	"github.com/thzyh/aimili-gateway/internal/domain"
)

type standbyTestSource struct {
	fakeAimiliSource
	assigned string
}

func (s *standbyTestSource) DedicatedStandbys(context.Context) ([]aimili.DedicatedStandby, error) {
	return nil, nil
}
func (s *standbyTestSource) ConfigureDedicatedStandbys(context.Context, []aimili.DedicatedStandbyConfig) ([]aimili.DedicatedStandby, error) {
	return nil, nil
}
func (s *standbyTestSource) AssignDedicatedStandby(_ context.Context, _ int, id string) (aimili.DedicatedStandby, error) {
	s.assigned = id
	return aimili.DedicatedStandby{}, nil
}

func TestAssignStandbyResolvesPoolIdentityBeforeCallingEgress(t *testing.T) {
	candidate := aimili.Candidate{ID: "JP_60.42.14.88_1513_tcp", CountryCode: "JP", ProxyType: "residential", ProbeStatus: "available"}
	identity, _ := domain.NewProxyGroupIdentity(candidate.CountryCode, domain.ProxyTypeResidential, candidate.ID)
	for _, id := range []string{identity.ID, candidate.ID} {
		source := &standbyTestSource{fakeAimiliSource: fakeAimiliSource{candidates: []aimili.Candidate{candidate}}}
		service, err := New(Config{MaxOnline: 4}, source, &fakeXUISource{}, &fakeGroupSource{}, &fakeAccountStatus{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.AssignDedicatedStandby(context.Background(), 0, id); err != nil {
			t.Fatal(err)
		}
		if source.assigned != candidate.ID {
			t.Fatalf("egress received %q, want native candidate ID", source.assigned)
		}
	}
}

func TestAssignStandbyRejectsStalePoolIdentityWithoutMutation(t *testing.T) {
	source := &standbyTestSource{}
	service, err := New(Config{MaxOnline: 4}, source, &fakeXUISource{}, &fakeGroupSource{}, &fakeAccountStatus{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AssignDedicatedStandby(context.Background(), 0, "agw-jp-res-missing")
	if errorCode(err) != "candidate_not_found" || source.assigned != "" {
		t.Fatalf("err=%v assigned=%q", err, source.assigned)
	}
}
