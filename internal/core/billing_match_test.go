package core

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"testing"
	"time"
)

func imageBillFixture() (Task, BillRecord) {
	start := time.Date(2026, 9, 6, 12, 50, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	unit, zero, gross := 1.0, money.USD(0), money.USD(40000)
	task := Task{ID: "task", Provider: "bytedance", ProviderIdentity: "credential", MediaType: "image", Model: "seedream-4-5-251128", Status: TaskSucceeded, CreatedAt: start.Add(time.Minute), UpdatedAt: start.Add(2 * time.Minute), Options: MediaOptions{ImageURLs: []string{"reference"}}, Cost: &TaskCost{BilledUnits: &unit, ExpectedUSD: gross}}
	bill := BillRecord{ID: "bill", Model: "seedream-4-5", InstanceID: "model-service", UsageStart: &start, UsageEnd: &end, Units: &unit, PackageUnits: &unit, TariffUSD: &gross, PaidUSD: &zero, Settled: true, ImageOperation: "edit"}
	return task, bill
}

func TestUniqueImageWindowMatchesPackageBill(t *testing.T) {
	task, bill := imageBillFixture()
	got := MatchUsageBills([]Task{task}, []BillRecord{bill}, "credential")
	if len(got) != 1 || *got[task.ID].PaidUSD != 0 || *got[task.ID].TariffUSD != 40000 {
		t.Fatal(got)
	}
	// Later reconciliation must use the immutable usage timestamp.
	used := task.UpdatedAt
	task.Cost.UsageRecordedAt = &used
	task.UpdatedAt = task.UpdatedAt.Add(24 * time.Hour)
	if len(MatchUsageBills([]Task{task}, []BillRecord{bill}, "credential")) != 1 {
		t.Fatal("lost stable match")
	}
}

func TestManualTariffRetainsBillingWindowAmbiguity(t *testing.T) {
	task, bill := imageBillFixture()
	manual := task
	manual.ID = "manual"
	manual.Provider = "bytedance--independent"
	cost := *task.Cost
	cost.Pricing.Unit = "request"
	manual.Cost = &cost
	if len(MatchUsageBills([]Task{manual}, []BillRecord{bill}, "credential")) != 0 {
		t.Fatal("manual request tariff matched a usage invoice")
	}
	if len(MatchUsageBills([]Task{task, manual}, []BillRecord{bill}, "credential")) != 0 {
		t.Fatal("overlapping manual request was ignored when matching the invoice")
	}
}

func TestImageWindowRejectsAmbiguityAndWrongUsage(t *testing.T) {
	for _, mode := range []string{"two_tasks", "uncertain_failure", "other_credential", "other_version", "wrong_model", "count", "unpaid", "two_bills", "another_operation", "outside_window"} {
		t.Run(mode, func(t *testing.T) {
			task, bill := imageBillFixture()
			tasks := []Task{task}
			bills := []BillRecord{bill}
			switch mode {
			case "two_tasks", "uncertain_failure", "other_credential":
				other := task
				other.ID = "other"
				if mode == "uncertain_failure" {
					other.Status = TaskFailed
				}
				if mode == "other_credential" {
					other.ProviderIdentity = "different"
				}
				tasks = append(tasks, other)
			case "other_version":
				tasks[0].Model = "seedream-4-5-pro"
			case "wrong_model":
				bills[0].Model = "seedream-4"
			case "count":
				n := 2.0
				bills[0].Units = &n
			case "unpaid":
				bills[0].Settled = false
			case "two_bills":
				other := bill
				other.ID = "other"
				bills = append(bills, other)
			case "another_operation":
				bills[0].ImageOperation = "generate"
			case "outside_window":
				tasks[0].CreatedAt = tasks[0].CreatedAt.Add(time.Hour)
				tasks[0].UpdatedAt = tasks[0].UpdatedAt.Add(time.Hour)
			}
			if len(MatchUsageBills(tasks, bills, "credential")) != 0 {
				t.Fatal("ambiguous charge confirmed")
			}
		})
	}
}

func TestVideoWindowRequiresUniqueMatchingAudioAndTokens(t *testing.T) {
	task, bill := imageBillFixture()
	audio, units, gross := true, 38800.0, money.USD(93120)
	task.MediaType, task.Model = "video", "seedance-1-5-pro-251215"
	task.Options.GenerateAudio = &audio
	task.Cost.BilledUnits = &units
	bill.MediaType, bill.Model, bill.ImageOperation = "video", "seedance-1-5-pro", ""
	bill.GenerateAudio, bill.Units, bill.PackageUnits, bill.TariffUSD = &audio, &units, &units, &gross
	if len(MatchUsageBills([]Task{task}, []BillRecord{bill}, "credential")) != 1 {
		t.Fatal("matching video not reconciled")
	}
	for _, mode := range []string{"duplicate_task", "duplicate_bill", "wrong_tokens", "wrong_audio", "wrong_media", "wrong_credentials"} {
		t.Run(mode, func(t *testing.T) {
			candidate, row := task, bill
			cost := *task.Cost
			candidate.Cost = &cost
			tasks, bills := []Task{candidate}, []BillRecord{row}
			switch mode {
			case "duplicate_task":
				other := task
				other.ID = "other"
				tasks = append(tasks, other)
			case "duplicate_bill":
				other := bill
				other.ID = "other"
				bills = append(bills, other)
			case "wrong_tokens":
				n := 38400.0
				cost.BilledUnits = &n
			case "wrong_audio":
				silent := false
				tasks[0].Options.GenerateAudio = &silent
			case "wrong_media":
				tasks[0].MediaType = "image"
			case "wrong_credentials":
				tasks[0].ProviderIdentity = "other"
			}
			if len(MatchUsageBills(tasks, bills, "credential")) != 0 {
				t.Fatal("unreliable bill match accepted")
			}
		})
	}
}
