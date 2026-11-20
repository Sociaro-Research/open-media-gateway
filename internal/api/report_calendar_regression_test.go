package api

import (
	"testing"
	"time"
)

func TestReportsUseInclusiveLocalCalendarDates(t *testing.T) {
	for _, tc := range []struct{ zone, date, fromUTC, untilUTC string }{
		{"UTC", "2026-09-06", "2026-09-06T00:00:00Z", "2026-09-07T00:00:00Z"},
		{"Asia%2FKolkata", "2026-09-06", "2026-09-05T18:30:00Z", "2026-09-06T18:30:00Z"},
		{"America%2FSantiago", "2026-09-06", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"America%2FSantiago", "2026-09-05", "2026-09-05T04:00:00Z", "2026-09-06T04:00:00Z"},
		{"America%2FSantiago", "2026-04-04", "2026-04-04T03:00:00Z", "2026-04-05T04:00:00Z"},
		{"America%2FHavana", "2015-11-01", "2015-11-01T04:00:00Z", "2015-11-02T05:00:00Z"},
		{"America%2FNew_York", "4096-12-31", "4096-12-31T05:00:00Z", "4097-01-01T05:00:00Z"},
		{"Pacific%2FApia", "2011-12-30", "2011-12-30T10:00:00Z", "2011-12-30T10:00:00Z"},
	} {
		t.Run(tc.zone+"/"+tc.date, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			svc := &exportReportService{reportTestService: &reportTestService{accountService: s.service.(*accountService)}}
			s.service = svc
			for _, mode := range []string{"", "&view=summary", "&format=csv"} {
				w := sendTest(s.handler(), "GET", "/v1/admin/reports?timezone="+tc.zone+"&from="+tc.date+"&to="+tc.date+mode, "", "alice", "")
				from := svc.filter.From.UTC().Format(time.RFC3339)
				until := svc.filter.Until.UTC().Format(time.RFC3339)
				if w.Code != 200 || from != tc.fromUTC || until != tc.untilUTC {
					t.Fatalf("mode %q HTTP %d; got [%s, %s), want [%s, %s)", mode, w.Code, from, until, tc.fromUTC, tc.untilUTC)
				}
			}
		})
	}
}
