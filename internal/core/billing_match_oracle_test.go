package core

import (
	"strings"
	"time"
)

// Frozen test-only oracle from main 6df4bf8. Keep independent of the optimized matcher.
// MatchUsageBills correlates complete, single-generation image/video billing windows.
// InstanceNo identifies the provider's model service, so it is never compared to
// an inference request ID. Ambiguous windows (including uncertain failed calls)
// retain their reported usage cost. The match method is exposed to API clients.
func originalMatchUsageBills(tasks []Task, bills []BillRecord, fingerprint string) map[string]BillRecord {
	candidates := map[string][]BillRecord{}
	touches := map[string]int{}
	for _, bill := range bills {
		if bill.Model == "" || bill.UsageStart == nil || bill.UsageEnd == nil || bill.Units == nil {
			continue
		}
		var eligible []Task
		for _, task := range tasks {
			if task.Cost == nil || !originalBillingModelMatches(task.Model, bill.Model) {
				continue
			}
			mediaType := bill.MediaType
			if mediaType == "" {
				// Legacy image snapshots do not carry an explicit media type.
				mediaType = "image"
			}
			if task.MediaType != mediaType {
				continue
			}
			if bill.GenerateAudio != nil && (task.Options.GenerateAudio == nil || *task.Options.GenerateAudio != *bill.GenerateAudio) {
				continue
			}
			if task.Cost.ActualSource == "provider_failed_no_charge" {
				continue
			}
			hadImageInput := task.HadImageInput || len(task.Options.ImageURLs) > 0
			if bill.ImageOperation == "edit" && !hadImageInput {
				continue
			}
			if bill.ImageOperation == "generate" && hadImageInput {
				continue
			}
			start, end := task.CreatedAt, task.UpdatedAt
			if task.Cost.SubmittedAt != nil {
				start = *task.Cost.SubmittedAt
			}
			if task.Cost.UsageRecordedAt != nil {
				end = *task.Cost.UsageRecordedAt
			}
			if task.Status == TaskQueued || task.Status == TaskRunning {
				end = time.Now()
			}
			if !start.Before(*bill.UsageEnd) || end.Before(*bill.UsageStart) {
				continue
			}
			eligible = append(eligible, task)
		}
		for _, task := range eligible {
			touches[task.ID]++
		}
		// Count all credentials under the owner before checking the fingerprint; a
		// shared provider account must not assign the same bill to two Gateway keys.
		if len(eligible) != 1 {
			continue
		}
		task := eligible[0]
		if task.Cost.Pricing.Unit == "request" || task.ProviderIdentity != fingerprint || task.Status != TaskSucceeded || task.Cost.BilledUnits == nil || *task.Cost.BilledUnits != *bill.Units || task.Cost.Pricing.FixedUSD != 0 {
			continue
		}
		candidates[task.ID] = append(candidates[task.ID], bill)
	}
	result := map[string]BillRecord{}
	for id, rows := range candidates {
		if len(rows) != 1 || touches[id] != 1 {
			continue
		}
		bill := rows[0]
		if bill.Settled && bill.PaidUSD != nil && bill.TariffUSD != nil {
			result[id] = bill
		}
	}
	return result
}

func originalBillingModelMatches(taskModel, billModel string) bool {
	// Match an exact model or its documented dated version, never arbitrary
	// prefixes of a different model family.
	if taskModel == billModel {
		return true
	}
	suffix := strings.TrimPrefix(taskModel, billModel+"-")
	if suffix == taskModel || len(suffix) != 6 {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
