package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/config"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
)

func TestFetchBillsValidatesCountsAndSnapshot(t *testing.T) {
	for _, mode := range []string{"inexact_count", "missing_detail_id", "duplicate_detail_id", "incomplete_snapshot"} {
		t.Run(mode, func(t *testing.T) {
			repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
			cipher, err := providerconfig.NewCipher("synthetic-bill-count-key")
			if err != nil {
				t.Fatal(err)
			}
			manager := NewManager(repo, cipher, config.Config{ProviderHTTPTimeout: time.Second})
			calls := 0
			manager.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "open.byteplusapi.com" || r.URL.Query().Get("Action") != "ListBillDetail" {
					return nil, fmt.Errorf("unexpected fixture request")
				}
				calls++
				body := `{"Result":{"Total":1,"List":[{"Product":"Smart_Drawing_T2I"}]}}`
				if calls == 2 {
					row := map[string]any{
						"BillDetailId": "first", "InstanceNo": "model-service", "Currency": "USD",
						"PaidAmount": "0", "UnpaidAmount": "0", "Count": "1", "DeductionCount": "1",
						"Product": "Smart_Drawing_T2I", "BillCategory": "Consumption-usage", "ExpandField": "seedream-4-5",
						"ExpenseBeginTime": "2026-09-06 20:50:00", "ExpenseEndTime": "2026-09-06 20:55:00",
						"Unit": "Piece", "PriceUnit": "Piece", "Price": "0.04", "BillingFunction": "Fixed Price",
					}
					first := make(map[string]any, len(row))
					for key, value := range row {
						first[key] = value
					}
					row["BillDetailId"], row["Count"] = "second", "1.0000000000000001"
					total := 2
					switch mode {
					case "missing_detail_id":
						delete(row, "BillDetailId")
					case "duplicate_detail_id":
						row["BillDetailId"] = "first"
					case "incomplete_snapshot":
						total = 3
					}
					wire, err := json.Marshal(map[string]any{"Result": map[string]any{"Total": total, "List": []any{first, row}}})
					if err != nil {
						return nil, err
					}
					body = string(wire)
				}
				if calls > 2 {
					return nil, fmt.Errorf("unexpected fixture page")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			key, endpoint, access, secret := "synthetic-inference", "https://provider.example/api/v3", "synthetic-billing", "synthetic-secret"
			if _, err := manager.Update(context.Background(), "bytedance", ProviderUpdate{APIKey: &key, BaseURL: &endpoint, BillingAccessKeyID: &access, BillingAccessKeySecret: &secret}); err != nil {
				t.Fatal(err)
			}
			bills, fingerprint, err := manager.FetchBills(context.Background(), "bytedance", "2026-09")
			if calls != 2 || fingerprint == "" {
				t.Fatalf("snapshot path not exercised: calls=%d identity=%q err=%v", calls, fingerprint, err)
			}
			if mode != "inexact_count" {
				if err == nil || bills != nil {
					t.Fatalf("invalid snapshot returned partial import: bills=%+v err=%v", bills, err)
				}
				return
			}
			if err != nil || len(bills) != 2 {
				t.Fatalf("unmatchable rows must remain auditable: bills=%+v err=%v", bills, err)
			}
			if bills[0].Units == nil || *bills[0].Units != 1 || bills[0].TariffUSD == nil || *bills[0].TariffUSD != 40000 || bills[1].Units != nil || bills[1].TariffUSD != nil {
				t.Errorf("SDK/manager import rounded a fractional count: %+v", bills)
			}
			var raw map[string]any
			if err := json.Unmarshal(bills[1].Raw, &raw); err != nil || raw["Count"] != "1.0000000000000001" {
				t.Fatalf("original Count lost: %s err=%v", bills[1].Raw, err)
			}
		})
	}
}
