package bytedance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func seedreamCountRow() map[string]any {
	return map[string]any{
		"BillDetailId": "count-detail", "InstanceNo": "model-service",
		"BillCategory": "Consumption-usage", "Product": "Smart_Drawing_T2I",
		"ExpandField": "seedream-4-5", "Element": "Seedream 4.5-Piece-Generate",
		"ExpenseBeginTime": "2026-09-06 20:50:00", "ExpenseEndTime": "2026-09-06 20:55:00",
		"Count": "1", "DeductionCount": "1", "Unit": "Piece", "PriceUnit": "Piece",
		"BillingFunction": "Fixed Price", "Price": "0.040000",
		"PaidAmount": "0", "UnpaidAmount": "0", "Currency": "USD",
	}
}

func TestSeedreamBillCountRequiresExactWholeUnits(t *testing.T) {
	for _, field := range []string{"Count", "DeductionCount"} {
		for _, value := range []string{"0.99999999999999999", "1.0000000000000001", "1.9999999999999999", "9007199254740993", "1e-400", "-1e-400", "1.5", "-1", "NaN", "Inf", "1e309", ""} {
			t.Run(field+"/"+value, func(t *testing.T) {
				row := seedreamCountRow()
				// A large valid Count lets the deduction boundary reach its own check.
				if field == "DeductionCount" {
					row["Count"] = "9007199254740994"
				}
				row[field] = value
				bill, err := normalizeBill("2026-09", row)
				if err != nil {
					t.Fatal(err)
				}
				if field == "Count" {
					if bill.Units != nil {
						t.Errorf("inexact Count %q became %g units", value, *bill.Units)
					}
					if bill.TariffUSD != nil {
						t.Errorf("inexact Count %q produced tariff %v", value, *bill.TariffUSD)
					}
				}
				if field == "DeductionCount" && bill.PackageUnits != nil {
					t.Errorf("inexact DeductionCount became package units: %v", *bill.PackageUnits)
				}
				var raw map[string]any
				if err := json.Unmarshal(bill.Raw, &raw); err != nil || raw[field] != value {
					t.Fatalf("original count was not preserved: %s, %v", bill.Raw, err)
				}
			})
		}
	}
}

func TestSeedreamExactCountBoundaries(t *testing.T) {
	for _, tc := range []struct {
		count, covered      string
		units, packageUnits float64
	}{
		{"1", "0", 1, 0},
		{"9007199254740992", "9007199254740992", 9007199254740992, 9007199254740992},
		{"9007199254740994", "9007199254740994", 9007199254740994, 9007199254740994},
	} {
		t.Run(tc.count+"/"+tc.covered, func(t *testing.T) {
			row := seedreamCountRow()
			// A zero unit price keeps representation-boundary counts below money overflow.
			row["Count"], row["DeductionCount"], row["Price"] = tc.count, tc.covered, "0"
			bill, err := normalizeBill("2026-09", row)
			if err != nil || bill.Units == nil || *bill.Units != tc.units || bill.PackageUnits == nil || *bill.PackageUnits != tc.packageUnits || bill.TariffUSD == nil || *bill.TariffUSD != 0 {
				t.Fatalf("exact count boundary changed: %+v, %v", bill, err)
			}
		})
	}
}

func TestSeedreamRoundedCountCannotConfirmSingleTask(t *testing.T) {
	row := seedreamCountRow()
	row["Count"] = "1.0000000000000001"
	bill, err := normalizeBill("2026-09", row)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 6, 12, 51, 0, 0, time.UTC)
	units := 1.0
	task := core.Task{
		ID: "completed-image", ProviderIdentity: "fixture", Model: "seedream-4-5-251128",
		MediaType: "image", Status: core.TaskSucceeded, CreatedAt: start, UpdatedAt: start.Add(time.Minute),
		Cost: &core.TaskCost{BilledUnits: &units},
	}
	if matches := core.MatchUsageBills([]core.Task{task}, []core.BillRecord{bill}, "fixture"); len(matches) != 0 {
		t.Fatalf("fractional image count confirmed one completed task: %+v", matches)
	}
}

func TestSeedreamExactCountFormsRemainSupported(t *testing.T) {
	for _, tc := range []struct {
		value string
		units float64
	}{
		{"1", 1}, {"1.0000000000000000", 1}, {"1e0", 1}, {" 1.0 ", 1},
		{"08", 8}, {"010", 10},
	} {
		t.Run(tc.value, func(t *testing.T) {
			row := seedreamCountRow()
			row["Count"], row["DeductionCount"] = tc.value, tc.value
			bill, err := normalizeBill("2026-09", row)
			if err != nil || bill.Units == nil || *bill.Units != tc.units || bill.PackageUnits == nil || *bill.PackageUnits != tc.units || bill.TariffUSD == nil || int64(*bill.TariffUSD) != 40000*int64(tc.units) || !bill.Settled {
				t.Fatalf("valid whole count lost: %+v, %v", bill, err)
			}
		})
	}
}
