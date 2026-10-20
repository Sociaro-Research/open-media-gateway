package core

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// ReportMetrics uses customer charges, never provider prices. Missing costs and
// token counters remain explicit; an absent measurement is not a zero.
type ReportMetrics struct {
	Profit                   money.USD `json:"profit_usd"`
	RecognizedProfitRequests int64     `json:"recognized_profit_requests"`
	PendingProfitRequests    int64     `json:"pending_profit_requests"`
	Requests                 int64     `json:"requests"`
	Successful               int64     `json:"successful"`
	Failed                   int64     `json:"failed"`
	Spend                    money.USD `json:"spend_usd"`
	PricedRequests           int64     `json:"priced_requests"`
	Tokens                   float64   `json:"tokens"`
	TokenRequests            int64     `json:"token_requests"`
}
type ReportGroup struct {
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	ReportMetrics
}
type ReportPeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
	ReportMetrics
}
type ReportSummary struct {
	ReportMetrics
	AverageCost    *money.USD     `json:"average_cost_usd"`
	Interval       string         `json:"interval"`
	Periods        []ReportPeriod `json:"periods"`
	PublicModels   []ReportGroup  `json:"public_models"`
	ProviderModels []ReportGroup  `json:"provider_models"`
	Providers      []ReportGroup  `json:"providers"`
}
type ReportWalker interface {
	WalkCostReport(context.Context, ReportFilter, func(CostReportRow) error) error
}

var ErrReportInterval = errors.New("use day, week or month; select a coarser interval for more than 10000 periods")

func reportTokens(usage *ProviderUsage) (float64, bool) {
	if usage == nil {
		return 0, false
	}
	values := map[string]float64{}
	for _, m := range usage.Metrics {
		if m.Unit == "token" && m.Quantity >= 0 {
			values[m.Name] = m.Quantity
		}
	}
	if n, ok := values["total_tokens"]; ok {
		return n, true
	}
	// Completion/output are aliases; audio/detail counters can be subsets.
	input, hasInput := values["input_tokens"]
	output, hasOutput := values["output_tokens"]
	if !hasOutput {
		output, hasOutput = values["completion_tokens"]
	}
	if hasInput || hasOutput {
		return input + output, true
	}
	if n, ok := values["audio_tokens"]; ok {
		return n, true
	}
	return 0, false
}
func (m *ReportMetrics) add(row CostReportRow) {
	m.Requests++
	if row.ProfitUSD != nil && row.ProfitStatus == "recognized" {
		m.Profit += *row.ProfitUSD
		m.RecognizedProfitRequests++
	}
	if row.ProfitStatus == "awaiting_billing" || row.ProfitStatus == "awaiting_cost" {
		m.PendingProfitRequests++
	}
	if row.Status == string(TaskSucceeded) {
		m.Successful++
	}
	if row.Status == string(TaskFailed) {
		m.Failed++
	}
	if row.ConsumedUSD != nil {
		m.Spend += *row.ConsumedUSD
		m.PricedRequests++
	}
	if n, ok := reportTokens(row.Usage); ok {
		m.Tokens += n
		m.TokenRequests++
	}
}

// SummarizeCostReport streams all filtered records through the same scoped
// reader as CSV export, retaining only aggregate groups, not individual rows.
func SummarizeCostReport(ctx context.Context, reader ReportWalker, f ReportFilter, interval string) (ReportSummary, error) {
	out := ReportSummary{Interval: interval, Periods: []ReportPeriod{}, PublicModels: []ReportGroup{}, ProviderModels: []ReportGroup{}, Providers: []ReportGroup{}}
	if interval != "day" && interval != "week" && interval != "month" {
		return out, ErrReportInterval
	}
	starts := []time.Time{}
	shortMonth := interval == "month" && !f.Until.After(f.From.AddDate(0, 1, 0))
	for start := f.From; start.Before(f.Until); {
		if len(starts) >= 10000 {
			return out, ErrReportInterval
		}
		next := start.AddDate(0, 0, 1)
		if interval == "week" {
			next = start.AddDate(0, 0, 7-(int(start.Weekday())+6)%7)
		}
		if interval == "month" {
			next = time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, start.Location())
			if shortMonth {
				next = f.Until
			}
		}
		if next.After(f.Until) {
			next = f.Until
		}
		starts = append(starts, start)
		out.Periods = append(out.Periods, ReportPeriod{From: start.Format("2006-01-02"), To: next.AddDate(0, 0, -1).Format("2006-01-02")})
		start = next
	}
	public, models, providers := map[string]*ReportGroup{}, map[string]*ReportGroup{}, map[string]*ReportGroup{}
	add := func(groups map[string]*ReportGroup, key, name, provider string, row CostReportRow) {
		if groups[key] == nil {
			groups[key] = &ReportGroup{Name: name, Provider: provider}
		}
		groups[key].add(row)
	}
	f.Limit = 0
	f.Offset = 0
	err := reader.WalkCostReport(ctx, f, func(row CostReportRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row.CreatedAt.Before(f.From) || !row.CreatedAt.Before(f.Until) {
			return nil
		}
		out.add(row)
		index := sort.Search(len(starts), func(i int) bool { return starts[i].After(row.CreatedAt) }) - 1
		if index >= 0 {
			out.Periods[index].add(row)
		}
		name := row.PublicModel
		if name == "" {
			name = row.Provider + "/" + row.Model
		}
		add(public, name, name, "", row)
		add(models, row.Provider+"\x00"+row.Model, row.Model, row.Provider, row)
		providerName := row.ProviderName
		if providerName == "" {
			providerName = row.Provider
		}
		add(providers, row.Provider, providerName, "", row)
		return nil
	})
	if err != nil {
		return ReportSummary{}, err
	}
	collect := func(groups map[string]*ReportGroup) []ReportGroup {
		result := make([]ReportGroup, 0, len(groups))
		for _, g := range groups {
			result = append(result, *g)
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].Spend != result[j].Spend {
				return result[i].Spend > result[j].Spend
			}
			if result[i].Name != result[j].Name {
				return result[i].Name < result[j].Name
			}
			return result[i].Provider < result[j].Provider
		})
		return result
	}
	out.PublicModels = collect(public)
	out.ProviderModels = collect(models)
	out.Providers = collect(providers)
	if out.PricedRequests > 0 {
		avg := out.Spend / money.USD(out.PricedRequests)
		out.AverageCost = &avg
	}
	return out, nil
}
