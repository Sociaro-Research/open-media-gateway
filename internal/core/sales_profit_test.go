package core

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"strings"
	"testing"
	"time"
)

func TestSalesProfitSummaryAndPrivacy(t *testing.T) {
	now := time.Now().UTC()
	gain, loss := money.USD(30000000), money.USD(-5000000)
	rows := &summaryRows{rows: []CostReportRow{{CreatedAt: now, ProfitStatus: "recognized", ProfitUSD: &gain}, {CreatedAt: now, ProfitStatus: "recognized", ProfitUSD: &loss}, {CreatedAt: now, ProfitStatus: "awaiting_billing"}, {CreatedAt: now, ProfitStatus: "not_tracked"}}}
	summary, e := SummarizeCostReport(context.Background(), rows, ReportFilter{From: now.Add(-time.Minute), Until: now.Add(time.Minute)}, "day")
	if e != nil {
		t.Fatal(e)
	}
	if summary.Profit != 25000000 || summary.RecognizedProfitRequests != 2 || summary.PendingProfitRequests != 1 || summary.Periods[0].Profit != 25000000 {
		t.Fatal(summary)
	}
	payload := map[string]any{"summary": summary, "data": rows.rows}
	customer, e := CustomerJSON(payload)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(customer), "profit") {
		t.Fatalf("customer profit leak %s", customer)
	}
	sales, e := SalesJSON(payload)
	if e != nil {
		t.Fatal(e)
	}
	var decoded map[string]any
	if e = json.Unmarshal(sales, &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded["summary"].(map[string]any)["profit_usd"] != float64(25) {
		t.Fatal(string(sales))
	}
}
