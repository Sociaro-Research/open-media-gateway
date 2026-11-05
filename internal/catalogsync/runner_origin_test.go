package catalogsync

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type originRunnerRepository struct {
	*failureRunnerRepository
	runs []Run
}

func (r *originRunnerRepository) Commit(ctx context.Context, schedule Schedule, state State, run Run, next time.Time) error {
	if err := r.failureRunnerRepository.Commit(ctx, schedule, state, run, next); err != nil {
		return err
	}
	var stored Run
	if err := json.Unmarshal(encoded(run), &stored); err != nil {
		return err
	}
	r.runs = append(r.runs, stored)
	return nil
}

func TestRunnerSnapshotsCheckOriginBeforeCaptureAndCommit(t *testing.T) {
	runner, repo, _, _, now := failureRunnerFixture("fixture")
	stored := &originRunnerRepository{failureRunnerRepository: repo}
	runner.Repo = stored
	runner.CheckOrigin = "https://ORIGINAL.example.test:443/"
	repo.afterState = func() { runner.CheckOrigin = "https://current-sender.example.test" }
	if err := runner.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(stored.runs) != 1 || stored.runs[0].CheckOrigin != "https://original.example.test" {
		t.Fatal("origin was not frozen before checking", stored.runs)
	}
	if _, err := uuid.Parse(stored.runs[0].ID); err != nil {
		t.Fatal("run correlation ID lost", err)
	}
	_, report, _ := Report(stored.runs[0])
	if strings.Contains(report, runner.CheckOrigin) {
		t.Fatal("report was relabeled as the current sender")
	}
}

func TestRunnerCheckOriginDoesNotAffectReconciliation(t *testing.T) {
	now := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	var states []State
	var runs []Run
	for _, origin := range []string{"https://one.example.test", "https://two.example.test", "", "https://user:synthetic-secret@example.test"} {
		runner, repo, _, _, _ := failureRunnerFixture("fixture")
		stored := &originRunnerRepository{failureRunnerRepository: repo}
		runner.Repo = stored
		runner.CheckOrigin = origin
		if err := runner.Tick(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		if len(stored.runs) != 1 {
			t.Fatal("missing committed run")
		}
		run := stored.runs[0]
		if origin == "" || strings.Contains(origin, "synthetic-secret") {
			if run.CheckOrigin != "" || strings.Contains(string(encoded(run)), "check_origin") {
				t.Fatal("missing/invalid origin should be omitted from saved JSON")
			}
		} else if run.CheckOrigin != origin {
			t.Fatal("valid origin was not persisted")
		}
		run.ID, run.CheckOrigin, run.CompletedAt = "", "", time.Time{}
		runs = append(runs, run)
		states = append(states, repo.states["fixture"])
	}
	for i := 1; i < len(runs); i++ {
		if !reflect.DeepEqual(runs[0], runs[i]) || !reflect.DeepEqual(states[0], states[i]) {
			t.Fatal("provenance changed catalogue outcomes", i)
		}
	}
}
