package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

// Faults occur after real SQL acceptance, without repeating a mutation or making
// a provider call. They model a lost commit acknowledgement and failed read.
type uncertainAdmissionRepository struct {
	*TaskRepository
	ackLost, readUnavailable bool
	creates, recoveries      int
	accepted                 core.Task
}

type uncertainAdmissionProvider struct{ admissionProvider }

func (*uncertainAdmissionProvider) IsFree() bool { return true }

func (r *uncertainAdmissionRepository) CreateOrGet(ctx context.Context, task core.Task, key, hash string) (core.Task, bool, error) {
	r.creates++
	stored, created, err := r.TaskRepository.CreateOrGet(ctx, task, key, hash)
	if err == nil && r.ackLost {
		r.accepted = stored
		r.readUnavailable = true
		return core.Task{}, false, errors.New("private-body https://private.invalid/?token=private-token foreign-task")
	}
	return stored, created, err
}

func (r *uncertainAdmissionRepository) RecoverAcceptedIntent(ctx context.Context, key, intent string) (core.Task, bool, error) {
	r.recoveries++
	if r.readUnavailable {
		return core.Task{}, false, errors.New("private-body https://private.invalid/?token=private-token foreign-task")
	}
	return r.TaskRepository.RecoverAcceptedIntent(ctx, key, intent)
}

func TestGenerationUnconfirmedCommittedAdmissionAndSameKeyRecovery(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_key", true: "with_key"}[keyed], func(t *testing.T) {
			db := auditDatabase(t, true)
			repo := &uncertainAdmissionRepository{TaskRepository: NewTaskRepository(db), ackLost: true}
			q := &quantityQueue{}
			p := &uncertainAdmissionProvider{admissionProvider{MediaProvider: mock.New(time.Millisecond, ""), enabled: true}}
			svc := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "mock-video-v1"}, "mock")
			svc.EnableAccounting()
			h := api.New(svc, admissionHealth{}, "synthetic-secret")
			send := func(method, path, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer synthetic-secret")
				if keyed {
					r.Header.Set("Idempotency-Key", "private-key")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			body := `{"model":"mock/mock-video-v1","prompt":"synthetic lost acknowledgement"}`
			w := send("POST", "/v1/videos", body)
			var response struct{ Error map[string]any }
			if w.Code != 503 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error["code"] != "service_unavailable" || response.Error["type"] != "server_error" || response.Error["param"] != nil || len(response.Error) != 4 {
				t.Fatal("uncertain commit response changed", w.Code, w.Body.String())
			}
			for _, action := range []string{"could not be confirmed", "Journal", "original Idempotency-Key", "operator"} {
				if !strings.Contains(w.Body.String(), action) {
					t.Fatal("uncertain commit has no safe next step", action, w.Body.String())
				}
			}
			for _, private := range []string{"private-body", "private.invalid", "private-token", "foreign-task", "private-key"} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatal("private fault details escaped")
				}
			}
			if repo.creates != 1 || repo.accepted.ID == "" || q.enqueued != 0 || p.submits != 0 || keyed && repo.recoveries != 3 || !keyed && repo.recoveries != 0 {
				t.Fatal("fault caused another admission or enqueue", repo.creates, q.enqueued, p.submits, repo.recoveries)
			}
			before, err := repo.Get(context.Background(), repo.accepted.ID)
			if err != nil || before.Cost == nil {
				t.Fatal("committed admission has no pricing snapshot", err)
			}
			// The original accepted task can still be checked without submitting.
			for _, path := range []string{"/v1/generations/" + repo.accepted.ID, "/v1/generations"} {
				read := send("GET", path, "")
				if read.Code != 200 || !strings.Contains(read.Body.String(), repo.accepted.ID) {
					t.Fatal("original task unavailable to Journal/status", read.Code)
				}
			}
			if keyed {
				repo.ackLost, repo.readUnavailable = false, false
				replay := send("POST", "/v1/videos", body)
				var recovered struct{ ID, Status string }
				if replay.Code != 202 || json.Unmarshal(replay.Body.Bytes(), &recovered) != nil || recovered.ID != repo.accepted.ID || recovered.Status != "queued" || replay.Header().Get("Location") != core.GenerationView(repo.accepted).StatusURL || repo.creates != 1 || q.enqueued != 1 || p.submits != 0 {
					t.Fatal("same-key recovery changed original admission", replay.Code, repo.creates, q.enqueued, p.submits)
				}
			}
			stored, err := repo.Get(context.Background(), repo.accepted.ID)
			if err != nil || stored.Status != core.TaskQueued || !reflect.DeepEqual(stored.Cost, before.Cost) {
				t.Fatal("diagnostics/recovery changed lifecycle or accepted pricing", err)
			}
			var tasks int
			if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&tasks); err != nil || tasks != 1 {
				t.Fatal("durable admission count changed", tasks, err)
			}
		})
	}
}
