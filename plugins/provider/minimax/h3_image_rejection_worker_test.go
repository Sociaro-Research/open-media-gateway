package minimax_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"github.com/google/uuid"
)

type h3RejectionCase struct {
	id, model, path, mode string
	status                int
	writes                atomic.Int32
	ackRead               atomic.Bool
	posts                 atomic.Int32
}

type h3ReplayRepo struct {
	*postgres.TaskRepository
	observed chan string
}

func (r h3ReplayRepo) AcquireTask(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	ok, err := r.TaskRepository.AcquireTask(ctx, id, ttl)
	if err == nil && !ok {
		r.observed <- id
	}
	return ok, err
}

func TestH3RejectedSubmissionsAccounting(t *testing.T) {
	ctx, db, dsn, q := minimaxPostgres(t)
	const revision = "h3-rejected-fixture"
	scope := core.WithProviderConnectionScope(identity.WithOwnerID(ctx, identity.SystemOwnerID), core.ProviderConnectionScope{ID: "minimax", OwnerID: identity.SystemOwnerID, Revision: revision})
	repo := postgres.NewTaskRepository(db)
	cases := map[string]*h3RejectionCase{}
	add := func(model, path, mode string, status int) {
		id := uuid.NewString()
		cases[id] = &h3RejectionCase{id: id, model: model, path: path, mode: mode, status: status}
	}
	for _, model := range []string{"MiniMax-H3-Image", "MiniMax-H3-Image-Highspeed"} {
		for _, path := range []string{"/v1/images/generations", "/v1/images/edits"} {
			for _, status := range []int{402, 422, 500} {
				for _, mode := range []string{"verified", "missing", "contradictory", "generic", "non-json"} {
					add(model, path, mode, status)
				}
			}
		}
	}
	for _, mode := range []string{"transport", "claim-loss", "lost-commit-ack", "ordinary-auth", "transient-write", "unavailable-write"} {
		add("MiniMax-H3-Image", "/v1/images/generations", mode, 500)
	}
	successor := core.WithClaimToken(scope, uuid.NewString())
	native := &http.Client{Transport: workerRoundTrip(func(req *http.Request) (*http.Response, error) {
		var b struct {
			Prompt, Model string
			N             int
			Stream        bool
			Images        []struct {
				URL string `json:"image_url"`
			}
		}
		if json.NewDecoder(req.Body).Decode(&b) != nil {
			return nil, errors.New("bad fixture request")
		}
		c := cases[b.Prompt]
		if c == nil || c.posts.Add(1) != 1 || req.Method != "POST" || req.URL.Host != "api.minimax.io" || req.URL.Path != c.path || b.Model != c.model || b.N != 2 || b.Stream || req.GetBody != nil || (c.path == "/v1/images/edits") != (len(b.Images) == 1) {
			t.Error("unexpected native request")
			return nil, errors.New("fixture mismatch")
		}
		status, typ, body := c.status, "application/json; charset=utf-8", minimax.H3RejectionFixture(c.status)
		switch c.mode {
		case "transport":
			return nil, errors.New("private transport detail")
		case "ordinary-auth":
			status, body = 401, `{"type":"error","error":{"http_code":"401","message":"private provider detail"}}`
		case "non-json":
			typ = "text/plain"
		case "missing":
			body = strings.Replace(body, `"text_tokens":0,`, "", 1)
		case "contradictory":
			body = strings.Replace(body, `"total_tokens":0`, `"total_tokens":37`, 1)
		case "generic":
			body = fmt.Sprintf(`{"type":"error","error":{"http_code":"%d","message":"private provider detail"}}`, status)
		case "claim-loss":
			if err := repo.ReleaseTask(req.Context(), c.id); err != nil {
				return nil, err
			}
			if ok, err := repo.AcquireTask(successor, c.id, time.Minute); err != nil || !ok {
				t.Error("could not transfer active claim")
				return nil, errors.New("fixture claim")
			}
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {typ}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	provider := minimax.NewH3Fixture(t, native)
	now := time.Now().UTC()
	const url = "https://reference.fixture/source.png"
	webhook := "https://webhook.fixture/receiver"
	faultRepo := &h3TerminalFaultRepo{TaskRepository: repo, begin: make(chan error, 1), cases: cases}
	for _, c := range cases {
		options := core.MediaOptions{OutputCount: 2}
		if c.path == "/v1/images/edits" {
			options.ImageURLs = []string{url}
			options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{url: {Type: "image", ContentType: "image/png", SizeBytes: 500, Width: 1024, Height: 1024}}
		}
		cost, _, err := provider.Quote(scope, core.MediaModel{}, core.MediaRequest{Model: c.model, Prompt: c.id, Options: options})
		if err != nil {
			t.Fatal(err)
		}
		if c.mode == "claim-loss" {
			faultRepo.lost = c.id
		}
		tsk := core.Task{ID: c.id, Provider: "minimax", ProviderIdentity: revision, Model: c.model, MediaType: "image", Prompt: c.id, Options: options, Cost: &cost, CreatedAt: now, UpdatedAt: now, WebhookURL: &webhook, MinimizeRequestLogging: c.model == "MiniMax-H3-Image", Status: core.TaskQueued}
		if _, _, err = repo.CreateOrGet(scope, tsk, "", c.id); err != nil {
			t.Fatal(err)
		}
		if err := q.Enqueue(scope, c.id); err != nil {
			t.Fatal(err)
		}
	}
	// Sequences survive transaction rollback, so the first webhook write can
	// fail inside the actual terminal transaction and the second can commit.
	for _, c := range cases {
		if c.mode == "transient-write" || c.mode == "unavailable-write" {
			name := "transient"
			attempts := 1
			if c.mode == "unavailable-write" {
				name, attempts = "unavailable", 2
			}
			query := fmt.Sprintf(`CREATE SEQUENCE fixture_%[1]s_attempts;
CREATE FUNCTION fixture_%[1]s_webhook() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF nextval('fixture_%[1]s_attempts') <= %[2]d THEN RAISE EXCEPTION 'fixture transient webhook outage'; END IF; RETURN NEW; END $$;
CREATE TRIGGER fixture_%[1]s BEFORE INSERT ON webhook_deliveries FOR EACH ROW
WHEN (NEW.task_id='%[3]s' AND NEW.event_type='task.failed') EXECUTE FUNCTION fixture_%[1]s_webhook()`, name, attempts, c.id)
			if _, err := db.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
		}
	}
	original, stop := context.WithCancel(scope)
	w := worker.New(faultRepo, q, provider, nil, time.Millisecond, 12*time.Minute, 1, time.Millisecond)
	w.OperationConcurrency = 1
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- w.Run(original)
	}()
	defer func() { stop(); <-done }()
	select {
	case err := <-faultRepo.begin:
		if !errors.Is(err, core.ErrLeaseLost) {
			t.Fatal("old claim wrote result", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lost, err := repo.Get(scope, faultRepo.lost)
	if err != nil || lost.Status != core.TaskRunning || lost.Usage != nil || lost.Cost == nil || lost.Cost.ActualUSD != nil {
		t.Fatal("stale write escaped", err)
	}
	if err = repo.ReleaseTask(successor, lost.ID); err != nil {
		t.Fatal(err)
	}
	for {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE status='failed'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == len(cases) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	reopened, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	fresh := postgres.NewTaskRepository(reopened)
	readRepo := h3ReplayRepo{fresh, make(chan string, len(cases)*2)}
	replay, finish := context.WithCancel(scope)
	w = worker.New(readRepo, q, minimax.NewH3Fixture(t, native), nil, time.Millisecond, 12*time.Minute, 1, time.Millisecond)
	w.OperationConcurrency = 1
	completed := make(chan error, 1)
	go func() { completed <- w.Run(replay) }()
	defer func() {
		finish()
		if err := <-completed; err != nil {
			t.Error(err)
		}
	}()
	for id := range cases {
		if err := q.Enqueue(scope, id); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for len(seen) != len(cases) {
		select {
		case id := <-readRepo.observed:
			seen[id] = true
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for _, c := range cases {
		task, err := fresh.Get(scope, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != core.TaskFailed || task.HasStoredResult || len(task.StoredArtifacts) != 0 || c.posts.Load() != 1 || task.Cost == nil || task.Error == nil {
			t.Fatal("failed task invariant", c.mode, c.status)
		}
		known := c.mode == "verified" || c.mode == "lost-commit-ack" || c.mode == "transient-write"
		var writes int32
		switch c.mode {
		case "verified", "claim-loss":
			writes = 1
		case "transient-write", "unavailable-write", "lost-commit-ack":
			writes = 2
		}
		if c.writes.Load() != writes {
			t.Fatal("unexpected local persistence attempts", c.mode, c.writes.Load())
		}
		if c.mode == "lost-commit-ack" && !c.ackRead.Load() {
			t.Fatal("lost acknowledgment was not recognized under the original claim")
		}
		if c.mode == "ordinary-auth" && *task.Error == core.ErrProviderSubmissionUncertain.Error() {
			t.Fatal("ordinary auth rejection became uncertain")
		}
		if known {
			want := core.UnchargedImageRejection{}.Usage()
			if !reflect.DeepEqual(task.Usage, &want) || task.Cost.ActualUSD == nil || *task.Cost.ActualUSD != 0 || task.Cost.MeteredUSD == nil || *task.Cost.MeteredUSD != 0 || task.Cost.ActualSource != "provider_failed_no_charge" || task.Cost.Status != "confirmed" || task.Cost.UsageRecordedAt == nil {
				t.Fatal("confirmed row missing evidence", c.path, c.status)
			}
		} else if task.Usage != nil || task.Cost.ActualUSD != nil || task.Cost.MeteredUSD != nil {
			t.Fatal("unverified zero was stored", c.mode, c.status)
		}
		if (c.mode == "claim-loss" || c.mode == "transport" || c.mode == "non-json" || c.mode == "unavailable-write" || c.status == 500 && (c.mode == "generic" || c.mode == "missing" || c.mode == "contradictory")) && *task.Error != core.ErrProviderSubmissionUncertain.Error() {
			t.Fatal("lost uncertainty", c.mode)
		}
		var events, receipts int
		var payload []byte
		if err := reopened.QueryRowContext(ctx, `SELECT count(*), (array_agg(payload))[1] FROM webhook_deliveries WHERE task_id=$1 AND event_type=$2`, c.id, core.EventTaskFailed).Scan(&events, &payload); err != nil || events != 1 {
			t.Fatal("duplicate or missing failed webhook", c.mode, events, err)
		}
		if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM accepted_provider_receipts WHERE task_id=$1", c.id).Scan(&receipts); err != nil || receipts != 0 {
			t.Fatal("spurious receipt", c.mode, err)
		}
		if strings.Contains(*task.Error, "private") || strings.Contains(string(payload), "private provider detail") || strings.Contains(string(payload), "private image detail") {
			t.Fatal("raw provider data exposed")
		}
		if known && !strings.Contains(string(payload), "provider_failed_no_charge") {
			t.Fatal("webhook missing atomic cost")
		}
		if task.MinimizeRequestLogging && task.Prompt != "" {
			t.Fatal("terminal privacy cleanup bypassed")
		}
	}
	t.Logf("%d failed tasks reopened and redelivered; one native POST and one terminal webhook per task", len(cases))
}

type h3TerminalFaultRepo struct {
	*postgres.TaskRepository
	lost  string
	cases map[string]*h3RejectionCase
	begin chan error
}

func (r *h3TerminalFaultRepo) MarkImagesRejectedWithoutCharge(ctx context.Context, t core.Task, result core.UnchargedImageRejection) error {
	c := r.cases[t.ID]
	attempt := c.writes.Add(1)
	err := r.TaskRepository.MarkImagesRejectedWithoutCharge(ctx, t, result)
	if t.ID == r.lost {
		r.begin <- err
	}
	if c.mode == "lost-commit-ack" && attempt == 2 {
		c.ackRead.Store(err == nil)
	}
	if err == nil && c.mode == "lost-commit-ack" && attempt == 1 {
		return errors.New("fixture lost commit acknowledgment")
	}
	return err
}

func TestH3RejectionAtomicRollback(t *testing.T) {
	ctx, db, dsn, _ := minimaxPostgres(t)
	owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
	claim := core.WithClaimToken(owner, uuid.NewString())
	repo := postgres.NewTaskRepository(db)
	now := time.Now().UTC()
	webhook := "https://webhook.fixture/receiver"
	task := core.Task{ID: uuid.NewString(), Provider: "minimax", ProviderIdentity: "fixture", Model: "MiniMax-H3-Image", MediaType: "image", Prompt: "private fixture", WebhookURL: &webhook, Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now, MinimizeRequestLogging: true, Cost: &core.TaskCost{QuantityUnknown: true}}
	task, _, err := repo.CreateOrGet(owner, task, "", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.AcquireTask(claim, task.ID, time.Minute); err != nil || !ok {
		t.Fatal("claim", ok, err)
	}
	if err := repo.MarkRunning(claim, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = repo.Get(owner, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim = core.WithProviderRevision(identity.WithResourceOwners(claim, task.ProviderOwnerID, task.StorageOwnerID), task.ProviderIdentity)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fixture_block_rejection() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.event_type = 'task.failed' THEN RAISE EXCEPTION 'fixture terminal webhook unavailable'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER fixture_block_rejection BEFORE INSERT ON webhook_deliveries FOR EACH ROW EXECUTE FUNCTION fixture_block_rejection()`); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkImagesRejectedWithoutCharge(claim, task, core.UnchargedImageRejection{}); err == nil || !strings.Contains(err.Error(), "fixture terminal webhook unavailable") {
		t.Fatal("did not reach terminal webhook fault", err)
	}
	reopened, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	fresh := postgres.NewTaskRepository(reopened)
	state, err := fresh.Get(owner, task.ID)
	if err != nil || state.Status != core.TaskRunning || state.Error != nil || state.Usage != nil || state.Cost == nil || state.Cost.ActualUSD != nil || state.Cost.MeteredUSD != nil || state.Prompt != task.Prompt {
		t.Fatal("partial terminal write after rollback", err)
	}
	countFailures := func() int {
		t.Helper()
		var n int
		if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM webhook_deliveries WHERE task_id=$1 AND event_type=$2", task.ID, core.EventTaskFailed).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if countFailures() != 0 {
		t.Fatal("failed webhook escaped rollback")
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER fixture_block_rejection ON webhook_deliveries"); err != nil {
		t.Fatal(err)
	}
	if err := fresh.MarkImagesRejectedWithoutCharge(claim, task, core.UnchargedImageRejection{}); err != nil {
		t.Fatal(err)
	}
	state, err = fresh.Get(owner, task.ID)
	if err != nil || state.Status != core.TaskFailed || state.Prompt != "" || state.Usage == nil || state.Cost.ActualUSD == nil || *state.Cost.ActualUSD != 0 || countFailures() != 1 {
		t.Fatal("incomplete terminal commit", err)
	}
	if err := fresh.MarkImagesRejectedWithoutCharge(core.WithClaimToken(claim, uuid.NewString()), task, core.UnchargedImageRejection{}); !errors.Is(err, core.ErrLeaseLost) {
		t.Fatal("foreign claim acknowledged the write", err)
	}
	if err := fresh.MarkImagesRejectedWithoutCharge(claim, task, core.UnchargedImageRejection{}); err != nil || countFailures() != 1 {
		t.Fatal("committed write was not idempotent", err)
	}
}
