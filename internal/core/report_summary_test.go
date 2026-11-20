package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

type summaryRows struct {
	rows   []CostReportRow
	filter ReportFilter
	err    error
}

func (r *summaryRows) WalkCostReport(_ context.Context, f ReportFilter, visit func(CostReportRow) error) error {
	r.filter = f
	for _, row := range r.rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return r.err
}
func TestReportSummaryFullRangeAndUnknownMeasurements(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	charge := money.USD(100001)
	rows := &summaryRows{}
	for i := 0; i < 151; i++ {
		rows.rows = append(rows.rows, CostReportRow{CreatedAt: start, Provider: "p", Model: "original", PublicModel: "public-name", Status: "succeeded", ConsumedUSD: &charge, Usage: &ProviderUsage{Metrics: []UsageMetric{{Name: "total_tokens", Unit: "token", Quantity: 12}, {Name: "input_tokens", Unit: "token", Quantity: 7}, {Name: "output_tokens", Unit: "token", Quantity: 5}, {Name: "audio_tokens", Unit: "token", Quantity: 2}}}})
	}
	rows.rows = append(rows.rows, CostReportRow{CreatedAt: start.AddDate(0, 0, 1), Provider: "other", Model: "original", Status: "failed"}, CostReportRow{CreatedAt: start.AddDate(0, 0, 2), Status: "running"}, CostReportRow{CreatedAt: start.AddDate(0, 0, 3), ConsumedUSD: &charge})
	out, err := SummarizeCostReport(context.Background(), rows, ReportFilter{From: start, Until: start.AddDate(0, 0, 3), Limit: 1, Offset: 50, KeyID: "key", UserEmail: "user@test", AllOrganizations: true}, "day")
	if err != nil {
		t.Fatal(err)
	}
	if out.Requests != 153 || out.Successful != 151 || out.Failed != 1 || out.PricedRequests != 151 || out.Spend != 151*charge || *out.AverageCost != charge || out.Tokens != 151*12 || out.TokenRequests != 151 {
		t.Fatalf("wrong metrics: %+v", out)
	}
	if rows.filter.Limit != 0 || rows.filter.Offset != 0 || rows.filter.KeyID != "key" || rows.filter.UserEmail != "user@test" || !rows.filter.AllOrganizations {
		t.Fatal("lost filters", rows.filter)
	}
	if len(out.PublicModels) != 3 || out.PublicModels[0].Name != "public-name" || len(out.ProviderModels) != 3 || len(out.Periods) != 3 || out.Periods[1].Requests != 1 {
		t.Fatal(out)
	}
	rows.err = errors.New("database disconnected")
	if _, err := SummarizeCostReport(context.Background(), rows, rows.filter, "day"); err == nil {
		t.Fatal("partial summary returned as success")
	}
}
func TestReportSummaryCalendarBuckets(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Nicosia")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 28, 0, 0, 0, 0, zone)
	for _, tc := range []struct {
		interval string
		days     int
		count    int
		lastFrom string
	}{{"day", 4, 4, "2026-03-31"}, {"week", 4, 2, "2026-03-30"}, {"month", 4, 1, "2026-03-28"}, {"month", 70, 4, "2026-06-01"}} {
		rows := &summaryRows{rows: []CostReportRow{{CreatedAt: time.Date(2026, 3, 29, 22, 30, 0, 0, time.UTC), Status: "succeeded"}}}
		out, err := SummarizeCostReport(context.Background(), rows, ReportFilter{From: start, Until: start.AddDate(0, 0, tc.days)}, tc.interval)
		if err != nil || len(out.Periods) != tc.count || out.Periods[len(out.Periods)-1].From != tc.lastFrom {
			t.Fatal(tc, out, err)
		}
		if tc.interval == "day" && out.Periods[2].Successful != 1 {
			t.Fatal("DST/local date bucket", out)
		}
	}
	if _, err := SummarizeCostReport(context.Background(), &summaryRows{}, ReportFilter{From: start, Until: start.AddDate(0, 0, 1)}, "year"); !errors.Is(err, ErrReportInterval) {
		t.Fatal(err)
	}
}
func TestReportTokenAliases(t *testing.T) {
	for _, tc := range []struct {
		metrics []UsageMetric
		want    float64
		known   bool
	}{
		{nil, 0, false},
		{[]UsageMetric{{Name: "generated_images", Unit: "image", Quantity: 1}}, 0, false},
		{[]UsageMetric{{Name: "total_tokens", Unit: "token", Quantity: 0}}, 0, true},
		{[]UsageMetric{{Name: "input_tokens", Unit: "token", Quantity: 4}, {Name: "completion_tokens", Unit: "token", Quantity: 6}, {Name: "output_tokens", Unit: "token", Quantity: 6}}, 10, true},
	} {
		got, known := reportTokens(&ProviderUsage{Metrics: tc.metrics})
		if got != tc.want || known != tc.known {
			t.Fatal(tc, got, known)
		}
	}
}

func TestReportSummaryBucketsAtMidnightTransitions(t *testing.T) {
	for _, tc := range []struct {
		interval, zone, from, until, boundary string
		periods                               []ReportPeriod
	}{
		{
			"day", "America/Santiago", "2026-09-05", "2026-09-08", "2026-09-06T04:00:00Z",
			[]ReportPeriod{{From: "2026-09-05", To: "2026-09-05"}, {From: "2026-09-06", To: "2026-09-06"}, {From: "2026-09-07", To: "2026-09-07"}},
		},
		{
			"week", "America/Santiago", "2026-09-06", "2026-09-09", "2026-09-07T03:00:00Z",
			[]ReportPeriod{{From: "2026-09-06", To: "2026-09-06"}, {From: "2026-09-07", To: "2026-09-08"}},
		},
		{
			"month", "Asia/Amman", "2011-03-30", "2011-05-03", "2011-03-31T22:00:00Z",
			[]ReportPeriod{{From: "2011-03-30", To: "2011-03-31"}, {From: "2011-04-01", To: "2011-04-30"}, {From: "2011-05-01", To: "2011-05-02"}},
		},
		{
			"day", "America/Havana", "2015-10-31", "2015-11-02", "2015-11-01T04:00:00Z",
			[]ReportPeriod{{From: "2015-10-31", To: "2015-10-31"}, {From: "2015-11-01", To: "2015-11-01"}},
		},
		{
			"day", "Pacific/Apia", "2011-12-29", "2012-01-01", "2011-12-30T10:00:00Z",
			[]ReportPeriod{{From: "2011-12-29", To: "2011-12-29"}, {From: "2011-12-31", To: "2011-12-31"}},
		},
	} {
		t.Run(tc.zone+"/"+tc.interval, func(t *testing.T) {
			zone, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			parse := func(layout, value string) time.Time {
				t.Helper()
				got, err := time.Parse(layout, value)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			f := ReportFilter{
				From:  ReportDayStart(parse("2006-01-02", tc.from), zone),
				Until: ReportDayStart(parse("2006-01-02", tc.until), zone),
			}
			at := parse(time.RFC3339, tc.boundary)
			reader := &summaryRows{rows: []CostReportRow{
				{CreatedAt: f.From.Add(-time.Nanosecond)}, {CreatedAt: at.Add(-time.Nanosecond)},
				{CreatedAt: at}, {CreatedAt: f.Until},
			}}
			summary, err := SummarizeCostReport(context.Background(), reader, f, tc.interval)
			if err != nil || summary.Requests != 2 || len(summary.Periods) != len(tc.periods) {
				t.Fatalf("summary=%+v err=%v", summary, err)
			}
			for i, want := range tc.periods {
				got := summary.Periods[i]
				if i < 2 {
					want.Requests = 1
				}
				if got != want {
					t.Fatalf("bucket %d got %+v want %+v", i, got, want)
				}
			}
		})
	}
}
