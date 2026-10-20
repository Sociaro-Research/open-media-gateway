package providerassets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/google/uuid"
)

type Service struct {
	Repo      Repository
	Directory Directory
	mu        sync.Mutex
	cache     map[string]cached
}
type cached struct {
	asset Asset
	at    time.Time
}

func New(repo Repository, d Directory) *Service {
	return &Service{Repo: repo, Directory: d, cache: map[string]cached{}}
}
func (s *Service) client(ctx context.Context, p string) (Client, error) {
	return s.Directory.AssetLibrary(ctx, p)
}
func (s *Service) Groups(ctx context.Context, p string) ([]Group, error) {
	c, err := s.client(ctx, p)
	if err != nil {
		return nil, err
	}
	return s.Repo.Groups(ctx, p, c.Scope())
}
func (s *Service) CreateGroup(ctx context.Context, p, name string) (Group, error) {
	c, err := s.client(ctx, p)
	if err != nil {
		return Group{}, err
	}
	g, err := c.CreateGroup(ctx, name)
	if err != nil {
		return g, err
	}
	return g, s.Repo.SaveGroup(ctx, p, c.Scope(), g)
}
func (s *Service) Inventory(ctx context.Context, p, group, next string) (Page, error) {
	c, err := s.client(ctx, p)
	if err != nil {
		return Page{}, err
	}
	return c.List(ctx, group, next, "")
}
func (s *Service) InventoryGroups(ctx context.Context, p string) ([]Group, error) {
	c, err := s.client(ctx, p)
	if err != nil {
		return nil, err
	}
	return c.Groups(ctx)
}
func (s *Service) Register(ctx context.Context, p, sourceID, sourceURL, name, kind, group string) (Record, error) {
	c, err := s.client(ctx, p)
	if err != nil {
		return Record{}, err
	}
	groups, err := s.Repo.Groups(ctx, p, c.Scope())
	if err != nil {
		return Record{}, err
	}
	found := false
	for _, g := range groups {
		if g.ID == group {
			found = true
		}
	}
	if !found {
		return Record{}, fmt.Errorf("%w: select a group in this workspace or create one", ErrInvalid)
	}
	id := uuid.NewString()
	label := []rune(name)
	if len(label) > 40 {
		label = label[:40]
	}
	remoteName := string(label) + "-" + id[:12]
	r := Record{ID: id, Owner: identity.OwnerID(ctx), Actor: identity.ActorID(ctx), ProviderOwner: identity.ProviderOwnerID(ctx), Provider: p, Scope: c.Scope(), SourceID: sourceID, Asset: Asset{GroupID: group, Name: name, Type: kind, Status: "Creating", RegistrationName: remoteName}}
	r, created, err := s.Repo.Reserve(ctx, r)
	if err != nil || !created {
		return r, err
	}
	a, err := c.Create(ctx, group, sourceURL, remoteName, kind)
	// A timed-out create may have succeeded. Keep its unique name and reconcile
	// by listing; never repeat the non-idempotent provider call automatically.
	if err != nil {
		r.Status = "Unknown"
		r.Failure = err.Error()
		if errors.Is(err, ErrRejected) {
			r.Status = "Failed"
		}
		_ = s.saveDetached(ctx, r)
		return r, nil
	}
	a.Name = name
	a.RegistrationName = remoteName
	r.Asset = a
	if err = s.saveDetached(ctx, r); errors.Is(err, ErrConflict) {
		return s.Repo.Get(ctx, r.ID)
	}
	return r, err
}
func (s *Service) Import(ctx context.Context, p, id string) (Record, error) {
	c, err := s.client(ctx, p)
	if err != nil {
		return Record{}, err
	}
	a, err := c.Get(ctx, id)
	if err != nil {
		return Record{}, err
	}
	groups, err := c.Groups(ctx)
	if err != nil {
		return Record{}, err
	}
	var group Group
	for _, g := range groups {
		if g.ID == a.GroupID {
			group = g
		}
	}
	if group.ID == "" {
		return Record{}, fmt.Errorf("%w: use a virtual portrait asset", ErrInvalid)
	}
	if err = s.Repo.SaveGroup(ctx, p, c.Scope(), group); err != nil {
		return Record{}, err
	}
	r := Record{ID: uuid.NewString(), Owner: identity.OwnerID(ctx), Actor: identity.ActorID(ctx), ProviderOwner: identity.ProviderOwnerID(ctx), Provider: p, Scope: c.Scope(), Asset: a}
	r, _, err = s.Repo.Reserve(ctx, r)
	return r, err
}
func (s *Service) Get(ctx context.Context, p, id string) (Record, error) {
	r, err := s.Repo.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Provider != p || (r.Status == "Deleted" || r.Status == "Detached") {
		return r, ErrNotFound
	}
	c, err := s.client(ctx, p)
	if err != nil {
		return r, err
	}
	if c.Scope() != r.Scope {
		return r, ErrNotFound
	}
	return s.refresh(ctx, c, r)
}
func (s *Service) List(ctx context.Context, p string, offset int) ([]Record, bool, error) {
	c, err := s.client(ctx, p)
	if err != nil {
		return nil, false, err
	}
	items, err := s.Repo.List(ctx, p, c.Scope(), offset)
	if err != nil {
		return nil, false, err
	}
	more := len(items) > 48
	if more {
		items = items[:48]
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 6)
	for i := range items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if r, e := s.refresh(ctx, c, items[i]); e == nil {
				items[i] = r
			}
		}(i)
	}
	wg.Wait()
	return items, more, nil
}
func (s *Service) refresh(ctx context.Context, c Client, r Record) (Record, error) {
	if r.Status == "Deleting" || r.Status == "Deleted" || r.Status == "Detached" || r.Status == "Failed" && r.Asset.ID == "" {
		return r, nil
	}
	s.mu.Lock()
	hit, ok := s.cache[r.ID]
	s.mu.Unlock()
	ttl := time.Minute
	if r.Status != "Active" {
		ttl = 3 * time.Second
	}
	if ok && time.Since(hit.at) < ttl {
		r.Asset = hit.asset
		return r, nil
	}
	var a Asset
	var err error
	if r.Asset.ID != "" {
		a, err = c.Get(ctx, r.Asset.ID)
	} else if r.RegistrationName != "" {
		var page Page
		page, err = c.List(ctx, r.GroupID, "", r.RegistrationName)
		if err == nil {
			for _, candidate := range page.Items {
				if candidate.Name == r.RegistrationName {
					if a.ID != "" {
						return r, ErrConflict
					}
					a = candidate
				}
			}
		}
		if a.ID == "" {
			if err == nil {
				r.Status = "Unknown"
				err = s.Repo.Save(ctx, r)
			}
			return r, err
		}
	} else {
		return r, nil
	}
	if errors.Is(err, ErrNotFound) {
		r.Status = "Missing"
		return r, s.Repo.Save(ctx, r)
	}
	if err != nil {
		return r, err
	}
	a.RegistrationName = r.RegistrationName
	if r.SourceID != "" {
		a.Name = r.Name
	}
	r.Asset = a
	if err = s.Repo.Save(ctx, r); err != nil {
		return r, err
	}
	s.mu.Lock()
	if len(s.cache) > 2000 {
		s.cache = map[string]cached{}
	}
	s.cache[r.ID] = cached{a, time.Now()}
	s.mu.Unlock()
	return r, nil
}
func (s *Service) Delete(ctx context.Context, p, id string) error {
	r, err := s.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if r.Provider != p || r.Status == "Detached" {
		return ErrNotFound
	}
	c, err := s.client(ctx, p)
	if err != nil {
		return err
	}
	if c.Scope() != r.Scope {
		return ErrNotFound
	}
	r, err = s.Repo.BeginDelete(ctx, id)
	if err != nil {
		return err
	}
	if r.Asset.ID == "" && r.Status == "Deleted" {
		return nil
	}
	if err = c.Delete(ctx, r.Asset.ID); err != nil {
		return err
	}
	r.Status = "Deleted"
	s.mu.Lock()
	delete(s.cache, r.ID)
	s.mu.Unlock()
	return s.saveDetached(ctx, r)
}

// Detach removes only the current workspace binding, never the remote file.
func (s *Service) Detach(ctx context.Context, p, id string) error {
	r, err := s.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	c, err := s.client(ctx, p)
	if err != nil {
		return err
	}
	if r.Provider != p || c.Scope() != r.Scope {
		return ErrNotFound
	}
	if r.Status == "Creating" || r.Status == "Unknown" || r.Status == "Processing" || r.Status == "Deleting" {
		return ErrConflict
	}
	if err = s.Repo.Detach(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
	return nil
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := s.Repo.Pending(ctx, 24)
			if err != nil {
				continue
			}
			for _, r := range rows {
				if ctx.Err() != nil {
					return
				}
				work, cancel := context.WithTimeout(ctx, 20*time.Second)
				work = identity.WithResourceOwners(identity.WithOwnerID(work, r.Owner), r.ProviderOwner, r.ProviderOwner)
				c, err := s.client(work, r.Provider)
				if err == nil && c.Scope() == r.Scope {
					_, _ = s.refresh(work, c, r)
				} else {
					r.Status = "Disconnected"
					r.Failure = "Reconnect the asset library to refresh this registration."
					_ = s.Repo.Save(work, r)
				}
				cancel()
			}
		}
	}
}
func Kind(contentType string) string {
	if strings.HasPrefix(contentType, "image/") {
		return "Image"
	}
	if strings.HasPrefix(contentType, "video/") {
		return "Video"
	}
	if strings.HasPrefix(contentType, "audio/") {
		return "Audio"
	}
	return ""
}

func (s *Service) saveDetached(ctx context.Context, r Record) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.Repo.Save(cleanup, r)
}
