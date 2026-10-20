package core

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// Compare complete records (including raw bytes and optional billing fields),
// and check both row identity/order and nested input values for mutation.
func compareBillMatchers(t *testing.T, tasks []Task, bills []BillRecord) map[string]BillRecord {
	t.Helper()
	taskRows, billRows := append([]Task(nil), tasks...), append([]BillRecord(nil), bills...)
	beforeTasks, err := json.Marshal(tasks)
	if err != nil {
		t.Fatal(err)
	}
	beforeBills, err := json.Marshal(bills)
	if err != nil {
		t.Fatal(err)
	}
	want := originalMatchUsageBills(tasks, bills, "credential")
	got := MatchUsageBills(tasks, bills, "credential")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("full BillRecord mismatch:\ngot  %#v\nwant %#v", got, want)
	}
	afterTasks, _ := json.Marshal(tasks)
	afterBills, _ := json.Marshal(bills)
	if string(beforeTasks) != string(afterTasks) || string(beforeBills) != string(afterBills) ||
		(len(tasks) > 0 && !reflect.DeepEqual(tasks, taskRows)) || (len(bills) > 0 && !reflect.DeepEqual(bills, billRows)) {
		t.Fatal("matcher mutated input rows or nested values")
	}
	return got
}

func TestMatchUsageBillsOracleInvariants(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Task, *BillRecord, *[]Task, *[]BillRecord)
		want int
	}{
		{"unique dated model and legacy image", nil, 1},
		{"exact model", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Model = b.Model }, 1},
		{"six nonnumeric suffix", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Model = "seedream-4-5-25112x" }, 0},
		{"seven digit suffix", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Model += "0" }, 0},
		{"start equals bill end", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { a.CreatedAt = *b.UsageEnd }, 0},
		{"end equals bill start", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { a.UpdatedAt = *b.UsageStart }, 1},
		{"submitted start takes priority", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) {
			start := a.CreatedAt
			a.Cost.SubmittedAt = &start
			a.CreatedAt = start.Add(time.Hour)
		}, 1},
		{"submitted outside", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Cost.SubmittedAt = b.UsageEnd }, 0},
		{"usage end takes priority", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) {
			end := a.UpdatedAt
			a.Cost.UsageRecordedAt = &end
			a.UpdatedAt = end.Add(-time.Hour)
		}, 1},
		{"recorded outside", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) {
			end := b.UsageStart.Add(-time.Nanosecond)
			a.Cost.UsageRecordedAt = &end
		}, 0},
		{"duplicate task row", func(a *Task, _ *BillRecord, tasks *[]Task, _ *[]BillRecord) { *tasks = append(*tasks, *a) }, 0},
		{"duplicate bill row", func(_ *Task, b *BillRecord, _ *[]Task, bills *[]BillRecord) { *bills = append(*bills, *b) }, 0},
		{"unsettled bill still touches", func(_ *Task, b *BillRecord, _ *[]Task, bills *[]BillRecord) {
			other := *b
			other.ID = "unsettled"
			other.Settled = false
			*bills = append(*bills, other)
		}, 0},
		{"no cost excluded", func(a *Task, _ *BillRecord, tasks *[]Task, _ *[]BillRecord) {
			other := *a
			other.Cost = nil
			*tasks = append(*tasks, other)
		}, 1},
		{"no charge excluded", func(a *Task, _ *BillRecord, tasks *[]Task, _ *[]BillRecord) {
			other := *a
			cost := *a.Cost
			cost.ActualSource = "provider_failed_no_charge"
			other.Cost = &cost
			*tasks = append(*tasks, other)
		}, 1},
		{"explicit audio requires option", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { audio := false; b.GenerateAudio = &audio }, 0},
		{"nil audio permits explicit option", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) {
			audio := false
			a.Options.GenerateAudio = &audio
		}, 1},
		{"matching explicit audio", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) {
			audio := true
			a.Options.GenerateAudio, b.GenerateAudio = &audio, &audio
		}, 1},
		{"different explicit audio", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) {
			yes, no := true, false
			a.Options.GenerateAudio, b.GenerateAudio = &yes, &no
		}, 0},
		{"stored image input", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) {
			a.Options.ImageURLs = nil
			a.HadImageInput = true
		}, 1},
		{"generate without input", func(a *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) {
			a.Options.ImageURLs = nil
			b.ImageOperation = "generate"
		}, 1},
		{"generate with input", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { b.ImageOperation = "generate" }, 0},
		{"edit without input", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Options.ImageURLs = nil }, 0},
		{"different units", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) { units := 2.0; a.Cost.BilledUnits = &units }, 0},
		{"nil billed units", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Cost.BilledUnits = nil }, 0},
		{"request tariff", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Cost.Pricing.Unit = "request" }, 0},
		{"fixed tariff", func(a *Task, _ *BillRecord, _ *[]Task, _ *[]BillRecord) { a.Cost.Pricing.FixedUSD = 1 }, 0},
		{"missing model", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { b.Model = "" }, 0},
		{"missing start", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { b.UsageStart = nil }, 0},
		{"missing end", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { b.UsageEnd = nil }, 0},
		{"missing units", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { b.Units = nil }, 0},
		{"missing paid", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { b.PaidUSD = nil }, 0},
		{"missing tariff", func(_ *Task, b *BillRecord, _ *[]Task, _ *[]BillRecord) { b.TariffUSD = nil }, 0},
	}
	// These rows must preserve ambiguity until the original final checks.
	for _, mode := range []string{"credentials", "failed", "running", "queued", "manual", "fixed", "units"} {
		cases = append(cases, struct {
			name string
			edit func(*Task, *BillRecord, *[]Task, *[]BillRecord)
			want int
		}{"ambiguity " + mode, func(a *Task, _ *BillRecord, tasks *[]Task, _ *[]BillRecord) {
			other, cost := *a, *a.Cost
			other.ID, other.Cost = "other", &cost
			switch mode {
			case "credentials":
				other.ProviderIdentity = "other"
			case "failed":
				other.Status = TaskFailed
			case "running":
				other.Status = TaskRunning
				end := a.CreatedAt.Add(-time.Hour)
				cost.UsageRecordedAt = &end
			case "queued":
				other.Status = TaskQueued
				other.UpdatedAt = a.CreatedAt.Add(-time.Hour)
			case "manual":
				cost.Pricing.Unit = "request"
			case "fixed":
				cost.Pricing.FixedUSD = 1
			case "units":
				cost.BilledUnits = nil
			}
			*tasks = append(*tasks, other)
		}, 0})
	}
	for _, tc := range cases {
		for _, indexed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/indexed-%t", tc.name, indexed), func(t *testing.T) {
				a, b := imageBillFixture()
				b.Raw, b.Currency, b.InstanceID = json.RawMessage(`{"marker":"full record"}`), "USD", "distinct-instance"
				tasks, bills := []Task{a}, []BillRecord{b}
				if tc.edit != nil {
					tc.edit(&tasks[0], &bills[0], &tasks, &bills)
				}
				if indexed {
					for i := 0; i < 256; i++ {
						other, _ := imageBillFixture()
						other.ID = fmt.Sprint("outside-", i)
						other.CreatedAt = a.CreatedAt.Add(time.Duration(i+1) * 24 * time.Hour)
						other.UpdatedAt = other.CreatedAt.Add(time.Minute)
						tasks = append(tasks, other)
					}
					for i := 0; i < 8; i++ {
						_, padding := imageBillFixture()
						padding.Model, padding.ID = "index-padding", fmt.Sprint("padding-", i)
						bills = append(bills, padding)
					}
				}
				if got := compareBillMatchers(t, tasks, bills); len(got) != tc.want {
					t.Fatalf("got %d matches, want %d", len(got), tc.want)
				}
			})
		}
	}
}

func TestMatchUsageBillsOracleRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(10420261016))
	matches := 0
	for iteration := 0; iteration < 1000; iteration++ {
		a, prototype := imageBillFixture()
		tasks := make([]Task, rng.Intn(300))
		for i := range tasks {
			a, _ := imageBillFixture()
			a.ID = fmt.Sprint(rng.Intn(300)) // Duplicate IDs/rows must not be deduplicated.
			a.Model = []string{a.Model, "seedream-4-5", "seedream-4-5-pro", "seedream-4-5-12345x", "other-123456"}[rng.Intn(5)]
			a.MediaType = []string{"image", "video", ""}[rng.Intn(3)]
			a.Status = []TaskStatus{TaskSucceeded, TaskSucceeded, TaskFailed, TaskQueued, TaskRunning}[rng.Intn(5)]
			a.CreatedAt = a.CreatedAt.Add(time.Duration(rng.Intn(100)-50) * time.Minute)
			a.UpdatedAt = a.CreatedAt.Add(time.Duration(rng.Intn(20)-2) * time.Minute)
			if rng.Intn(3) == 0 {
				start := a.CreatedAt.Add(time.Duration(rng.Intn(20)-10) * time.Minute)
				a.Cost.SubmittedAt = &start
			}
			if rng.Intn(3) == 0 {
				end := a.UpdatedAt.Add(time.Duration(rng.Intn(20)-10) * time.Minute)
				a.Cost.UsageRecordedAt = &end
			}
			if rng.Intn(3) == 0 {
				a.ProviderIdentity = "other"
			}
			if rng.Intn(4) == 0 {
				a.Options.ImageURLs = nil
			}
			a.HadImageInput = rng.Intn(4) == 0
			if rng.Intn(3) != 0 {
				audio := rng.Intn(2) == 0
				a.Options.GenerateAudio = &audio
			}
			switch rng.Intn(12) {
			case 0:
				a.Cost = nil
			case 1:
				a.Cost.ActualSource = "provider_failed_no_charge"
			case 2:
				a.Cost.Pricing.Unit = "request"
			case 3:
				a.Cost.Pricing.FixedUSD = 100
			case 4:
				a.Cost.BilledUnits = nil
			case 5:
				units := 2.0
				a.Cost.BilledUnits = &units
			}
			tasks[i] = a
			if i > 0 && rng.Intn(10) == 0 {
				tasks[i] = tasks[i-1]
			}
		}
		bills := make([]BillRecord, rng.Intn(60))
		for i := range bills {
			bill := prototype
			bill.ID, bill.InstanceID, bill.Currency = fmt.Sprint(rng.Intn(60)), fmt.Sprint("instance-", i), "USD"
			bill.Raw = json.RawMessage(fmt.Sprintf(`{"iteration":%d,"row":%d}`, iteration, i))
			bill.Model = []string{prototype.Model, a.Model, "other", ""}[rng.Intn(4)]
			bill.MediaType = []string{"", "image", "video"}[rng.Intn(3)]
			bill.ImageOperation = []string{"", "edit", "generate"}[rng.Intn(3)]
			start := prototype.UsageStart.Add(time.Duration(rng.Intn(100)-50) * time.Minute)
			end := start.Add(time.Duration(rng.Intn(15)-2) * time.Minute)
			bill.UsageStart, bill.UsageEnd = &start, &end
			if rng.Intn(3) != 0 {
				audio := rng.Intn(2) == 0
				bill.GenerateAudio = &audio
			}
			switch rng.Intn(12) {
			case 0:
				bill.Settled = false
			case 1:
				bill.Units = nil
			case 2:
				bill.UsageStart = nil
			case 3:
				bill.UsageEnd = nil
			case 4:
				bill.PaidUSD = nil
			case 5:
				bill.TariffUSD = nil
			}
			bills[i] = bill
			if i > 0 && rng.Intn(10) == 0 {
				bills[i] = bills[i-1]
			}
		}
		matches += len(compareBillMatchers(t, tasks, bills))
	}
	if matches == 0 {
		t.Fatal("random corpus never exercised a successful match")
	}
	t.Logf("1000 seeded corpora; %d complete matching records", matches)
}

func TestMatchUsageBillsPendingUsesLiveEnd(t *testing.T) {
	a, bill := imageBillFixture()
	// A pending row's recorded future end must not make it eligible tomorrow.
	// Conversely, its stale UpdatedAt must not hide ambiguity in a past window.
	for _, status := range []TaskStatus{TaskQueued, TaskRunning} {
		for _, future := range []bool{false, true} {
			pending := a
			cost := *a.Cost
			pending.ID, pending.Status, pending.Cost = "pending", status, &cost
			start := time.Now().Add(-24 * time.Hour)
			if future {
				start = start.Add(48 * time.Hour)
			}
			end := start.Add(time.Hour)
			row := bill
			row.UsageStart, row.UsageEnd = &start, &end
			a.CreatedAt, a.UpdatedAt = start, end
			pending.CreatedAt, pending.UpdatedAt = start, start.Add(-time.Hour)
			recorded := time.Now().Add(72 * time.Hour)
			cost.UsageRecordedAt = &recorded
			want := 0
			if future {
				want = 1
			}
			for _, indexed := range []bool{false, true} {
				tasks, bills := []Task{a, pending}, []BillRecord{row}
				if indexed {
					for i := 0; i < 256; i++ {
						other, _ := imageBillFixture()
						other.ID, other.Model = fmt.Sprint("padding-", i), "different-model"
						tasks = append(tasks, other)
					}
					for i := 0; i < 8; i++ {
						_, padding := imageBillFixture()
						padding.Model, padding.ID = "index-padding", fmt.Sprint("padding-", i)
						bills = append(bills, padding)
					}
				}
				if got := compareBillMatchers(t, tasks, bills); len(got) != want {
					t.Fatalf("status=%s future=%t indexed=%t: %d matches", status, future, indexed, len(got))
				}
			}
		}
	}
}
