package worker

import (
	"context"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

type timeoutReceivingProvider struct {
	providerStub
	timeout *int64
}

func (p *timeoutReceivingProvider) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	p.timeout = r.Options.TimeoutSeconds
	return p.providerStub.Generate(ctx, r)
}
func TestWorkerPassesPersistedSubmissionTimeout(t *testing.T) {
	seconds := int64(600)
	repo := &repoStub{task: core.Task{ID: "timeout", Status: core.TaskQueued, Model: "model", Options: core.MediaOptions{TimeoutSeconds: &seconds}}}
	p := &timeoutReceivingProvider{}
	w := New(repo, nil, p, &storageStub{}, time.Millisecond, time.Second, 2, time.Second)
	if err := processSteps(w, repo); err != nil {
		t.Fatal(err)
	}
	if p.timeout == nil || *p.timeout != 600 || p.created != 1 {
		t.Fatal("persisted override lost or generation repeated", p.timeout, p.created)
	}
}
