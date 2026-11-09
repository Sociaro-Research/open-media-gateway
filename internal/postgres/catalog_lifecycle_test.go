package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestCatalogLifecycleSurvivesRepositoryReopenAndRetirementPublication(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewCatalogRepository(db)
	source := catalogsync.Source{ID: "notice", URL: "https://official.example.test/lifecycle", Parser: "document"}
	schedule := catalogsync.Schedule{Provider: "lifecycle-fixture", Time: "03:00", Version: 1, Sources: []catalogsync.Source{source}}
	if err := repo.EnsureSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	model := core.MediaModel{ID: "known-media", MediaType: "image"}
	capture := catalogsync.Capture{Models: []core.MediaModel{model}, IDs: []string{model.ID}, LifecycleSources: []catalogsync.Source{source}}
	// The runner supplies the document hash; repository publication retains its exact evidence.
	obs := []catalogsync.Observation{{Source: source, Text: "Official lifecycle fixture.", Hash: fmt.Sprintf("%x", sha256.Sum256([]byte("Official lifecycle fixture."))), Entries: []catalogsync.Entry{{ID: model.ID, Lifecycle: &catalogsync.Lifecycle{ShutdownDate: "2026-11-11", ShutdownAt: "2026-11-11T09:00:00Z"}}}}}
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	publish := func(previous catalogsync.State, at time.Time) catalogsync.State {
		state, run := catalogsync.Reconcile(previous, capture, obs, at, nil)
		run.ID, run.Provider, run.StartedAt, run.CompletedAt = uuid.NewString(), schedule.Provider, at, at
		if err := repo.Commit(ctx, schedule, state, run, at.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		repo = NewCatalogRepository(db)
		stored, err := repo.State(ctx, schedule.Provider)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Models[model.ID].Lifecycle == nil || stored.Models[model.ID].Lifecycle.Source != source.URL || stored.Models[model.ID].Metadata.ShutdownAt != "2026-11-11T09:00:00Z" {
			t.Fatal("lifecycle lost on reopen", stored)
		}
		return stored
	}
	state := publish(catalogsync.State{}, cutoff.Add(-time.Second))
	if state.Models[model.ID].Inactive || !state.Models[model.ID].Lifecycle.Unavailable(cutoff) {
		t.Fatal("stored future boundary not executable independently of sync")
	}
	state = publish(state, cutoff)
	if !state.Models[model.ID].Inactive || state.Models[model.ID].Contract != nil {
		t.Fatal("retirement was not published")
	}
	versions, err := repo.Versions(ctx, schedule.Provider, 50, 0)
	if err != nil || len(versions) != 2 || versions[1].Models[model.ID].Inactive || versions[1].Models[model.ID].Contract == nil {
		t.Fatal("historical version changed", versions, err)
	}
	_, repeat := catalogsync.Reconcile(state, capture, obs, cutoff.Add(time.Hour), nil)
	if len(repeat.Changes) != 0 {
		t.Fatal("repository replay oscillated", repeat.Changes)
	}
}
