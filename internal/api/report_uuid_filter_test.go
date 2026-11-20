package api

import (
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestCostReportCanonicalizesUUIDFilters(t *testing.T) {
	const userID = "abcdefab-1111-4111-8111-abcdefabcdef"
	const keyID = "feedface-2222-4222-8222-feedfacefeed"
	s, _ := newPersonalTestServer()
	svc := &exportReportService{reportTestService: &reportTestService{accountService: s.service.(*accountService)}}
	s.service = svc
	h := s.handler()
	tests := []struct {
		name, query, user, email string
	}{
		{"primary user alias", "user=" + strings.ToUpper(userID) + "&user_id=ignored", userID, ""},
		{"fallback user alias", "user_id=" + strings.ReplaceAll(userID, "-", ""), userID, ""},
		{"email precedence", "user=Case%40example.com&user_id=ignored", "", "Case@example.com"},
	}
	modes := []struct{ name, query string }{
		{"list", ""},
		{"usage", "&view=summary"},
		{"CSV", "&format=csv"},
	}
	for _, tc := range tests {
		for _, mode := range modes {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				svc.filter.UserID, svc.filter.UserEmail, svc.filter.KeyID = "", "", ""
				svc.owner = ""
				url := "/v1/admin/reports?" + tc.query + "&api_key_id=" + strings.ToUpper(keyID) + mode.query
				w := sendTest(h, "GET", url, "", "alice", "")
				if w.Code != 200 || svc.filter.UserID != tc.user || svc.filter.UserEmail != tc.email ||
					svc.filter.KeyID != keyID || svc.owner != identity.PersonalOwnerID("alice") {
					t.Fatalf("HTTP %d, owner %q, filter %+v", w.Code, svc.owner, svc.filter)
				}
			})
		}
	}
}
