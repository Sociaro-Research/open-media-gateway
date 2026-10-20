package core

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func BenchmarkMatchUsageBills(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		for _, billCount := range []int{100, 1000} {
			b.Run(fmt.Sprintf("tasks-%d/bills-%d", n, billCount), func(b *testing.B) {
				task, prototype := imageBillFixture()
				tasks := make([]Task, n)
				bills := make([]BillRecord, billCount)
				for i := range tasks {
					copy := task
					copy.ID = fmt.Sprint(i)
					copy.CreatedAt = task.CreatedAt.Add(time.Duration(i) * 10 * time.Minute)
					copy.UpdatedAt = copy.CreatedAt.Add(time.Minute)
					tasks[i] = copy
				}
				for i := range bills {
					copy := prototype
					copy.ID = fmt.Sprint(i)
					start := tasks[i%n].CreatedAt.Add(-time.Minute)
					end := start.Add(5 * time.Minute)
					copy.UsageStart, copy.UsageEnd = &start, &end
					bills[i] = copy
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if matches := MatchUsageBills(tasks, bills, "credential"); len(matches) != min(n, billCount) {
						b.Fatal("matching semantics changed", len(matches))
					}
				}
			})
		}
	}
}

// These shapes guard the index's setup cost and its unavoidable overlap scans.
// Fixture setup is excluded; MatchUsageBills must build its index on every call.
func BenchmarkMatchUsageBillsShapes(b *testing.B) {
	for _, shape := range []string{"small", "dense-overlap", "many-pending", "invalid-bills"} {
		b.Run(shape, func(b *testing.B) {
			task, bill := imageBillFixture()
			n, billCount, want := 1000, 100, 0
			if shape == "small" {
				n, billCount, want = 8, 4, 4
			}
			if shape == "many-pending" {
				n = 10000
			}
			if shape == "invalid-bills" {
				n, bill.Units = 10000, nil
			}
			tasks, bills := make([]Task, n), make([]BillRecord, billCount)
			for i := range tasks {
				tasks[i] = task
				tasks[i].ID = fmt.Sprint(i)
				if shape == "small" {
					tasks[i].CreatedAt = task.CreatedAt.Add(time.Duration(i) * 10 * time.Minute)
					tasks[i].UpdatedAt = tasks[i].CreatedAt.Add(time.Minute)
				}
				if shape == "many-pending" {
					tasks[i].Status = TaskRunning
				}
			}
			for i := range bills {
				bills[i] = bill
				bills[i].ID = fmt.Sprint(i)
				if shape == "small" {
					start := tasks[i].CreatedAt.Add(-time.Minute)
					end := start.Add(5 * time.Minute)
					bills[i].UsageStart, bills[i].UsageEnd = &start, &end
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := MatchUsageBills(tasks, bills, "credential"); len(got) != want {
					b.Fatal("matching semantics changed", len(got))
				}
			}
		})
	}
}

// Owner/provider history includes unrelated models, and provider rows may be
// incomplete. Raw row count must not justify sorting a whole day of history.
func BenchmarkMatchUsageBillsBroadEnvelope(b *testing.B) {
	for _, shape := range []string{"single-usable", "single-usable-padded", "eight-usable"} {
		b.Run(shape, func(b *testing.B) {
			task, bill := imageBillFixture()
			n := 100000
			tasks := make([]Task, n)
			rng := rand.New(rand.NewSource(104))
			order := rng.Perm(n)
			for i := range tasks {
				tasks[i] = task
				tasks[i].ID, tasks[i].Model = fmt.Sprint(i), "unrelated-model-123456"
				tasks[i].CreatedAt = bill.UsageStart.Add(-24*time.Hour + time.Duration(order[i])*time.Second/2)
				tasks[i].UpdatedAt = bill.UsageEnd.Add(time.Hour)
			}
			bills := []BillRecord{bill}
			if shape != "single-usable" {
				for i := 1; i < 8; i++ {
					row := bill
					row.ID = fmt.Sprint(i)
					if shape == "single-usable-padded" {
						row.Units = nil
					}
					bills = append(bills, row)
				}
			}
			want := originalMatchUsageBills(tasks, bills, "credential")
			got := MatchUsageBills(tasks, bills, "credential")
			if len(want) != 0 || !reflect.DeepEqual(got, want) {
				b.Fatal("full matching records differ", got, want)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := MatchUsageBills(tasks, bills, "credential"); !reflect.DeepEqual(got, want) {
					b.Fatal("full matching records differ", got, want)
				}
			}
		})
	}
}

func BenchmarkReportSummary(b *testing.B) {
	for _, n := range []int{10000, 100000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rows := &summaryRows{rows: make([]CostReportRow, n)}
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := range rows.rows {
				rows.rows[i] = CostReportRow{CreatedAt: start.Add(time.Duration(i%365) * 24 * time.Hour), Provider: "mock", Model: fmt.Sprint(i % 100), Status: "succeeded"}
			}
			filter := ReportFilter{From: start, Until: start.AddDate(1, 0, 0)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := SummarizeCostReport(context.Background(), rows, filter, "month")
				if err != nil || result.Requests != int64(n) {
					b.Fatal(result.Requests, err)
				}
			}
		})
	}
}
