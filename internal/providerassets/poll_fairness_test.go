package providerassets

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

type pollingRepository struct {
	Repository
	mu           *sync.Mutex
	rows         []Record
	batches      [][]string
	saves        int
	wrongContext bool
	cursors      []string
	queryTimes   []time.Time
	queryErrorAt int
	saveErrorFor string
}

func (r *pollingRepository) Pending(ctx context.Context, limit int, afterID string) ([]Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.cursors = append(r.cursors, afterID)
	r.queryTimes = append(r.queryTimes, time.Now())
	if len(r.cursors) == r.queryErrorAt {
		return nil, errors.New("synthetic query failure")
	}
	rows := []Record{}
	for _, row := range r.rows {
		if row.ID <= afterID {
			continue
		}
		if row.Status == "Processing" || row.Status == "Creating" || row.Status == "Unknown" {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	ids := []string{}
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	r.batches = append(r.batches, ids)
	return rows, nil
}

func (r *pollingRepository) Save(ctx context.Context, next Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saves++
	if identity.OwnerID(ctx) != next.Owner || identity.ProviderOwnerID(ctx) != next.ProviderOwner {
		r.wrongContext = true
		return ErrConflict
	}
	if next.Owner == r.saveErrorFor {
		return errors.New("synthetic Save failure")
	}
	for i, old := range r.rows {
		if old.ID != next.ID {
			continue
		}
		if old.Status == "Deleting" || old.Status == "Deleted" || old.Status == "Detached" || old.Asset.ID != "" && old.Asset.ID != next.Asset.ID {
			return ErrConflict
		}
		next.UpdatedAt = time.Now()
		r.rows[i] = next
		return nil
	}
	return ErrNotFound
}

func TestBackgroundPollingCompletesLapsWhileProcessingAndRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, repo, bad, good := pollingFixture("get")
		good.status = "Processing"
		for i := 26; i <= 49; i++ {
			r := repo.rows[24]
			r.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
			r.Asset.ID = fmt.Sprintf("native-%d", i)
			repo.rows = append(repo.rows, r)
		}
		before := append([]Record{}, repo.rows[:24]...)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go service.Run(ctx)
		time.Sleep(21 * time.Second) // One full lap and the first batch of the next.
		synctest.Wait()
		repo.inspect(func() {
			if bad.getCalls != 48 || good.getCalls != 25 {
				t.Fatalf("lap did not revisit failures: bad=%d healthy=%d", bad.getCalls, good.getCalls)
			}
			if !reflect.DeepEqual(before, repo.rows[:24]) {
				t.Fatal("poll errors rewrote stored state")
			}
			bad.getError = nil
		})
		time.Sleep(15 * time.Second)
		synctest.Wait()
		for _, row := range repo.rows[:24] {
			if row.Status != "Active" || row.Asset.ID == "" {
				t.Fatal("previously failed account did not recover", row.ID, row.Status)
			}
		}
		for _, row := range repo.rows[24:] {
			if row.Status != "Processing" {
				t.Fatal("polling invented terminal success", row.ID, row.Status)
			}
		}
		assertPollingBounds(t, repo)
		cancel()
		synctest.Wait()
		if bad.creates+good.creates != 0 || bad.wrongContext+good.wrongContext != 0 || repo.wrongContext {
			t.Fatal("Create resubmission or crossed account context")
		}
	})
}

func TestBackgroundPollingRetainsCursorAfterQueryFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, repo, _, good := pollingFixture("get")
		repo.queryErrorAt = 2
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go service.Run(ctx)
		time.Sleep(16 * time.Second)
		synctest.Wait()
		if good.getCalls != 1 || len(repo.cursors) != 3 || repo.cursors[1] != repo.rows[23].ID || repo.cursors[2] != repo.cursors[1] {
			t.Fatal("query failure lost sweep position", good.getCalls, repo.cursors)
		}
		cancel()
		synctest.Wait()
	})
}

func TestBackgroundPollingInsertionAndTerminalChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, repo, bad, good := pollingFixture("get")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go service.Run(ctx)
		time.Sleep(6 * time.Second)
		synctest.Wait()
		// A new lower ID waits for wrap; a higher one joins this lap. Neither
		// updated_at ordering nor a removed next-page row can stall the sweep.
		repo.inspect(func() {
			lower, higher := repo.rows[24], repo.rows[24]
			lower.ID, lower.Asset.ID = "00000000-0000-4000-8000-000000000000", "native-low"
			higher.ID, higher.Asset.ID = "00000000-0000-4000-8000-000000000026", "native-high"
			repo.rows = append(repo.rows, lower, higher)
			repo.rows[24].Status = "Deleted"
		})
		time.Sleep(5 * time.Second)
		synctest.Wait()
		repo.inspect(func() {
			if repo.rows[25].Status != "Processing" || repo.rows[26].Status != "Active" || repo.rows[24].Status != "Deleted" {
				t.Fatal("new/terminal rows not handled in current lap")
			}
		})
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if repo.rows[25].Status != "Active" || good.getCalls != 2 || bad.getCalls != 47 {
			t.Fatal("lower ID not visited after wrap", good.getCalls, bad.getCalls)
		}
		cancel()
		synctest.Wait()
	})
}

func TestBackgroundPollingCancellation(t *testing.T) {
	for _, when := range []string{"before-first-tick", "during-request"} {
		t.Run(when, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				service, repo, bad, good := pollingFixture("get")
				bad.blockGet = true
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan struct{})
				go func() { service.Run(ctx); close(done) }()
				if when == "during-request" {
					time.Sleep(6 * time.Second)
					synctest.Wait()
					if bad.getCalls != 1 {
						t.Fatal("request did not start", bad.getCalls)
					}
				}
				cancel()
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Fatal("Run did not stop on cancellation")
				}
				if good.getCalls != 0 || repo.saves != 0 || bad.creates != 0 {
					t.Fatal("cancellation started more work or saved failure")
				}
			})
		})
	}
}

func TestBackgroundPollingExistingOutcomes(t *testing.T) {
	for _, outcome := range []string{"success", "not-found", "no-match", "unavailable", "scope-mismatch", "save-error"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				service, repo, bad, good := pollingFixture("get")
				repo.rows = []Record{repo.rows[0], repo.rows[24]}
				repo.rows[0].SourceID, repo.rows[0].Name = "source-fixture", "Human label"
				before := repo.rows[0]
				bad.getError = nil
				want := "Active"
				switch outcome {
				case "not-found":
					bad.getError, want = ErrNotFound, "Missing"
				case "no-match":
					repo.rows[0].Asset.ID, repo.rows[0].Status = "", "Unknown"
					bad.emptyList, want = true, "Unknown"
				case "unavailable":
					delete(service.Directory.(pollingDirectory), bad.owner)
					want = "Disconnected"
				case "scope-mismatch":
					bad.scope, want = "different-scope", "Disconnected"
				case "save-error":
					repo.saveErrorFor, want = bad.owner, "Processing"
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go service.Run(ctx)
				time.Sleep(6 * time.Second)
				synctest.Wait()
				if repo.rows[0].Status != want || repo.rows[1].Status != "Active" || good.getCalls != 1 {
					t.Fatal("changed polling outcome", repo.rows[0].Status, repo.rows[1].Status)
				}
				if outcome == "save-error" && !reflect.DeepEqual(before, repo.rows[0]) {
					t.Fatal("failed Save changed stored state")
				}
				if outcome != "no-match" && repo.rows[0].Asset.ID != before.Asset.ID || repo.rows[0].RegistrationName != before.RegistrationName || repo.rows[0].Name != before.Name {
					t.Fatal("lost accepted ID/registration identity")
				}
				cancel()
				synctest.Wait()
				if bad.creates+good.creates != 0 || bad.wrongContext+good.wrongContext != 0 || repo.wrongContext {
					t.Fatal("Create resubmission or crossed account context")
				}
			})
		})
	}
}

type pollingClient struct {
	Client
	mu                          *sync.Mutex
	owner, providerOwner, scope string
	getError, listError         error
	getCalls, listCalls         int
	creates, wrongContext       int
	status                      string
	emptyList, blockGet         bool
}

func (c *pollingClient) Scope() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scope
}
func (c *pollingClient) check(ctx context.Context) {
	if identity.OwnerID(ctx) != c.owner || identity.ProviderOwnerID(ctx) != c.providerOwner {
		c.wrongContext++
	}
}
func (c *pollingClient) Get(ctx context.Context, id string) (Asset, error) {
	c.mu.Lock()
	c.check(ctx)
	c.getCalls++
	block, status, err := c.blockGet, c.status, c.getError
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return Asset{}, ctx.Err()
	}
	if status == "" {
		status = "Active"
	}
	return Asset{ID: id, Status: status}, err
}
func (c *pollingClient) List(ctx context.Context, group, next, name string) (Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.check(ctx)
	c.listCalls++
	if c.emptyList {
		return Page{}, c.listError
	}
	return Page{Items: []Asset{{ID: "accepted-" + name, GroupID: group, Name: name, Status: "Active"}}}, c.listError
}
func (c *pollingClient) Create(context.Context, string, string, string, string) (Asset, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creates++
	return Asset{}, errors.New("background polling must never Create")
}

type pollingDirectory map[string]*pollingClient

func (d pollingDirectory) AssetLibrary(ctx context.Context, _ string) (Client, error) {
	client := d[identity.OwnerID(ctx)]
	if client == nil {
		return nil, ErrUnavailable
	}
	return client, nil
}

func pollingFixture(mode string) (*Service, *pollingRepository, *pollingClient, *pollingClient) {
	mu := &sync.Mutex{}
	repo := &pollingRepository{mu: mu}
	bad := &pollingClient{mu: mu, owner: "owner-a", providerOwner: "account-a", scope: "scope-a"}
	good := &pollingClient{mu: mu, owner: "owner-b", providerOwner: "account-b", scope: "scope-b"}
	for i := 1; i <= 25; i++ {
		client := bad
		if i == 25 {
			client = good
		}
		r := Record{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Owner: client.owner, ProviderOwner: client.providerOwner, Provider: "fixture", Scope: client.scope, UpdatedAt: time.Now().Add(-time.Hour + time.Duration(i)*time.Second), Asset: Asset{ID: fmt.Sprintf("native-%d", i), Status: "Processing", RegistrationName: fmt.Sprintf("registration-%d", i)}}
		if mode == "list" {
			r.Asset.ID = ""
			r.Status = "Unknown"
		}
		repo.rows = append(repo.rows, r)
	}
	if mode == "get" {
		bad.getError = errors.New("persistent synthetic Get failure")
	} else {
		bad.listError = errors.New("persistent synthetic List failure")
	}
	return New(repo, pollingDirectory{bad.owner: bad, good.owner: good}), repo, bad, good
}

func (r *pollingRepository) inspect(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn()
}

func TestBackgroundPollingIndependentAccountProgress(t *testing.T) {
	for _, mode := range []string{"get", "list"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				service, repo, bad, good := pollingFixture(mode)
				before := append([]Record{}, repo.rows[:24]...)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go service.Run(ctx)
				time.Sleep(11 * time.Second) // Two real Run ticks, virtual test time.
				synctest.Wait()
				if repo.rows[24].Status != "Active" {
					t.Errorf("independent account still %s after two batches; bad Get/List=%d/%d, healthy Get/List=%d/%d, batches=%v", repo.rows[24].Status, bad.getCalls, bad.listCalls, good.getCalls, good.listCalls, repo.batches)
				}
				if !reflect.DeepEqual(before, repo.rows[:24]) {
					t.Fatal("failed polling must not rotate timestamps or invent saved success")
				}
				for _, batch := range repo.batches {
					if len(batch) > 24 {
						t.Fatal("unbounded batch", len(batch))
					}
				}
				assertPollingBounds(t, repo)
				cancel()
				synctest.Wait()
				if bad.creates+good.creates != 0 || bad.wrongContext+good.wrongContext != 0 || repo.wrongContext {
					t.Fatal("polling resubmitted Create or crossed account context")
				}
			})
		})
	}
}

func assertPollingBounds(t *testing.T, repo *pollingRepository) {
	t.Helper()
	queries := map[time.Time]int{}
	for _, stamp := range repo.queryTimes {
		queries[stamp]++
		if queries[stamp] > 2 {
			t.Fatal("more than two Pending queries in one tick")
		}
	}
	for _, batch := range repo.batches {
		if len(batch) > 24 {
			t.Fatal("more than 24 refresh candidates in one tick")
		}
	}
}

func TestBackgroundPollingEmptySweepIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, repo, bad, good := pollingFixture("get")
		for i := range repo.rows {
			repo.rows[i].Status = "Deleted"
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go service.Run(ctx)
		time.Sleep(26 * time.Second)
		synctest.Wait()
		if len(repo.cursors) != 5 || bad.getCalls+good.getCalls+repo.saves != 0 {
			t.Fatal("empty sweep spun or touched terminal rows", repo.cursors)
		}
		cancel()
		synctest.Wait()
	})
}
