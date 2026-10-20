package reliability_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
)

type latencySummary struct {
	Count              int `json:"count"`
	P50, P95, P99, Max float64
}

func latencies(samples []float64) latencySummary {
	if len(samples) == 0 {
		return latencySummary{}
	}
	sort.Float64s(samples)
	percentile := func(p float64) float64 { return samples[max(0, int(math.Ceil(float64(len(samples))*p))-1)] }
	return latencySummary{len(samples), percentile(.50), percentile(.95), percentile(.99), samples[len(samples)-1]}
}

type resourceSample struct {
	Seconds                                 float64 `json:"seconds"`
	HeapBytes, HeapObjects, TotalAllocBytes uint64
	Goroutines, DBOpen, DBInUse             int
	DBWaitCount                             int64
	DBMaxIdleClosed, DBMaxLifetimeClosed    int64
	DBWaitSeconds                           float64
	Queued, Running, Succeeded, Failed      int
	OldestQueuedSeconds, PollOverdueSeconds float64
}

func sample(s *testStack, start time.Time) resourceSample {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	db := s.db.Stats()
	x := resourceSample{Seconds: time.Since(start).Seconds(), HeapBytes: mem.HeapAlloc, HeapObjects: mem.HeapObjects, TotalAllocBytes: mem.TotalAlloc, Goroutines: runtime.NumGoroutine(), DBOpen: db.OpenConnections, DBInUse: db.InUse, DBWaitCount: db.WaitCount, DBWaitSeconds: db.WaitDuration.Seconds()}
	x.DBMaxIdleClosed, x.DBMaxLifetimeClosed = db.MaxIdleClosed, db.MaxLifetimeClosed
	// Worker IDs belong to the fixture; synthetic historical rows have no
	// provider_task_id and a different prompt, so they do not inflate completions.
	err := s.db.QueryRowContext(s.ctx, `SELECT count(*) FILTER(WHERE status='queued'),count(*) FILTER(WHERE status='running'),count(*) FILTER(WHERE status='succeeded'),count(*) FILTER(WHERE status='failed'),COALESCE(max(EXTRACT(EPOCH FROM clock_timestamp()-created_at)) FILTER(WHERE status='queued'),0),COALESCE(max(EXTRACT(EPOCH FROM clock_timestamp()-worker_next_at)) FILTER(WHERE status='running' AND worker_next_at<clock_timestamp() AND worker_lease_until IS NULL),0) FROM tasks WHERE prompt<>'synthetic history'`).Scan(&x.Queued, &x.Running, &x.Succeeded, &x.Failed, &x.OldestQueuedSeconds, &x.PollOverdueSeconds)
	if err != nil {
		s.t.Error(err)
	}
	return x
}

type performanceResult struct {
	DatabaseTasks, ProviderJobs, VerifiedCSVRows                 int
	VerifiedSummaryRows                                          int64
	ReportClients                                                int
	Name                                                         string `json:"name"`
	GoVersion, OS, Architecture                                  string
	CPUs, Workers, HistoryRows, TargetRPS                        int
	Seconds                                                      float64
	OfferedSeconds, DrainSeconds                                 float64
	Attempted, Accepted, Completed, DroppedStarts                int
	HTTPStatuses                                                 map[string]int
	AdmissionMS, ReadMS, ReportMS, SubmitDelayMS, PublishDelayMS latencySummary
	ReportByEndpointMS                                           map[string]latencySummary
	Samples                                                      []resourceSample
	Before, AfterIdle                                            resourceSample
	Errors                                                       []string
}
type observations struct {
	sync.Mutex
	admission, read, report []float64
	reports                 map[string][]float64
	statuses                map[string]int
	ids                     []string
	errors                  []string
}

func (o *observations) record(op string, start time.Time, status int, err error) {
	o.Lock()
	defer o.Unlock()
	ms := float64(time.Since(start).Microseconds()) / 1000
	switch op {
	case "admission":
		o.admission = append(o.admission, ms)
	case "read":
		o.read = append(o.read, ms)
	default:
		if strings.HasPrefix(op, "report/") {
			o.report = append(o.report, ms)
			if o.reports == nil {
				o.reports = map[string][]float64{}
			}
			o.reports[op] = append(o.reports[op], ms)
		}
	}
	o.statuses[fmt.Sprintf("%s:%d", op, status)]++
	expected := http.StatusOK
	if op == "admission" {
		expected = http.StatusAccepted
	}
	if err != nil {
		o.errors = append(o.errors, err.Error())
	} else if status != expected {
		o.errors = append(o.errors, fmt.Sprintf("%s HTTP %d", op, status))
	}
}

func TestLoadAccountingRejectsWrongSuccessStatus(t *testing.T) {
	o := &observations{statuses: map[string]int{}}
	o.record("admission", time.Now(), http.StatusOK, nil)
	if len(o.errors) != 1 {
		t.Fatal("HTTP 200 without an accepted task must fail the load run")
	}
	o.record("admission", time.Now(), http.StatusAccepted, nil)
	o.record("report/csv", time.Now(), http.StatusOK, nil)
	if len(o.errors) != 1 {
		t.Fatal("expected admission/report statuses were incorrectly rejected")
	}
}
func perfInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		t.Fatalf("invalid %s", name)
	}
	return n
}
func perfDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		t.Fatalf("invalid %s", name)
	}
	return d
}
func perfList(t *testing.T, name string, fallback string) []int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		raw = fallback
	}
	out := []int{}
	for _, v := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("invalid %s", name)
		}
		out = append(out, n)
	}
	return out
}
func writeEvidence(t *testing.T, name string, value any) {
	t.Helper()
	output := os.Getenv("GATEWAY_PERF_OUTPUT")
	if output == "" {
		t.Fatal("GATEWAY_PERF_OUTPUT is required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, name+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func (s *testStack) personalKey() identity.User {
	s.t.Helper()
	user, err := postgres.NewAuthRepository(s.db).UpsertGoogleUser(s.ctx, "perf-user", "perf@example.test", "Performance", "")
	if err != nil {
		s.t.Fatal(err)
	}
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Performance", Scopes: []string{apikey.Generation, apikey.KeysManage}}, time.Now())
	if err != nil {
		s.t.Fatal(err)
	}
	if err = postgres.NewAPIKeyRepository(s.db).Create(s.ctx, user.ID, key, apikey.Hash(secret)); err != nil {
		s.t.Fatal(err)
	}
	s.key = secret
	return user
}

func seedHistory(t *testing.T, s *testStack, n int, owner string) {
	t.Helper()
	cost, _ := json.Marshal(core.FreeCost(time.Now()))
	_, err := s.db.ExecContext(s.ctx, `INSERT INTO tasks(id,status,provider,model,media_type,prompt,owner_id,actor_id,provider_owner_id,storage_owner_id,cost,created_at,updated_at) SELECT gen_random_uuid(),'succeeded','mock','test-video','video','synthetic history',$1::uuid,$1::uuid,$1::uuid,$1::uuid,$3::jsonb,now()-interval '365 days'+(i%31536000)*interval '1 second',now()-interval '365 days'+(i%31536000)*interval '1 second' FROM generate_series(1,$2) AS i`, owner, n, string(cost))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(s.ctx, "ANALYZE tasks"); err != nil {
		t.Fatal(err)
	}
}

// The performance suite is explicit opt-in: machine-sensitive time budgets
// cannot make ordinary correctness CI flaky. Failure accounting is mandatory.
func TestPerformanceGateway(t *testing.T) {
	if os.Getenv("GATEWAY_PERF") != "1" {
		t.Skip("set GATEWAY_PERF=1 for measured load tests")
	}
	duration := perfDuration(t, "GATEWAY_PERF_STAGE", 10*time.Second)
	history := perfInt(t, "GATEWAY_PERF_HISTORY", 10000)
	for _, workers := range perfList(t, "GATEWAY_PERF_WORKERS", "1,2,4") {
		for _, rate := range perfList(t, "GATEWAY_PERF_RATES", "1,10,50") {
			t.Run(fmt.Sprintf("workers-%d/rps-%d", workers, rate), func(t *testing.T) {
				s := newStack(t, duration+3*time.Minute)
				user := s.personalKey()
				seedHistory(t, s, history, identity.PersonalOwnerID(user.ID))
				s.fixture.delay = 100 * time.Millisecond
				s.startWorkers(workers, time.Second)
				measureLoad(t, s, fmt.Sprintf("gateway-w%d-r%d", workers, rate), duration, rate, workers, history)
			})
		}
	}
}
func TestPerformanceSoak(t *testing.T) {
	if os.Getenv("GATEWAY_PERF") != "1" || os.Getenv("GATEWAY_PERF_SOAK") == "" {
		t.Skip("set GATEWAY_PERF_SOAK to a duration")
	}
	d := perfDuration(t, "GATEWAY_PERF_SOAK", 2*time.Hour)
	s := newStack(t, d+3*time.Minute)
	user := s.personalKey()
	history := perfInt(t, "GATEWAY_PERF_HISTORY", 10000)
	seedHistory(t, s, history, identity.PersonalOwnerID(user.ID))
	s.startWorkers(2, time.Second)
	measureLoad(t, s, "soak", d, perfInt(t, "GATEWAY_PERF_SOAK_RPS", 1), 2, history)
}
func measureLoad(t *testing.T, s *testStack, name string, duration time.Duration, rate, workers, history int) {
	count := int(duration.Seconds() * float64(rate))
	if count < 1 {
		t.Fatal("stage duration and rate must offer at least one request")
	}
	if os.Getenv("GATEWAY_PERF_CPU_PROFILE") == "1" {
		file, err := os.Create(filepath.Join(os.Getenv("GATEWAY_PERF_OUTPUT"), name+"-cpu.pprof"))
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			file.Close()
			t.Fatal(err)
		}
		defer func() { pprof.StopCPUProfile(); file.Close() }()
	}
	runtime.GC()
	start := time.Now()
	before := sample(s, start)
	result := performanceResult{Name: name, GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU(), Workers: workers, HistoryRows: history, TargetRPS: rate, Before: before, OfferedSeconds: duration.Seconds()}
	defer func() { writeEvidence(t, name, result) }()
	o := &observations{statuses: map[string]int{}}
	ctx, stop := context.WithCancel(s.ctx)
	defer stop()
	var background, requests sync.WaitGroup
	// Real report HTTP serialization/CSV spool, alongside status reads and admission.
	for _, op := range []string{"read", "report"} {
		op := op
		clients := 1
		if op == "report" {
			clients = perfInt(t, "GATEWAY_PERF_REPORT_CLIENTS", 1)
			result.ReportClients = clients
		}
		for client := 0; client < clients; client++ {
			background.Add(1)
			go func() {
				defer background.Done()
				ticker := time.NewTicker(500 * time.Millisecond)
				defer ticker.Stop()
				iteration := 0
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if ctx.Err() != nil {
							return
						}
						operation := op
						path := "/v1/generations?limit=50"
						if op == "report" {
							operation = "report/table"
							path = "/v1/admin/reports?from=2020-01-01&to=2030-01-01"
							switch iteration % 3 {
							case 1:
								operation = "report/summary"
								path += "&view=summary&interval=month"
							case 2:
								operation = "report/csv"
								path += "&format=csv"
							}
							iteration++
						}
						at := time.Now()
						status, err := s.discardGet(path)
						o.record(operation, at, status, err)
					}
				}
			}()
		}
	}
	background.Add(1)
	go func() {
		defer background.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				x := sample(s, start)
				result.Samples = append(result.Samples, x)
				if int(x.Seconds)%30 == 0 {
					o.Lock()
					accepted := len(o.ids)
					errors := len(o.errors)
					o.Unlock()
					writeEvidence(t, name+"-progress", map[string]any{"sample": x, "accepted": accepted, "http_errors": errors})
					t.Logf("progress %s %.0fs accepted=%d queued=%d running=%d completed=%d errors=%d heap=%.1fMiB", name, x.Seconds, accepted, x.Queued, x.Running, x.Succeeded, errors, float64(x.HeapBytes)/(1<<20))
				}
			}
		}
	}()
	// Open arrival schedule. A blocked server never silently reduces offered RPS.
	// Bound the client harness and report missed starts instead of masking them.
	slots := make(chan struct{}, 256)
	for i := 0; i < count; i++ {
		due := start.Add(time.Duration(float64(i) * float64(time.Second) / float64(rate)))
		if wait := time.Until(due); wait > 0 {
			select {
			case <-s.ctx.Done():
				t.Fatal(s.ctx.Err())
			case <-time.After(wait):
			}
		}
		result.Attempted++
		select {
		case slots <- struct{}{}:
		default:
			result.DroppedStarts++
			continue
		}
		requests.Add(1)
		go func(i int) {
			defer requests.Done()
			defer func() { <-slots }()
			at := time.Now()
			status, data, err := s.request("POST", "/v1/generations", `{"model":"mock/test-video","prompt":"measured load"}`, fmt.Sprintf("load-%d", i))
			o.record("admission", at, status, err)
			if status == 202 && err == nil {
				var task struct{ ID string }
				if err := json.Unmarshal(data, &task); err != nil || task.ID == "" {
					o.Lock()
					o.errors = append(o.errors, "invalid accepted task")
					o.Unlock()
					return
				}
				o.Lock()
				o.ids = append(o.ids, task.ID)
				o.Unlock()
			}
		}(i)
	}
	if remaining := time.Until(start.Add(duration)); remaining > 0 {
		time.Sleep(remaining)
	}
	requests.Wait()
	drainStart := time.Now()
	stop()
	background.Wait()
	result.Seconds = time.Since(start).Seconds()
	o.Lock()
	ids := append([]string(nil), o.ids...)
	result.Accepted = len(ids)
	result.HTTPStatuses = o.statuses
	result.Errors = o.errors
	result.AdmissionMS = latencies(o.admission)
	result.ReadMS = latencies(o.read)
	result.ReportMS = latencies(o.report)
	result.ReportByEndpointMS = map[string]latencySummary{}
	for endpoint, samples := range o.reports {
		result.ReportByEndpointMS[endpoint] = latencies(samples)
	}
	o.Unlock()
	if result.Accepted != result.Attempted-result.DroppedStarts {
		result.Errors = append(result.Errors, fmt.Sprintf("started=%d accepted=%d", result.Attempted-result.DroppedStarts, result.Accepted))
	}
	// Drain independently of the offered-load phase, then reconcile every ID.
	submission, publish := []float64{}, []float64{}
	for _, id := range ids {
		task := s.terminal(id)
		if task.Status != core.TaskSucceeded {
			result.Errors = append(result.Errors, "failed task "+id)
			continue
		}
		result.Completed++
		job := s.fixture.job(id)
		if job.Submissions != 1 {
			result.Errors = append(result.Errors, "duplicate submission "+id)
		}
		submission = append(submission, float64(job.Submitted.Sub(task.CreatedAt).Microseconds())/1000)
		publish = append(publish, float64(task.UpdatedAt.Sub(job.Ready).Microseconds())/1000)
		// Hash verification uses real downloads, after load to avoid unlabelled traffic.
		s.checkFile(task)
	}
	result.SubmitDelayMS, result.PublishDelayMS = latencies(submission), latencies(publish)
	if err := s.db.QueryRowContext(s.ctx, "SELECT count(*) FROM tasks WHERE prompt='measured load'").Scan(&result.DatabaseTasks); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	s.fixture.mu.Lock()
	result.ProviderJobs = len(s.fixture.jobs)
	s.fixture.mu.Unlock()
	if result.DatabaseTasks != result.Accepted || result.ProviderJobs != result.Accepted {
		result.Errors = append(result.Errors, fmt.Sprintf("accepted=%d database tasks=%d provider jobs=%d", result.Accepted, result.DatabaseTasks, result.ProviderJobs))
	}
	var reportErr error
	result.VerifiedCSVRows, result.VerifiedSummaryRows, reportErr = verifyFullReports(s, history+result.DatabaseTasks)
	if reportErr != nil {
		result.Errors = append(result.Errors, reportErr.Error())
	}
	result.DrainSeconds = time.Since(drainStart).Seconds()
	runtime.GC()
	result.AfterIdle = sample(s, start)
	if output := os.Getenv("GATEWAY_PERF_OUTPUT"); output != "" {
		file, err := os.Create(filepath.Join(output, name+"-heap.pprof"))
		if err != nil {
			t.Fatal(err)
		}
		err = pprof.WriteHeapProfile(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%s offered=%d accepted=%d completed=%d admission p95=%.2fms p99=%.2fms errors=%d dropped=%d", name, result.Attempted, result.Accepted, result.Completed, result.AdmissionMS.P95, result.AdmissionMS.P99, len(result.Errors), result.DroppedStarts)
	if len(result.Errors) > 0 || result.DroppedStarts > 0 || result.Completed != result.Accepted {
		t.Errorf("load failed integrity/accounting checks; see %s.json", name)
	}
}

func verifyFullReports(s *testStack, expected int) (int, int64, error) {
	path := "/v1/admin/reports?from=2020-01-01&to=2030-01-01"
	r, err := http.NewRequestWithContext(s.ctx, "GET", s.api.URL+path+"&format=csv", nil)
	if err != nil {
		return 0, 0, err
	}
	r.Header.Set("Authorization", "Bearer "+s.key)
	r.Header.Set("User-Agent", "Sociaro-Gateway-Isolated-Reliability/1.0")
	response, err := s.client.Do(r)
	if err != nil {
		return 0, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.ContentLength <= 0 {
		return 0, 0, fmt.Errorf("full CSV status=%d length=%d", response.StatusCode, response.ContentLength)
	}
	reader := csv.NewReader(response.Body)
	reader.ReuseRecord = true
	if _, err := reader.Read(); err != nil {
		return 0, 0, fmt.Errorf("CSV header: %w", err)
	}
	count := 0
	for {
		_, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, 0, fmt.Errorf("CSV row %d: %w", count, err)
		}
		count++
	}
	if count != expected {
		return count, 0, fmt.Errorf("full CSV rows=%d expected=%d", count, expected)
	}
	status, data, err := s.request("GET", path+"&view=summary&interval=month", "", "")
	if err != nil || status != 200 {
		return count, 0, fmt.Errorf("full summary status=%d err=%v", status, err)
	}
	var summary core.ReportSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return count, 0, err
	}
	if summary.Requests != int64(expected) {
		return count, summary.Requests, fmt.Errorf("full summary requests=%d expected=%d", summary.Requests, expected)
	}
	return count, summary.Requests, nil
}

func TestPerformancePendingPolling(t *testing.T) {
	if os.Getenv("GATEWAY_PERF") != "1" {
		t.Skip("set GATEWAY_PERF=1")
	}
	n := perfInt(t, "GATEWAY_PERF_PENDING", 1000)
	d := perfDuration(t, "GATEWAY_PERF_PENDING_DURATION", 10*time.Second)
	s := newStack(t, d+3*time.Minute)
	result := map[string]any{"pending": n, "completed": 0}
	defer func() { writeEvidence(t, "pending-polling", result) }()
	s.fixture.ready = false
	for i := 0; i < n; i++ {
		s.submit("pending-polling", fmt.Sprintf("pending-%d", i), false)
	}
	s.startWorkers(2, time.Second)
	eventually(t, s.ctx, "pending tasks not submitted", func() bool { return len(s.fixture.snapshot()) == n })
	start := time.Now()
	snapshots := []resourceSample{}
	for time.Since(start) < d {
		snapshots = append(snapshots, sample(s, start))
		select {
		case <-s.ctx.Done():
			t.Fatal(s.ctx.Err())
		case <-time.After(time.Second):
		}
	}
	newStart := time.Now()
	id := s.submit("new-during-polling", "new", false)
	eventually(t, s.ctx, "polling backlog prevented submission", func() bool { _, ok := s.fixture.snapshot()[id]; return ok })
	delay := time.Since(newStart).Seconds()
	jobs := s.fixture.snapshot()
	polls := 0
	for _, job := range jobs {
		polls += job.Polls
		if job.Submissions != 1 {
			t.Fatal("duplicate submission")
		}
		if job.Prompt == "pending-polling" && job.Polls < 2 {
			t.Errorf("job was not regularly polled: %+v", job)
		}
	}
	result["duration_seconds"], result["polls"], result["http_connections"] = time.Since(start).Seconds(), polls, s.fixture.connections.Load()
	result["new_submission_seconds"], result["samples"] = delay, snapshots
	s.fixture.release()
	completed := 0
	for id := range jobs {
		s.checkFile(s.terminal(id))
		completed++
		result["completed"] = completed
	}
	result["final"] = sample(s, start)
}
