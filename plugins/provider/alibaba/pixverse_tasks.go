package alibaba

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

// Every exact model has independent explicit Singapore rows on model-pricing.
// Integer millionths avoid float conversion and preserve older reviewed tariffs.
var pixverseRates = map[string]map[bool]map[string]money.USD{
	pixverseT2V: {
		false: {"360p": 23000, "540p": 32000, "720p": 40000, "1080p": 80000},
		true:  {"360p": 32000, "540p": 40000, "720p": 54000, "1080p": 103000},
	},
	pixverseI2V: {
		false: {"360p": 23000, "540p": 32000, "720p": 40000, "1080p": 80000},
		true:  {"360p": 32000, "540p": 40000, "720p": 54000, "1080p": 103000},
	},
	pixverseKF2V: {
		false: {"360p": 23000, "540p": 32000, "720p": 40000, "1080p": 80000},
		true:  {"360p": 32000, "540p": 40000, "720p": 54000, "1080p": 103000},
	},
	pixverseR2V: {
		false: {"360p": 23000, "540p": 32000, "720p": 40000, "1080p": 80000},
		true:  {"360p": 32000, "540p": 40000, "720p": 54000, "1080p": 103000},
	},
}

func quotePixversePublic(endpoint string, model core.MediaModel, r core.MediaRequest, now time.Time) (core.TaskCost, error) {
	if !pixverseEndpoint(endpoint) || model.ID != r.Model || model.MediaType != "video" {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	if _, err := pixversePayload(r); err != nil {
		return core.TaskCost{}, err
	}
	audio := r.Options.GenerateAudio != nil && *r.Options.GenerateAudio
	rate, ok := pixverseRates[r.Model][audio][r.Options.Resolution]
	if !ok {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	amount, err := rate.Multiply(float64(r.Options.Duration), 1)
	if err != nil {
		return core.TaskCost{}, err
	}
	billInput := false
	return core.TaskCost{
		ExpectedUSD: amount, EstimatedUnits: float64(r.Options.Duration), Status: "estimated", QuotedAt: now,
		Pricing: core.PricingSnapshot{
			Currency: "USD", Unit: "second", Per: 1, Price: rate, Region: "Singapore",
			Source: alibabaPriceSource, Version: "pixverse-v6-2026-11-08",
			CheckedAt: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), BillInputVideo: &billInput,
		},
	}, nil
}

func (a *Adapter) generatePixverse(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.validatePixverseConnection(); err != nil {
		return core.ProviderTask{}, err
	}
	payload, err := pixversePayload(r)
	if err != nil {
		return core.ProviderTask{}, err
	}
	// Exactly one audited asynchronous POST. The native ID is persisted unchanged
	// by the existing worker and remains available to billing reconciliation.
	// A native ID is an accepted receipt even if a creation status/code is
	// unfamiliar or contradictory. Persist it and reconcile by strict polling.
	task, err := a.createAsync(ctx, videoPath, payload)
	if err != nil {
		return core.ProviderTask{}, err
	}
	// Creation/provisional usage cannot establish output billing dimensions.
	task.Usage = nil
	return task, nil
}

func (a *Adapter) getPixverseTask(ctx context.Context, id string, request core.MediaRequest) (core.ProviderResult, error) {
	// Account opt-in can be withdrawn after acceptance. Poll the original job;
	// never activate, replace an endpoint, or resubmit generation during recovery.
	if !pixverseEndpoint(a.baseURL) || id == "" {
		return core.ProviderResult{}, failurePolicy.InvalidConnection()
	}
	var response dashResponse
	if err := a.do(ctx, a.baseURL, a.apiKey, http.MethodGet, "/tasks/"+url.PathEscape(id), nil, false, &response); err != nil {
		return core.ProviderResult{}, err
	}
	if response.Code != "" {
		return core.ProviderResult{}, failurePolicy.NativeFailure(providerkit.FailurePoll, response.Code)
	}
	if response.Output.TaskID != "" && response.Output.TaskID != id {
		return core.ProviderResult{}, failurePolicy.InvalidResponse(providerkit.FailurePoll, providerkit.ResponseTaskID)
	}
	if response.Output.Code != "" && response.Output.TaskStatus != "FAILED" && response.Output.TaskStatus != "CANCELED" {
		return core.ProviderResult{}, failurePolicy.NativeFailure(providerkit.FailurePoll, response.Output.Code)
	}
	switch response.Output.TaskStatus {
	case "PENDING", "RUNNING":
		if response.Output.TaskID == "" {
			return core.ProviderResult{}, failurePolicy.InvalidResponse(providerkit.FailurePoll, providerkit.ResponseTaskID)
		}
		status := core.ProviderQueued
		if response.Output.TaskStatus == "RUNNING" {
			status = core.ProviderRunning
		}
		// Provisional/non-success counters must not become billable output.
		return core.ProviderResult{Status: status}, nil
	case "SUCCEEDED":
		if response.Output.TaskID == "" || !pixverseMediaURL(response.Output.VideoURL) {
			return core.ProviderResult{}, failurePolicy.InvalidResponse(providerkit.FailurePoll, providerkit.ResponseMediaURL)
		}
		// Do not use resultURL(): its image/audio fallbacks are not this contract.
		return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: response.Output.VideoURL, Usage: pixverseUsage(response.Usage, request)}, nil
	case "FAILED", "CANCELED":
		code := response.Output.Code
		if code == "" {
			code = response.Code
		}
		return core.ProviderResult{Status: core.ProviderFailed, Error: failurePolicy.TaskFailure(code, response.Output.TaskStatus).Error()}, nil
	case "UNKNOWN":
		// Queries expire after 24h. UNKNOWN does not establish zero charge and
		// must not enter the generic FailureIsFree terminal path.
		return core.ProviderResult{}, fmt.Errorf("PixVerse task is unknown or outside its 24-hour query window; reconcile the original task without resubmission")
	default:
		return core.ProviderResult{}, failurePolicy.InvalidResponse(providerkit.FailurePoll, providerkit.ResponseStatus)
	}
}

func pixverseInteger(v any, min, max float64) bool {
	n, ok := v.(float64) // Values decoded by the bounded JSON response reader.
	return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n && n >= min && n <= max
}

func pixverseUsage(raw map[string]any, request core.MediaRequest) *core.ProviderUsage {
	encoded, _ := json.Marshal(raw)
	guarded := &core.ProviderUsage{Raw: encoded, CostStatus: "unavailable"}
	if !pixverseInteger(raw["duration"], 1, 15) || !pixverseInteger(raw["video_count"], 1, 1) ||
		!pixverseInteger(raw["fps"], 1, 2147483647) {
		return guarded
	}
	audio, ok := raw["audio"].(bool)
	if !ok || audio != (request.Options.GenerateAudio != nil && *request.Options.GenerateAudio) {
		return guarded
	}
	sr, ok := raw["SR"].(string)
	if !ok {
		return guarded
	}
	tier := strings.ToLower(sr)
	if !strings.HasSuffix(tier, "p") {
		tier += "p"
	}
	if tier != request.Options.Resolution || pixverseRates[request.Model][audio][tier] <= 0 {
		return guarded
	}
	size, ok := raw["size"].(string)
	if !ok {
		return guarded
	}
	parts := strings.Split(size, "*")
	if len(parts) != 2 {
		return guarded
	}
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 10000 {
			return guarded
		}
	}
	if request.Model == pixverseT2V || request.Model == pixverseR2V {
		expected, ok := pixverseSize(tier, request.Options.Ratio)
		if !ok || size != expected {
			return guarded
		}
	}
	if request.Model == pixverseT2V || request.Model == pixverseI2V {
		shot, present := raw["shot_type"]
		if present {
			expected := "single"
			if s, ok := request.Options.Parameters["shot_type"].(string); ok {
				expected = s
			}
			if shot != expected {
				return guarded
			}
		}
	}
	// Actual I2V/KF2V dimensions can differ from T2V size tables. SR, audio and
	// successful output duration establish the price dimensions, not min(w,h).
	// Never feed unvalidated aliases/nested counters to the generic normalizer.
	// Raw evidence is retained; only documented top-level duration is billable.
	return &core.ProviderUsage{
		Raw: encoded, CostStatus: "metered",
		Metrics: []core.UsageMetric{{Name: "duration", Unit: "second", Quantity: raw["duration"].(float64)}},
	}
}
