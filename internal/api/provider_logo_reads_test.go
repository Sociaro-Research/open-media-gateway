package api

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type logoKey struct{ owner, id string }

type logoReadRepository struct {
	records map[logoKey]providerconfig.Record
	blobs   map[logoKey][]byte
	lists   int
	reads   []string
}

func managedLogoFixture(s *Server, blobs map[logoKey][]byte) *logoReadRepository {
	repo := &logoReadRepository{records: make(map[logoKey]providerconfig.Record), blobs: blobs}
	for key := range blobs {
		repo.records[key] = providerconfig.Record{ProviderID: key.id, Enabled: true}
	}
	manager := providers.NewManager(repo, nil, config.Config{})
	service := core.NewTaskService(nil, nil, "mock", "mock-video-v1")
	service.SetProviderDirectory(manager)
	s.service, s.providerAdmin = service, manager
	return repo
}

func (s *logoReadRepository) Get(ctx context.Context, id string) (providerconfig.Record, error) {
	record, ok := s.records[logoKey{identity.ProviderOwnerID(ctx), id}]
	if !ok {
		return providerconfig.Record{}, providerconfig.ErrNotFound
	}
	return record, nil
}
func (s *logoReadRepository) Upsert(ctx context.Context, record providerconfig.Record) error {
	s.records[logoKey{identity.ProviderOwnerID(ctx), record.ProviderID}] = record
	return nil
}
func (s *logoReadRepository) List(ctx context.Context) ([]providerconfig.Record, error) {
	s.lists++
	var records []providerconfig.Record
	for key, record := range s.records {
		if key.owner == identity.ProviderOwnerID(ctx) {
			records = append(records, record)
		}
	}
	return records, nil
}
func (s *logoReadRepository) Logo(ctx context.Context, id string) ([]byte, error) {
	s.reads = append(s.reads, id)
	blob, ok := s.blobs[logoKey{identity.ProviderOwnerID(ctx), id}]
	if !ok {
		return nil, providerconfig.ErrNotFound
	}
	return blob, nil
}
func (s *logoReadRepository) SaveLogo(context.Context, string, []byte) error {
	panic("unexpected logo write")
}

type logoReadModelAccess struct {
	modelAccessMemory
	refs int
}

func (m *logoReadModelAccess) Ensure(ctx context.Context, refs []modelaccess.Ref) error {
	m.refs += len(refs)
	return m.modelAccessMemory.Ensure(ctx, refs)
}

func TestSharedProviderLogoReads(t *testing.T) {
	for _, count := range []int{1, 10, 30} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ids := []string{"mock"}
			for i := 1; i < count; i++ {
				ids = append(ids, fmt.Sprintf("mock--fixture-%d", i))
			}
			blobs := map[logoKey][]byte{}
			for _, id := range ids {
				blobs[logoKey{identity.SharedOwnerID, id}] = []byte(id)
			}
			s, _ := newPersonalTestServer()
			repo := managedLogoFixture(s, blobs)
			s.system = &systemFixture{settings: system.Settings{SharedResources: true}}
			policy := &logoReadModelAccess{}
			s.modelAccess = policy
			handler := s.handler()
			for _, id := range ids {
				path := "/provider-assets/" + identity.SharedOwnerID + "/" + id + "/icon.png"
				w := sendTest(handler, "GET", path, "", "bob", "")
				if w.Code != 200 || w.Body.String() != id || w.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatalf("GET %s: status=%d body=%q cache=%q", id, w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
				}
			}
			t.Logf("requests=%d directory_lists=%d logo_blob_reads=%d ensured_model_refs=%d", count, repo.lists, len(repo.reads), policy.refs)
			if repo.lists != 0 || !slices.Equal(repo.reads, ids) {
				t.Errorf("want no directory lists and one read of each requested blob")
			}
			if policy.refs != len(policy.models) {
				t.Errorf("registered %d model references for %d distinct models", policy.refs, len(policy.models))
			}
			// Read the exact same URL after the connection becomes unavailable.
			repo.records[logoKey{identity.SharedOwnerID, "mock"}] = providerconfig.Record{ProviderID: "mock", Archived: true}
			reads := len(repo.reads)
			path := "/provider-assets/" + identity.SharedOwnerID + "/mock/icon.png"
			if w := sendTest(handler, "GET", path, "", "bob", ""); w.Code != 404 || len(repo.reads) != reads {
				t.Errorf("archived connection: status=%d additional reads=%d", w.Code, len(repo.reads)-reads)
			}
		})
	}
}
