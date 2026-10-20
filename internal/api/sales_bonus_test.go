package api

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"testing"
)

type bonusFixture struct {
	*salesFixture
	writes int
}

func (f *bonusFixture) SalesBonuses(context.Context, string) (system.BonusDashboard, error) {
	return system.BonusDashboard{People: []system.BonusPerson{}}, nil
}
func (f *bonusFixture) BonusEntries(context.Context, string, string, int) ([]system.BonusEntry, error) {
	return make([]system.BonusEntry, 51), nil
}
func (f *bonusFixture) RecordBonusPayout(context.Context, string, string, system.BonusPayout) (int64, error) {
	f.writes++
	return 1, nil
}
func (f *bonusFixture) SaveBonusSettings(_ context.Context, actor string, s system.BonusSettings) (system.BonusSettings, error) {
	if actor != "alice" {
		return s, system.ErrForbidden
	}
	return s, nil
}
func TestSalesBonusHTTPBoundaries(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &bonusFixture{salesFixture: &salesFixture{clientManagerFixture: &clientManagerFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}, manager: true}, role: system.LeadClientManager}}
	s.system = f
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"System","scopes":["generation","system:manage"]}`)
	target := "/v1/sales/bonuses/11111111-1111-4111-8111-111111111111"
	for _, path := range []string{"/v1/sales/bonuses", target + "/entries"} {
		for _, tc := range []struct {
			session, key string
			status       int
		}{{"alice", "", 200}, {"bob", "", 200}, {"", key, 403}, {"", "", 401}} {
			w := sendTest(h, "GET", path, "", tc.session, tc.key)
			if w.Code != tc.status {
				t.Fatalf("%s %+v %d %s", path, tc, w.Code, w.Body.String())
			}
			if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable bonuses")
			}
		}
	}
	if w := sendTest(h, "GET", target+"/entries?offset=-1", "", "bob", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := sendTest(h, "GET", "/v1/sales/bonuses/invalid/entries", "", "bob", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"amount_usd":"invalid"}`, `{"unknown":1}`} {
		if w := sendTest(h, "POST", target+"/payouts", body, "bob", ""); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := sendTest(h, "POST", target+"/payouts", `{"amount_usd":500,"paid_at":"2026-10-13","method":"Bank","external_id":"11111111-1111-4111-8111-111111111111"}`, "bob", ""); w.Code != 200 || f.writes != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := sendTest(h, "PUT", "/v1/system/sales/bonus-settings", `{"manager_percent":5,"supervisor_percent":1,"version":1}`, "bob", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
