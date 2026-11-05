package catalogsync

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type failureRunnerRepository struct {
	Repository
	schedules                         []Schedule
	states                            map[string]State
	stateErr, commitErr               map[string]error
	attempts, committed               []string
	pending                           []Run
	emails                            map[string]string
	settingsErr, pendingErr, emailErr error
	afterState                        func()
	locked                            bool
}

func (r *failureRunnerRepository) WithLock(ctx context.Context, fn func(context.Context) error) (bool, error) {
	r.locked = true
	defer func() { r.locked = false }()
	return true, fn(ctx)
}
func (r *failureRunnerRepository) Settings(context.Context) (Settings, error) {
	return Settings{Timezone: "UTC", Email: "reports@example.test"}, r.settingsErr
}
func (r *failureRunnerRepository) Schedules(context.Context) ([]Schedule, error) {
	return append([]Schedule(nil), r.schedules...), nil
}
func (r *failureRunnerRepository) State(_ context.Context, id string) (State, error) {
	if !r.locked {
		panic("state read outside global lock")
	}
	r.attempts = append(r.attempts, id)
	if r.afterState != nil {
		r.afterState()
	}
	return r.states[id], r.stateErr[id]
}
func (r *failureRunnerRepository) Commit(_ context.Context, s Schedule, state State, run Run, _ time.Time) error {
	if !r.locked {
		panic("publication outside global lock")
	}
	if err := r.commitErr[s.Provider]; err != nil {
		return err
	}
	r.states[s.Provider] = state
	r.committed = append(r.committed, s.Provider)
	for i := range r.schedules {
		if r.schedules[i].Provider == s.Provider {
			r.schedules[i].Requested = false
			r.schedules[i].LastCheck = &run.CompletedAt
			next := run.CompletedAt.Add(24 * time.Hour)
			r.schedules[i].NextCheck = &next
		}
	}
	return nil
}
func (r *failureRunnerRepository) PendingEmails(context.Context) ([]Run, error) {
	return r.pending, r.pendingErr
}
func (r *failureRunnerRepository) EmailResult(_ context.Context, id, status string) error {
	if r.emailErr != nil {
		return r.emailErr
	}
	r.emails[id] = status
	return nil
}

type failureRunnerProvider struct {
	bootstrapErr error
	captured     []string
	cancel       context.CancelFunc
}

func (p *failureRunnerProvider) EnsureCatalogSchedules(context.Context) error { return p.bootstrapErr }
func (p *failureRunnerProvider) CaptureCatalog(_ context.Context, id string) (Capture, error) {
	p.captured = append(p.captured, id)
	if p.cancel != nil {
		p.cancel()
	}
	return Capture{}, nil
}
func (*failureRunnerProvider) CheckCatalogRate(context.Context, string, core.MediaModel, Rate) error {
	return nil
}

type failureRunnerMailer struct {
	sent   []string
	err    error
	cancel context.CancelFunc
}

func (m *failureRunnerMailer) SendCatalogReport(_ context.Context, _, _, _, _, key string) error {
	m.sent = append(m.sent, key)
	if m.cancel != nil {
		m.cancel()
	}
	return m.err
}
func failureRunnerFixture(ids ...string) (*Runner, *failureRunnerRepository, *failureRunnerProvider, *failureRunnerMailer, time.Time) {
	now := time.Now().UTC()
	due := now.Add(-time.Hour)
	repo := &failureRunnerRepository{states: map[string]State{}, stateErr: map[string]error{}, commitErr: map[string]error{}, emails: map[string]string{}}
	for _, id := range ids {
		repo.schedules = append(repo.schedules, Schedule{Provider: id, Enabled: true, Requested: true, Time: "03:00", Version: 7, NextCheck: &due})
		repo.states[id] = State{Version: 3, Models: map[string]Entry{"retained": {ID: "retained", Inactive: true}}, Observations: map[string]Observation{"old": {Hash: "last-confirmed"}}}
	}
	provider := &failureRunnerProvider{}
	mailer := &failureRunnerMailer{}
	return &Runner{Repo: repo, Providers: provider, Mailer: mailer}, repo, provider, mailer, now
}
func TestRunnerTickIsolatesProviderFailureAndRecovers(t *testing.T) {
	for _, stage := range []string{"state", "commit", "deadline"} {
		t.Run(stage, func(t *testing.T) {
			runner, repo, _, mailer, now := failureRunnerFixture("alpha", "beta")
			failure := errors.New(stage + " failure")
			if stage == "deadline" {
				failure = context.DeadlineExceeded
			}
			if stage == "state" {
				repo.stateErr["alpha"] = failure
			} else {
				repo.commitErr["alpha"] = failure
			}
			before := encoded(repo.states["alpha"])
			schedule := repo.schedules[0]
			repo.pending = []Run{{ID: "pending", Provider: "earlier", Status: "changed"}}
			if err := runner.Tick(context.Background(), now); !errors.Is(err, failure) {
				t.Fatalf("lost provider error: %v", err)
			}
			if !reflect.DeepEqual(repo.committed, []string{"beta"}) {
				t.Fatalf("later provider blocked: %v", repo.committed)
			}
			if string(encoded(repo.states["alpha"])) != string(before) || !reflect.DeepEqual(repo.schedules[0], schedule) {
				t.Fatal("failed check changed durable evidence or manual intent")
			}
			if !reflect.DeepEqual(mailer.sent, []string{"catalog-pending"}) || repo.emails["pending"] != "sent" {
				t.Fatalf("pending report blocked: %v %v", mailer.sent, repo.emails)
			}
			delete(repo.stateErr, "alpha")
			delete(repo.commitErr, "alpha")
			if err := runner.Tick(context.Background(), now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(repo.committed, []string{"beta", "alpha"}) || repo.schedules[0].Requested || repo.schedules[0].LastCheck == nil {
				t.Fatal("failed provider did not recover", repo.committed)
			}
		})
	}
}
func TestRunnerTickBoundsAndRotatesPersistentFailures(t *testing.T) {
	runner, repo, _, _, now := failureRunnerFixture("alpha", "beta", "gamma", "omega")
	failure := errors.New("persistent")
	for _, id := range []string{"alpha", "beta", "gamma"} {
		repo.stateErr[id] = failure
	}
	for tick := 0; tick < 3; tick++ {
		count := len(repo.attempts)
		err := runner.Tick(context.Background(), now.Add(time.Duration(tick)*time.Minute))
		if !errors.Is(err, failure) {
			t.Fatalf("tick %d masked failure: %v", tick, err)
		}
		if len(repo.attempts)-count > 2 {
			t.Fatal("unbounded check attempts")
		}
	}
	if !reflect.DeepEqual(repo.attempts, []string{"alpha", "beta", "gamma", "omega", "alpha", "beta"}) || !reflect.DeepEqual(repo.committed, []string{"omega"}) {
		t.Fatalf("unfair selection: %v %v", repo.attempts, repo.committed)
	}
}
func TestRunnerTickBootstrapFailureDoesNotBlockExistingWork(t *testing.T) {
	runner, repo, provider, mailer, now := failureRunnerFixture("beta")
	failure := errors.New("one schedule failed")
	provider.bootstrapErr = failure
	repo.pending = []Run{{ID: "pending"}}
	if err := runner.Tick(context.Background(), now); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(repo.committed) != 1 || len(mailer.sent) != 1 {
		t.Fatal("bootstrap blocked independent work")
	}
}
func TestRunnerTickStopsOnCancellation(t *testing.T) {
	for _, stage := range []string{"before", "state", "capture"} {
		t.Run(stage, func(t *testing.T) {
			runner, repo, provider, mailer, now := failureRunnerFixture("alpha", "beta")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch stage {
			case "before":
				cancel()
			case "state":
				repo.afterState = cancel
			case "capture":
				provider.cancel = cancel
			}
			if err := runner.Tick(ctx, now); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if len(repo.committed) != 0 || len(mailer.sent) != 0 || len(repo.attempts) > 1 {
				t.Fatal("continued canceled pass")
			}
		})
	}
}
func TestRunnerTickStopsOnSharedRepositoryFailure(t *testing.T) {
	runner, repo, _, mailer, now := failureRunnerFixture("alpha", "beta")
	local := errors.New("state failed")
	shared := errors.New("store unavailable")
	repo.stateErr["alpha"] = local
	repo.afterState = func() { repo.settingsErr = shared }
	if err := runner.Tick(context.Background(), now); !errors.Is(err, local) || !errors.Is(err, shared) {
		t.Fatal("lost aggregate error", err)
	}
	if !reflect.DeepEqual(repo.attempts, []string{"alpha"}) || len(repo.committed) != 0 || len(mailer.sent) != 0 {
		t.Fatal("continued shared outage")
	}
}
func TestRunnerTickPreservesProviderAndReportErrors(t *testing.T) {
	for _, stage := range []string{"pending", "result", "send"} {
		t.Run(stage, func(t *testing.T) {
			runner, repo, _, mailer, now := failureRunnerFixture("alpha")
			local := errors.New("state failed")
			report := errors.New("report failure")
			repo.stateErr["alpha"] = local
			repo.pending = []Run{{ID: "pending"}}
			switch stage {
			case "pending":
				repo.pendingErr = report
			case "result":
				repo.emailErr = report
			case "send":
				mailer.err = report
			}
			err := runner.Tick(context.Background(), now)
			if !errors.Is(err, local) {
				t.Fatal("provider failure masked", err)
			}
			if stage == "send" {
				if repo.emails["pending"] != "retry" {
					t.Fatal("failed delivery marked sent")
				}
			} else if !errors.Is(err, report) {
				t.Fatal("report error masked", err)
			}
		})
	}
}
func TestRunnerTickHonorsDueAndManualSchedules(t *testing.T) {
	runner, repo, _, _, now := failureRunnerFixture("disabled", "future", "manual", "never")
	future := now.Add(time.Hour)
	repo.schedules[0].Enabled = false
	repo.schedules[0].Requested = false
	repo.schedules[1].NextCheck = &future
	repo.schedules[1].Requested = false
	repo.schedules[2].Enabled = false
	repo.schedules[3].NextCheck = nil
	repo.schedules[3].Requested = false
	if err := runner.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repo.committed, []string{"manual"}) {
		t.Fatal("wrong eligibility", repo.committed)
	}
}

func TestRunnerTickStopsTypedSharedFailureWithReadableSettings(t *testing.T) {
	for _, stage := range []string{"state", "commit", "bootstrap"} {
		t.Run(stage, func(t *testing.T) {
			runner, repo, provider, mailer, now := failureRunnerFixture("alpha", "beta")
			failure := errors.Join(ErrRepositoryUnavailable, errors.New("read-only repository"))
			switch stage {
			case "state":
				repo.stateErr["alpha"] = failure
			case "commit":
				repo.commitErr["alpha"] = failure
			case "bootstrap":
				provider.bootstrapErr = failure
			}
			if _, err := repo.Settings(context.Background()); err != nil {
				t.Fatal("settings must remain readable")
			}
			if err := runner.Tick(context.Background(), now); !errors.Is(err, ErrRepositoryUnavailable) {
				t.Fatal("shared error lost", err)
			}
			if len(repo.attempts) > 1 || len(repo.committed) != 0 || len(mailer.sent) != 0 {
				t.Fatal("continued shared outage", repo.attempts)
			}
		})
	}
}
func TestRunnerRunStartsDespiteLocalScheduleBootstrapFailure(t *testing.T) {
	runner, repo, provider, mailer, _ := failureRunnerFixture("beta")
	provider.bootstrapErr = errors.New("one invalid schedule")
	// Run performs its first pass immediately; cancellation after email proves
	// bootstrap failures still allow both independent work and report delivery.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mailer.cancel = cancel
	repo.pending = []Run{{ID: "pending"}}
	done := make(chan struct{})
	go func() { defer close(done); runner.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish its immediate pass")
	}
	if !reflect.DeepEqual(repo.committed, []string{"beta"}) || len(mailer.sent) != 1 {
		t.Fatal("bootstrap blocked runner startup")
	}
}

func TestRunnerTickJoinsFailuresAndDeliversPendingReport(t *testing.T) {
	runner, repo, _, mailer, now := failureRunnerFixture("alpha", "beta", "gamma")
	first := errors.New("first provider failed")
	second := errors.New("second provider failed")
	repo.stateErr["alpha"] = first
	repo.commitErr["beta"] = second
	repo.pending = []Run{{ID: "pending"}}
	err := runner.Tick(context.Background(), now)
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatal("lost independent failure", err)
	}
	if len(repo.attempts) != 2 || len(repo.committed) != 0 || repo.emails["pending"] != "sent" || len(mailer.sent) != 1 {
		t.Fatal("incorrect bounded pass or pending delivery")
	}
	if err := runner.Tick(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repo.committed, []string{"gamma"}) {
		t.Fatal("persistent failures monopolized next tick")
	}
}
