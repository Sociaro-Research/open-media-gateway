package bytedance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
	"github.com/byteplus-sdk/byteplus-go-sdk-v2/byteplus"
	"github.com/byteplus-sdk/byteplus-go-sdk-v2/byteplus/session"
	bytebilling "github.com/byteplus-sdk/byteplus-go-sdk-v2/service/billing"
	"math"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Credentials = providerkit.Credentials

func fetchBills(ctx context.Context, c Credentials, month string, httpClient *http.Client) ([]map[string]any, error) {
	cfg := byteplus.NewConfig().WithAkSk(c.BillingAccessKeyID, c.BillingAccessKeySecret).WithRegion("ap-singapore-1").WithEndpoint("https://open.byteplusapi.com").WithHTTPClient(httpClient)
	sess, err := session.NewSession(cfg)
	if err != nil {
		return nil, err
	}
	client := bytebilling.New(sess)
	return bytePlusBillSnapshot(month, func(input map[string]interface{}) (*map[string]interface{}, error) {
		return client.ListBillDetailCommonWithContext(ctx, &input)
	})
}

// BytePlus can omit usage rows from an unfiltered detail list, even while Total
// counts them. Discover products first and request each product independently.
func bytePlusBillSnapshot(month string, fetch func(map[string]interface{}) (*map[string]interface{}, error)) ([]map[string]any, error) {
	read := func(product string, summary bool) ([]map[string]any, error) {
		result := []map[string]any{}
		expectedTotal := -1
		for page := 0; page < 100; page++ {
			input := map[string]interface{}{"BillPeriod": month, "Limit": 100, "Offset": page * 100, "GroupPeriod": 2, "GroupTerm": 0, "IgnoreZero": 0, "NeedRecordNum": 1}
			if summary {
				input["GroupPeriod"], input["GroupTerm"] = 0, 2
			}
			if product != "" {
				input["Product"] = []string{product}
			}
			response, err := fetch(input)
			if err != nil {
				return nil, err
			}
			if response == nil {
				return nil, errors.New("BytePlus returned no bill snapshot")
			}
			body, ok := (*response)["Result"].(map[string]interface{})
			if !ok {
				return nil, errors.New("BytePlus returned invalid bill result")
			}
			total, err := strconv.Atoi(fmt.Sprint(body["Total"]))
			if err != nil || total < 0 {
				return nil, errors.New("BytePlus returned invalid bill count")
			}
			if expectedTotal >= 0 && total != expectedTotal {
				return nil, errors.New("BytePlus bill changed during pagination; retry next sync")
			}
			expectedTotal = total
			rows, ok := body["List"].([]interface{})
			if !ok && !(body["List"] == nil && total == 0) {
				return nil, errors.New("BytePlus returned invalid bill details")
			}
			for _, item := range rows {
				row, ok := item.(map[string]interface{})
				if !ok {
					return nil, errors.New("invalid bill row")
				}
				result = append(result, row)
			}
			if len(result) > total {
				return nil, errors.New("BytePlus bill exceeds reported count")
			}
			if len(result) == total {
				return result, nil
			}
			if len(rows) != 100 {
				return nil, errors.New("BytePlus returned an incomplete bill snapshot")
			}
		}
		return nil, errors.New("billing snapshot exceeds the page limit")
	}
	summaries, err := read("", true)
	if err != nil {
		return nil, err
	}
	products := map[string]bool{}
	for _, row := range summaries {
		product, ok := row["Product"].(string)
		if !ok || strings.TrimSpace(product) == "" {
			return nil, errors.New("BytePlus bill summary has no product")
		}
		products[product] = true
	}
	names := make([]string, 0, len(products))
	for product := range products {
		names = append(names, product)
	}
	sort.Strings(names)
	result := []map[string]any{}
	for _, product := range names {
		rows, err := read(product, false)
		if err != nil {
			return nil, err
		}
		result = append(result, rows...)
	}
	return result, nil
}

func normalizeBill(month string, row map[string]any) (core.BillRecord, error) {
	value := func(key string) string {
		if row[key] == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(row[key]))
	}
	bill := core.BillRecord{Currency: value("Currency")}
	var paid, unpaid string

	bill.ID = value("BillDetailId")
	bill.InstanceID = value("InstanceNo")
	paid = value("PaidAmount")
	unpaid = value("UnpaidAmount")

	if bill.ID == "" {
		return bill, errors.New("provider bill has no stable detail ID")
	}
	if bill.Currency == "USD" && paid != "" && unpaid != "" {
		amount, err := money.Parse(paid)
		remaining, remErr := money.Parse(unpaid)
		if err == nil && remErr == nil && remaining == 0 && amount >= 0 {
			bill.PaidUSD = &amount
			bill.Settled = true
		}
	}
	// Seedream bills report gross usage and package deductions separately. The
	// displayed OriginalBillAmount is already zero for fully covered usage.
	category := strings.ToLower(value("BillCategory"))
	if value("Product") == "Smart_Drawing_T2I" && (category == "consume-use" || category == "consumption-usage") {
		bill.Model = value("ExpandField")
		location := time.FixedZone("BytePlus billing UTC+8", 8*60*60)
		start, startErr := time.ParseInLocation("2006-01-02 15:04:05", value("ExpenseBeginTime"), location)
		end, endErr := time.ParseInLocation("2006-01-02 15:04:05", value("ExpenseEndTime"), location)
		units, unitsOK := wholeBillUnits(value("Count"))
		covered, coveredOK := wholeBillUnits(value("DeductionCount"))
		price, priceErr := money.Parse(value("Price"))
		element := strings.ToLower(value("Element"))
		if strings.HasSuffix(element, "-edit") {
			bill.ImageOperation = "edit"
		} else if strings.HasSuffix(element, "-generate") {
			bill.ImageOperation = "generate"
		}
		if startErr == nil && endErr == nil && end.After(start) && end.Sub(start) <= 24*time.Hour {
			start, end = start.UTC(), end.UTC()
			bill.UsageStart, bill.UsageEnd = &start, &end
		}
		if unitsOK && units > 0 && value("Unit") == "Piece" {
			bill.Units = &units
			if coveredOK && covered <= units {
				bill.PackageUnits = &covered
			}
			if priceErr == nil && price >= 0 && value("PriceUnit") == "Piece" && value("BillingFunction") == "Fixed Price" && bill.Currency == "USD" {
				amount, err := price.Multiply(units, 1)
				if err == nil {
					bill.TariffUSD = &amount
				}
			}
		}
	}
	// Verified Seedance 1.5 audio usage rows use thousands of already-billable
	// tokens. Normalize the counters to tokens, retaining the bill's own tariff.
	if value("Product") == "ModelArk_video_generation" &&
		(category == "consume-use" || category == "consumption-usage") &&
		value("ExpandField") == "seedance-1-5-pro" && value("Element") == "Seedance-1.5-pro-inference-audio" {
		bill.Model, bill.MediaType = value("ExpandField"), "video"
		audio := true
		bill.GenerateAudio = &audio
		location := time.FixedZone("BytePlus billing UTC+8", 8*60*60)
		start, startErr := time.ParseInLocation("2006-01-02 15:04:05", value("ExpenseBeginTime"), location)
		end, endErr := time.ParseInLocation("2006-01-02 15:04:05", value("ExpenseEndTime"), location)
		if startErr == nil && endErr == nil && end.After(start) && end.Sub(start) <= 24*time.Hour {
			start, end = start.UTC(), end.UTC()
			bill.UsageStart, bill.UsageEnd = &start, &end
		}
		// Decimal parsing avoids artifacts such as 38.8 * 1000 in float math.
		units, unitsErr := money.Parse(value("Count"))
		covered, coveredErr := money.Parse(value("DeductionCount"))
		price, priceErr := money.Parse(value("Price"))
		if unitsErr == nil && units > 0 && units%1000 == 0 && value("Unit") == "K tokens" {
			tokens := float64(units / 1000)
			bill.Units = &tokens
			if coveredErr == nil && covered >= 0 && covered <= units && covered%1000 == 0 {
				packageTokens := float64(covered / 1000)
				bill.PackageUnits = &packageTokens
			}
			if priceErr == nil && price >= 0 && value("PriceUnit") == "K tokens" && value("BillingFunction") == "Fixed Price" && bill.Currency == "USD" {
				amount, err := price.Multiply(tokens, 1000)
				if err == nil {
					bill.TariffUSD = &amount
				}
			}
		}
	}
	raw, err := json.Marshal(row)
	bill.Raw = raw
	return bill, err
}

// Check the original number before accepting its float64 representation: a
// fractional count can round to a whole image, and a large integer can change.
func wholeBillUnits(raw string) (float64, bool) {
	units, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(units) || math.IsInf(units, 0) || units < 0 {
		return 0, false
	}
	exact, ok := new(big.Rat).SetString(raw)
	return units, ok && exact.IsInt() && exact.Cmp(new(big.Rat).SetFloat64(units)) == 0
}
