package bytedance

import (
	"fmt"
	"testing"
)

func billResponse(rows []interface{}, total int) *map[string]interface{} {
	result := map[string]interface{}{"ResponseMetadata": map[string]interface{}{}, "Result": map[string]interface{}{"List": rows, "Total": float64(total)}}
	return &result
}

func TestBytePlusSnapshotUsesProductDetailsAndAllPages(t *testing.T) {
	calls := 0
	rows, err := bytePlusBillSnapshot("2026-09", func(input map[string]interface{}) (*map[string]interface{}, error) {
		calls++
		if input["IgnoreZero"] != 0 || input["BillPeriod"] != "2026-09" {
			t.Fatal(input)
		}
		if input["GroupTerm"] == 2 {
			return billResponse([]interface{}{map[string]interface{}{"Product": "images"}, map[string]interface{}{"Product": "images"}}, 2), nil
		}
		products, ok := input["Product"].([]string)
		if !ok || len(products) != 1 || products[0] != "images" {
			t.Fatal("must scope usage bills to product", input)
		}
		offset := input["Offset"].(int)
		result := []interface{}{}
		for i := offset; i < 101 && i < offset+100; i++ {
			result = append(result, map[string]interface{}{"BillDetailId": fmt.Sprint(i), "PaidAmount": "0"})
		}
		return billResponse(result, 101), nil
	})
	if err != nil || len(rows) != 101 || calls != 3 {
		t.Fatalf("rows=%d calls=%d err=%v", len(rows), calls, err)
	}
}

func TestBytePlusRejectsIncompleteOrChangingSnapshot(t *testing.T) {
	for _, mode := range []string{"short", "changed", "missing_result", "bad_count"} {
		t.Run(mode, func(t *testing.T) {
			_, err := bytePlusBillSnapshot("2026-09", func(input map[string]interface{}) (*map[string]interface{}, error) {
				if input["GroupTerm"] == 2 {
					return billResponse([]interface{}{map[string]interface{}{"Product": "images"}}, 1), nil
				}
				if mode == "missing_result" {
					r := map[string]interface{}{"List": []interface{}{}}
					return &r, nil
				}
				if mode == "bad_count" {
					r := billResponse(nil, 0)
					(*r)["Result"].(map[string]interface{})["Total"] = "not a number"
					return r, nil
				}
				if mode == "short" {
					return billResponse([]interface{}{map[string]interface{}{}}, 2), nil
				}
				rows := []interface{}{}
				for i := 0; i < 100; i++ {
					rows = append(rows, map[string]interface{}{})
				}
				if input["Offset"] == 0 {
					return billResponse(rows, 101), nil
				}
				return billResponse([]interface{}{map[string]interface{}{}}, 102), nil
			})
			if err == nil {
				t.Fatal("incomplete snapshot accepted")
			}
		})
	}
}

func TestSeedreamBillPreservesTariffAndPackageCoverage(t *testing.T) {
	row := map[string]any{"BillDetailId": "bill", "InstanceNo": "model-service", "BillCategory": "Consumption-usage", "Product": "Smart_Drawing_T2I", "ExpandField": "seedream-4-5", "Element": "Seedream 4.5-Piece-Edit", "ExpenseBeginTime": "2026-09-06 20:50:00", "ExpenseEndTime": "2026-09-06 20:55:00", "Count": "1", "DeductionCount": "1", "Unit": "Piece", "PriceUnit": "Piece", "BillingFunction": "Fixed Price", "Price": "0.040000", "OriginalBillAmount": "0.000000", "PaidAmount": "0.00", "UnpaidAmount": "0.00", "Currency": "USD"}
	bill, err := normalizeBill("2026-09", row)
	if err != nil || !bill.Settled || bill.TariffUSD == nil || *bill.TariffUSD != 40000 || bill.PaidUSD == nil || *bill.PaidUSD != 0 || bill.PackageUnits == nil || *bill.PackageUnits != 1 || bill.UsageStart == nil || bill.UsageStart.Hour() != 12 || bill.ImageOperation != "edit" {
		t.Fatalf("%+v %v", bill, err)
	}
	row["BillCategory"] = "consume-new"
	bill, err = normalizeBill("2026-09", row)
	if err != nil || bill.Units != nil || bill.TariffUSD != nil {
		t.Fatal("package purchase treated as generation", bill, err)
	}
}

func TestSeedanceAudioBillUsesThousandTokenUnits(t *testing.T) {
	row := map[string]any{"BillDetailId": "video-bill", "InstanceNo": "model-service", "BillCategory": "Consumption-usage", "Product": "ModelArk_video_generation", "ExpandField": "seedance-1-5-pro", "Element": "Seedance-1.5-pro-inference-audio", "ExpenseBeginTime": "2026-09-09 03:50:00", "ExpenseEndTime": "2026-09-09 03:55:00", "Count": "38.8", "DeductionCount": "38.8", "Unit": "K tokens", "PriceUnit": "K tokens", "Price": "0.002400", "BillingFunction": "Fixed Price", "PaidAmount": "0", "UnpaidAmount": "0", "Currency": "USD"}
	bill, err := normalizeBill("2026-09", row)
	if err != nil || bill.Units == nil || *bill.Units != 38800 || bill.PackageUnits == nil || *bill.PackageUnits != 38800 || bill.TariffUSD == nil || *bill.TariffUSD != 93120 || !bill.Settled || *bill.PaidUSD != 0 || bill.MediaType != "video" || bill.GenerateAudio == nil || !*bill.GenerateAudio || bill.UsageStart.Hour() != 19 {
		t.Fatalf("bill=%+v err=%v", bill, err)
	}
	for _, field := range []string{"BillCategory", "Element", "Unit", "PriceUnit", "BillingFunction", "Currency"} {
		old := row[field]
		row[field] = "unknown"
		bill, err = normalizeBill("2026-09", row)
		if err != nil || bill.TariffUSD != nil {
			t.Fatalf("unverified %s accepted: %+v %v", field, bill, err)
		}
		row[field] = old
	}
}
