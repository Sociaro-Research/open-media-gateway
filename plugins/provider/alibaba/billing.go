package alibaba

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	bss "github.com/alibabacloud-go/bssopenapi-20171214/v6/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
	"net/http"
	"strings"
)

type Credentials = providerkit.Credentials

func fetchBills(ctx context.Context, c Credentials, month string, httpClient *http.Client) ([]map[string]any, error) {
	client, err := bss.NewClient(&openapi.Config{AccessKeyId: dara.String(c.BillingAccessKeyID), AccessKeySecret: dara.String(c.BillingAccessKeySecret), Endpoint: dara.String("business.ap-southeast-1.aliyuncs.com"), ConnectTimeout: dara.Int(10000), ReadTimeout: dara.Int(15000)})
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	next := ""
	for page := 0; page < 100; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The generated response declares money as float32. Request the raw JSON
		// through the official signed SDK and decode numbers without that precision loss.
		query := map[string]interface{}{"BillingCycle": month, "Granularity": "MONTHLY", "IsBillingItem": true, "IsHideZeroCharge": false, "MaxResults": 300}
		if next != "" {
			query["NextToken"] = next
		}
		response, err := client.CallApi(&openapiutil.Params{Action: dara.String("DescribeInstanceBill"), Version: dara.String("2017-12-14"), Protocol: dara.String("HTTPS"), Pathname: dara.String("/"), Method: dara.String("POST"), AuthType: dara.String("AK"), Style: dara.String("RPC"), ReqBodyType: dara.String("formData"), BodyType: dara.String("string")}, &openapiutil.OpenApiRequest{Query: openapiutil.Query(query)}, &dara.RuntimeOptions{})
		if err != nil {
			return nil, err
		}
		raw, ok := response["body"].(string)
		if !ok {
			return nil, errors.New("Alibaba returned an invalid bill body")
		}
		var body struct {
			Success bool
			Data    struct {
				Items     []map[string]any
				NextToken string
			}
		}
		decoder := json.NewDecoder(bytes.NewBufferString(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			return nil, err
		}
		if !body.Success {
			return nil, errors.New("Alibaba returned no successful bill snapshot")
		}
		result = append(result, body.Data.Items...)
		token := body.Data.NextToken
		if token == "" {
			return result, nil
		}
		if token == next {
			return nil, errors.New("Alibaba repeated a billing page")
		}
		next = token
	}
	return nil, errors.New("billing snapshot exceeds the page limit")
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

	bill.InstanceID = value("InstanceID")
	paid = value("PaymentAmount")
	unpaid = value("OutstandingAmount")
	// BSS monthly item rows have no detail ID; hash their documented dimensions,
	// excluding mutable monetary amounts so invoice revisions replace earlier data.
	dimensions := strings.Join([]string{month, bill.InstanceID, value("BillingItemCode"), value("ProductCode"), value("SubscriptionType"), value("BillingType")}, "\x00")
	hash := sha256.Sum256([]byte(dimensions))
	bill.ID = hex.EncodeToString(hash[:])
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
	raw, err := json.Marshal(row)
	bill.Raw = raw
	return bill, err
}
