package core

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// MatchUsageBills correlates complete, single-generation image/video billing windows.
// InstanceNo identifies the provider's model service, so it is never compared to
// an inference request ID. Ambiguous windows (including uncertain failed calls)
// retain their reported usage cost. The match method is exposed to API clients.
func MatchUsageBills(tasks []Task, bills []BillRecord, fingerprint string) map[string]BillRecord {
	candidates := map[string][]BillRecord{}
	touches := map[string]int{}
	var windows []billingTaskWindow
	indexed := false
	for _, bill := range bills {
		if bill.Model == "" || bill.UsageStart == nil || bill.UsageEnd == nil || bill.Units == nil {
			continue
		}
		// Invalid-only responses need no task scan or index setup.
		if !indexed {
			windows = billingTaskWindows(tasks, bills)
			indexed = true
		}
		var eligible *Task
		eligibleCount := 0
		limit := len(tasks)
		if windows != nil {
			limit = sort.Search(len(windows), func(i int) bool { return !windows[i].start.Before(*bill.UsageEnd) })
		}
		for i := 0; i < limit; i++ {
			task := &tasks[i]
			if windows != nil {
				window := windows[limit-1-i]
				if !window.openPrefix && window.maxEnd.Before(*bill.UsageStart) {
					break
				}
				task = &tasks[window.row]
			}
			if task.Cost == nil || !billingModelMatches(task.Model, bill.Model) {
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
			eligible, eligibleCount = task, eligibleCount+1
			touches[task.ID]++
		}
		// Count all credentials under the owner before checking the fingerprint; a
		// shared provider account must not assign the same bill to two Gateway keys.
		if eligibleCount != 1 {
			continue
		}
		task := eligible
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

// Each prefix records its latest end and whether it contains an open
// task. This conservatively prunes time-disjoint rows; all other eligibility
// checks, including the live time.Now predicate for pending tasks, stay above.
type billingTaskWindow struct {
	row                int
	start, end, maxEnd time.Time
	open, openPrefix   bool
}

func billingTaskWindows(tasks []Task, bills []BillRecord) []billingTaskWindow {
	// Avoid setup on small inputs. These paths still use the same predicate.
	if len(tasks) < 128 || len(bills) < 8 {
		return nil
	}
	var first, last time.Time
	usable := 0
	models := map[string]bool{}
	for _, bill := range bills {
		if bill.Model == "" || bill.UsageStart == nil || bill.UsageEnd == nil || bill.Units == nil {
			continue
		}
		if usable == 0 || bill.UsageStart.Before(first) {
			first = *bill.UsageStart
		}
		if usable == 0 || bill.UsageEnd.After(last) {
			last = *bill.UsageEnd
		}
		usable++
		models[bill.Model] = true
	}
	if usable < 8 {
		return nil
	}
	// Allocate only rows overlapping the overall bill envelope, rather than all
	// retained history. Two linear passes bound allocation without growing copies.
	window := func(task *Task) (billingTaskWindow, bool) {
		if task.Cost == nil {
			return billingTaskWindow{}, false
		}
		// Histories include every model for the owner/provider. Only exact models
		// or their six-digit versions can satisfy the unchanged model predicate.
		if !models[task.Model] {
			if len(task.Model) < 7 {
				return billingTaskWindow{}, false
			}
			base := task.Model[:len(task.Model)-7]
			if !models[base] || !billingModelMatches(task.Model, base) {
				return billingTaskWindow{}, false
			}
		}
		w := billingTaskWindow{start: task.CreatedAt, end: task.UpdatedAt, open: task.Status == TaskQueued || task.Status == TaskRunning}
		if task.Cost.SubmittedAt != nil {
			w.start = *task.Cost.SubmittedAt
		}
		if task.Cost.UsageRecordedAt != nil {
			w.end = *task.Cost.UsageRecordedAt
		}
		return w, w.start.Before(last) && (w.open || !w.end.Before(first))
	}
	count := 0
	for i := range tasks {
		if _, ok := window(&tasks[i]); ok {
			count++
		}
	}
	windows := make([]billingTaskWindow, 0, count)
	for i := range tasks {
		if w, ok := window(&tasks[i]); ok {
			w.row = i
			windows = append(windows, w)
		}
	}
	slices.SortFunc(windows, func(a, b billingTaskWindow) int { return a.start.Compare(b.start) })
	for i := range windows {
		w := &windows[i]
		w.maxEnd, w.openPrefix = w.end, w.open
		if i > 0 {
			previous := windows[i-1]
			if previous.maxEnd.After(w.maxEnd) {
				w.maxEnd = previous.maxEnd
			}
			w.openPrefix = w.openPrefix || previous.openPrefix
		}
	}
	return windows
}

func billingModelMatches(taskModel, billModel string) bool {
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
