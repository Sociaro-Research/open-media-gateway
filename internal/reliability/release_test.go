package reliability_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
)

// Only the temporary Go build overlay replaces the mock adapter. The shipped
// worker/gateway entrypoints, configuration, provider manager and persistence
// remain real. Release B adds a free provider to the composition list.
const releaseProviderSource = `package mock
import (
 "context"
 "bytes"
 "encoding/json"
 "fmt"
 "net/http"
 "net/url"
 "github.com/Sociaro-Research/open-media-gateway/internal/core"
 "github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)
func Definition() providerkit.Definition { return releaseDefinition("mock", true) }
func ReleaseDefinition() providerkit.Definition { return releaseDefinition("release-probe", false) }
func releaseDefinition(id string, primary bool) providerkit.Definition {
 return providerkit.Definition{ID:id, Name:id, DefaultModel:"test-video", Free:true, Default:primary, FailureNoCharge:true,
 RuntimeEnv:map[string]string{"fixture_url":"RELEASE_TEST_PROVIDER_URL"},
 Factory:func(_ providerkit.Credentials, s providerkit.Settings, c *http.Client)(core.MediaProvider,error){
  u,e:=url.Parse(s["fixture_url"]); if e!=nil || u.Scheme!="http" || u.Hostname()!="127.0.0.1" { return nil,fmt.Errorf("requires loopback fixture") }
  return &releaseProvider{id:id, base:u.String(), client:c},nil
 }}
}
type releaseProvider struct { id,base string; client *http.Client }
func(p *releaseProvider) Name()string{return p.id}
func(p *releaseProvider) IsFree()bool{return true}
func(p *releaseProvider) FailureIsFree()bool{return true}
func(p *releaseProvider) ListModels(ctx context.Context)([]core.MediaModel,error){models,e:=New(0,"embedded").ListModels(ctx);models[0].ID="test-video";return models[:1],e}
func(p *releaseProvider) call(ctx context.Context,method,path string,in,out any)error{
 b,e:=json.Marshal(in); if e!=nil{return e}; r,e:=http.NewRequestWithContext(ctx,method,p.base+path,bytes.NewReader(b));if e!=nil{return e}
 res,e:=p.client.Do(r);if e!=nil{return e};defer res.Body.Close();if res.StatusCode!=200{return fmt.Errorf("fixture HTTP %d",res.StatusCode)};return json.NewDecoder(res.Body).Decode(out)
}
func(p *releaseProvider) Generate(ctx context.Context,r core.MediaRequest)(core.ProviderTask,error){var out core.ProviderTask;e:=p.call(ctx,"POST","/submit",r,&out);return out,e}
func(p *releaseProvider) GetTask(ctx context.Context,id string)(core.ProviderResult,error){var out core.ProviderResult;e:=p.call(ctx,"GET","/poll?id="+url.QueryEscape(id),nil,&out);return out,e}
func(p *Adapter) DisplayName()string{return "mock"}
func(p *Adapter) IsFree()bool{return true}
func(p *Adapter) FailureIsFree()bool{return true}
`

func releaseBinaries(t *testing.T, ctx context.Context) [2][2]string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	definition := filepath.Join(dir, "definition.go")
	if err = os.WriteFile(definition, []byte(releaseProviderSource), 0600); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(root, "plugins/provider/registry.go")
	registry, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	addition := bytes.Replace(registry, []byte("mock.Definition(),"), []byte("mock.Definition(), mock.ReleaseDefinition(),"), 1)
	if bytes.Equal(registry, addition) {
		t.Fatal("provider composition insertion point changed")
	}
	registryB := filepath.Join(dir, "registry.go")
	if err = os.WriteFile(registryB, addition, 0600); err != nil {
		t.Fatal(err)
	}
	var binaries [2][2]string
	for release := range binaries {
		replace := map[string]string{filepath.Join(root, "plugins/provider/mock/definition.go"): definition}
		if release == 1 {
			replace[registryPath] = registryB
		}
		overlay := filepath.Join(dir, fmt.Sprintf("overlay-%d.json", release))
		body, _ := json.Marshal(map[string]any{"Replace": replace})
		if err = os.WriteFile(overlay, body, 0600); err != nil {
			t.Fatal(err)
		}
		for component, name := range []string{"gateway", "worker"} {
			binary := filepath.Join(dir, fmt.Sprintf("%s-%d", name, release))
			cmd := exec.CommandContext(ctx, "go", "build", "-race", "-overlay", overlay, "-o", binary, "./cmd/"+name)
			cmd.Dir = root
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build release %d %s: %v\n%s", release, name, err, output)
			}
			binaries[release][component] = binary
		}
	}
	for component := range 2 {
		a, _ := os.ReadFile(binaries[0][component])
		b, _ := os.ReadFile(binaries[1][component])
		if sha256.Sum256(a) == sha256.Sum256(b) {
			t.Fatal("release binaries must differ after adding the provider")
		}
	}
	return binaries
}

type releaseProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	log  string
}

func startReleaseProcess(t *testing.T, binary string, env []string) *releaseProcess {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "process.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	p := &releaseProcess{cmd: exec.Command(binary), done: make(chan struct{}), log: logPath}
	p.cmd.Env, p.cmd.Stdout, p.cmd.Stderr = env, log, log
	if err = p.cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); log.Close(); close(p.done) }()
	t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	})
	return p
}

func (p *releaseProcess) stop(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
}

func (p *releaseProcess) wait(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		if p.err != nil {
			body, _ := os.ReadFile(p.log)
			t.Fatalf("release process failed: %v\n%s", p.err, body)
		}
	case <-time.After(15 * time.Second):
		body, _ := os.ReadFile(p.log)
		t.Fatalf("release process did not drain\n%s", body)
	}
}

func TestReliabilityReleaseWithActiveTasks(t *testing.T) {
	s := newStack(t, 5*time.Minute)
	binaries := releaseBinaries(t, s.ctx)
	s.fixture.ready = false
	s.fixture.submitGate = make(chan struct{})
	s.fixture.archiveAfterChunk = make(chan struct{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	base := "http://127.0.0.1:" + port
	// Do not inherit external provider credentials or a production configuration.
	var env []string
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "GORACE"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, "DATABASE_URL="+s.dsn, "REDIS_URL="+s.redisURL, "PORT="+port,
		"CLIENT_API_KEY="+s.key, "WEBHOOK_SIGNING_SECRET=release-fixture-webhooks",
		"MEDIA_URL_SIGNING_SECRET=release-fixture-signing", "PROVIDER_CREDENTIALS_ENCRYPTION_KEY=release-fixture-cipher",
		"STORAGE_LOCAL_PATH="+s.root, "MEDIA_PROVIDER=mock", "MIGRATE=auto",
		"RELEASE_TEST_PROVIDER_URL="+s.fixture.server.URL, "DISPATCH_INTERVAL=20ms",
		"OUTBOUND_ALLOW_PRIVATE_HOSTS=127.0.0.1",
		"PROVIDER_POLL_INTERVAL=20ms", "PROVIDER_HTTP_TIMEOUT=1m", "WORKER_SHUTDOWN_TIMEOUT=40s")
	request := func(method, path string, input any) (int, []byte) {
		t.Helper()
		var body io.Reader
		if input != nil {
			data, _ := json.Marshal(input)
			body = bytes.NewReader(data)
		}
		r, _ := http.NewRequestWithContext(s.ctx, method, base+path, body)
		r.Header.Set("Authorization", "Bearer "+s.key)
		r.Header.Set("User-Agent", "Sociaro-Isolated-Release-Test")
		if input != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		res, err := s.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, data
	}
	ready := func(process *releaseProcess) {
		eventually(t, s.ctx, "gateway never healthy", func() bool {
			select {
			case <-process.done:
				body, _ := os.ReadFile(process.log)
				t.Fatalf("gateway exited before readiness: %v\n%s", process.err, body)
			default:
			}
			res, err := s.client.Get(base + "/healthz")
			if err != nil {
				return false
			}
			res.Body.Close()
			return res.StatusCode == 200
		})
	}
	submit := func(prompt, model string) string {
		status, body := request("POST", "/v1/generations", map[string]string{"model": model, "prompt": prompt})
		var result struct{ ID string }
		if status != 202 || json.Unmarshal(body, &result) != nil || result.ID == "" {
			t.Fatalf("release admission: %d %s", status, body)
		}
		return result.ID
	}
	gatewayA := startReleaseProcess(t, binaries[0][0], env)
	ready(gatewayA)
	workerA := startReleaseProcess(t, binaries[0][1], append(env, "MIGRATE=off"))
	ids := make([]string, 0, 35)
	for range 20 {
		ids = append(ids, submit("pending", "mock/test-video"))
	}
	eventually(t, s.ctx, "pending jobs not durable", func() bool {
		var n int
		_ = s.db.QueryRow(`SELECT count(*) FROM tasks WHERE provider_task_id IS NOT NULL`).Scan(&n)
		return n == 20
	})
	archiveID := submit("release-archive", "mock/test-video")
	ids = append(ids, archiveID)
	eventually(t, s.ctx, "archive never partially written", func() bool {
		found := false
		_ = filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && strings.HasPrefix(entry.Name(), ".media-") && strings.HasSuffix(entry.Name(), ".part") {
				info, _ := entry.Info()
				found = found || info != nil && info.Size() > 0
			}
			return nil
		})
		return found
	})
	submissionID := submit("release-submit", "mock/test-video")
	ids = append(ids, submissionID)
	eventually(t, s.ctx, "submission never reached provider", func() bool { return s.fixture.job(submissionID).Submissions == 1 })
	synchronousID := submit("release-sync", "mock/test-video")
	ids = append(ids, synchronousID)
	eventually(t, s.ctx, "synchronous generation never reached provider", func() bool { return s.fixture.job(synchronousID).Submissions == 1 })
	var leaseBefore time.Time
	if err := s.db.QueryRow(`SELECT worker_lease_until FROM tasks WHERE id=$1`, submissionID).Scan(&leaseBefore); err != nil {
		t.Fatal(err)
	}
	workerA.stop(t)
	eventually(t, s.ctx, "worker did not enter drain", func() bool {
		body, _ := os.ReadFile(workerA.log)
		return bytes.Contains(body, []byte("worker draining active operations"))
	})
	var queued []string
	for range 10 {
		id := submit("pending", "mock/test-video")
		ids, queued = append(ids, id), append(queued, id)
	}
	for _, id := range queued {
		task, _ := s.repo.Get(s.ctx, id)
		if task.Status != core.TaskQueued || s.fixture.job(id).Submissions != 0 {
			t.Fatal("draining worker accepted a new operation", id)
		}
	}
	// Protect the in-flight request beyond a heartbeat interval during shutdown.
	eventually(t, s.ctx, "draining worker stopped renewing ownership", func() bool {
		var lease time.Time
		err := s.db.QueryRow(`SELECT worker_lease_until FROM tasks WHERE id=$1`, submissionID).Scan(&lease)
		return err == nil && lease.After(leaseBefore)
	})
	select {
	case <-workerA.done:
		t.Fatal("worker exited before the protected provider response arrived")
	default:
	}
	close(s.fixture.submitGate)
	eventually(t, s.ctx, "shutdown cancelled the provider response", func() bool {
		task, _ := s.repo.Get(s.ctx, submissionID)
		return task.ProviderTaskID != nil
	})
	close(s.fixture.archiveAfterChunk)
	workerA.wait(t)
	if task, _ := s.repo.Get(s.ctx, synchronousID); task.Status != core.TaskSucceeded || s.fixture.job(synchronousID).Polls != 0 {
		t.Fatal("synchronous provider response must finish archival before the old worker exits", task.Status)
	}
	// Long-lived upstream jobs remain in progress: draining did not wait for them.
	if task, _ := s.repo.Get(s.ctx, ids[0]); task.Status != core.TaskRunning {
		t.Fatal("drain must not wait for provider job completion", task.Status)
	}
	gatewayA.stop(t)
	gatewayA.wait(t)
	gatewayB := startReleaseProcess(t, binaries[1][0], env)
	ready(gatewayB)
	workerB := startReleaseProcess(t, binaries[1][1], append(env, "MIGRATE=off"))
	s.fixture.release()
	ids = append(ids, submit("fast", "mock/test-video"), submit("fast", "release-probe/test-video"))
	for _, id := range ids {
		task := s.terminal(id)
		if task.Status != core.TaskSucceeded {
			t.Fatalf("release lost/failed accepted task %s: %v", id, task.Error)
		}
		status, body := request("GET", "/v1/generations/"+id+"/content", nil)
		want, got := sha256.Sum256(s.fixture.media), sha256.Sum256(body)
		if status != 200 || got != want || task.ResultSHA256 == nil || *task.ResultSHA256 != hex.EncodeToString(want[:]) {
			t.Fatal("release corrupted a result", id)
		}
		if s.fixture.job(id).Submissions != 1 {
			t.Fatal("release repeated a provider submission", id)
		}
	}
	var tasks, failed, pending int
	if err := s.db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE status IN ('queued','running')), count(*) FILTER (WHERE status='failed') FROM tasks`).Scan(&tasks, &pending, &failed); err != nil {
		t.Fatal(err)
	}
	if tasks != len(ids) || pending != 0 || failed != 0 || len(s.fixture.snapshot()) != len(ids) {
		t.Fatalf("release reconciliation: tasks=%d pending=%d failed=%d providers=%d expected=%d", tasks, pending, failed, len(s.fixture.snapshot()), len(ids))
	}
	workerB.stop(t)
	workerB.wait(t)
	gatewayB.stop(t)
	gatewayB.wait(t)
	t.Logf("two distinct release builds, new provider, %d tasks reconciled: queued/polling/submission/synchronous result/partial archive survived SIGTERM; all downloads intact, no duplicate submissions", len(ids))
}

func TestReliabilityShutdownDeadlineIsExplicit(t *testing.T) {
	s := newStack(t, 15*time.Second)
	s.fixture.blockSubmit = true
	ctx, stop := context.WithCancel(s.ctx)
	w := worker.New(s.repo, s.q, s.provider, s.store, 20*time.Millisecond, time.Minute, 3, 20*time.Millisecond)
	w.ShutdownTimeout = 100 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	id := s.submit("blocked-shutdown", "blocked-shutdown", false)
	eventually(t, s.ctx, "submission not started", func() bool { return s.fixture.job(id).Submissions == 1 })
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, worker.ErrShutdownTimeout) {
			t.Fatal("shutdown timeout was hidden", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown deadline did not cancel active HTTP work")
	}
	s.fixture.mu.Lock()
	s.fixture.blockSubmit = false
	s.fixture.mu.Unlock()
	s.startWorkers(1, 20*time.Millisecond)
	task := s.terminal(id)
	if task.Status != core.TaskFailed || task.Error == nil || !strings.Contains(*task.Error, "not resubmitted") || s.fixture.job(id).Submissions != 1 {
		t.Fatal("deadline must preserve explicit ambiguity without resubmission", task)
	}
}
