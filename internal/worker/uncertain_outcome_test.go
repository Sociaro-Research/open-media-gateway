package worker

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/bytedance"
)

// These paths were already repaired by a24ae18. Exercise the actual worker and
// public read projection so admission wording cannot mask a historical fallback.
func TestUncertainProviderOutcomesAlreadyHaveSafeGuidance(t *testing.T) {
	for _, phase := range []string{"submit", "poll", "expired_poll"} {
		t.Run(phase, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: diagnosticTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("private-body https://private.invalid/?token=private-token private-native-task")
			})}
			p, err := bytedance.New("fixture-key", "https://fixture.invalid", client)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			nativeID := "private-native-task"
			cost := core.FreeCost(now)
			beforeCost := cost
			task := core.Task{ID: "11111111-1111-4111-8111-111111111111", Provider: p.Name(), Model: "seedance-model", Prompt: "fixture", Status: core.TaskRunning, ProviderTaskID: &nativeID, WorkerStartedAt: &now, Cost: &cost}
			wantCalls := 1
			if phase == "submit" {
				task.Status, task.ProviderTaskID = core.TaskQueued, nil
			}
			if phase == "expired_poll" {
				started := now.Add(-time.Hour)
				task.WorkerStartedAt = &started
				wantCalls = 0
			}
			repo := &repoStub{task: task}
			w := New(repo, nil, p, nil, time.Second, time.Minute, 1, time.Second)
			if err := w.process(context.Background(), task.ID); err != nil {
				t.Fatal(err)
			}
			if repo.task.Status != core.TaskFailed || calls != wantCalls || !reflect.DeepEqual(repo.task.Cost, &beforeCost) || repo.task.ProviderTaskID != task.ProviderTaskID {
				t.Fatal("uncertain failure changed submissions, accounting or native identity", calls)
			}
			message := publicDiagnostic(t, repo.task)
			for _, action := range []string{"original", "before", "operator"} {
				if !strings.Contains(message, action) {
					t.Fatal("already repaired guidance lost", action, message)
				}
			}
			if phase == "submit" && !strings.Contains(message, "Acceptance may be unknown") || phase == "poll" && !strings.Contains(message, "does not prove the task stopped or was uncharged") || phase == "expired_poll" && !strings.Contains(message, "polling and timeout settings") {
				t.Fatal("phase-specific guidance lost", message)
			}
			if core.PublicGenerationFailure(message) != message {
				t.Fatal("historical read recognizer rejected safe guidance")
			}
			if err := w.process(context.Background(), task.ID); err != nil || calls != wantCalls {
				t.Fatal("terminal redelivery called provider again", err, calls)
			}
		})
	}
}
