package catalogsync

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

type Runner struct {
	Repo      Repository
	Providers Provider
	Client    *http.Client
	Mailer    Mailer
}

func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := r.Tick(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
			slog.Warn("catalogue synchronization failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *Runner) Tick(ctx context.Context, now time.Time) error {
	_, err := r.Repo.WithLock(ctx, func(ctx context.Context) error {
		if bootstrap, ok := r.Providers.(interface{ EnsureCatalogSchedules(context.Context) error }); ok {
			if err := bootstrap.EnsureCatalogSchedules(ctx); err != nil {
				return err
			}
		}
		settings, err := r.Repo.Settings(ctx)
		if err != nil {
			return err
		}
		schedules, err := r.Repo.Schedules(ctx)
		if err != nil {
			return err
		}
		for _, schedule := range schedules {
			if !schedule.Requested && (!schedule.Enabled || schedule.NextCheck == nil || schedule.NextCheck.After(now)) {
				continue
			}
			if err := r.check(ctx, schedule, settings, now); err != nil {
				return err
			}
			// One provider per tick bounds concurrency and spreads catch-up work.
			break
		}
		if settings.Email != "" && r.Mailer != nil {
			pending, err := r.Repo.PendingEmails(ctx)
			if err != nil {
				return err
			}
			for _, run := range pending {
				subject, text, markup := Report(run)
				status := "sent"
				if err := r.Mailer.SendCatalogReport(ctx, settings.Email, subject, text, markup, "catalog-"+run.ID); err != nil {
					status = "retry"
				}
				if err := r.Repo.EmailResult(ctx, run.ID, status); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}
func (r *Runner) check(parent context.Context, schedule Schedule, settings Settings, now time.Time) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	previous, err := r.Repo.State(ctx, schedule.Provider)
	if err != nil {
		return err
	}
	capture, err := r.Providers.CaptureCatalog(ctx, schedule.Provider)
	capture.IDs = append([]string{}, capture.IDs...)
	sort.Strings(capture.IDs)
	apiObservation := Observation{Source: Source{ID: "_api", Parser: "api"}, Entries: []Entry{}}
	apiObservation.Text = string(encoded(capture.IDs))
	apiObservation.Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(apiObservation.Text)))
	observations := []Observation{}
	if err != nil {
		// Do not pass provider error messages through reports: upstream errors
		// can contain credentials or private endpoint information.
		apiObservation.Error = "Provider model discovery failed; check the configured connection"
		for id, entry := range previous.Models {
			model := core.MediaModel{ID: id, Metadata: entry.Metadata}
			if entry.Contract != nil {
				model = *entry.Contract
				model.Metadata = entry.Metadata
			}
			if entry.ExcludedReason != "" {
				capture.Excluded = append(capture.Excluded, Exclusion{ID: id, Reason: entry.ExcludedReason, Metadata: entry.Metadata})
			} else if entry.Inactive {
				capture.Inactive = append(capture.Inactive, model)
			} else {
				capture.Models = append(capture.Models, model)
			}
		}
	}
	observations = append(observations, apiObservation)
	for _, source := range schedule.Sources {
		if resolver, ok := r.Providers.(interface {
			ResolveCatalogSource(context.Context, string, Source) Source
		}); ok {
			source = resolver.ResolveCatalogSource(ctx, schedule.Provider, source)
		}
		call, cancel := context.WithTimeout(ctx, 30*time.Second)
		ob := Fetch(call, r.Client, source)
		if ob.Error == "" && source.Parser == "document" {
			if interpreter, ok := r.Providers.(interface {
				InterpretCatalogSource(context.Context, string, Source, string) ([]Entry, error)
			}); ok {
				entries, err := interpreter.InterpretCatalogSource(call, schedule.Provider, source, ob.Text)
				if err != nil {
					ob.Error = "Operational source interpretation failed; last confirmed information was preserved"
					ob.FetchedHash = ob.Hash
					ob.Hash = ""
				} else if entries != nil {
					ob.Entries = entries
				}
			}
		}
		observations = append(observations, ob)
		cancel()
	}
	next, run := Reconcile(previous, capture, observations, now, func(model core.MediaModel, rate Rate) error {
		return r.Providers.CheckCatalogRate(ctx, schedule.Provider, model, rate)
	})
	// Daily incomplete or partial checks require a report even when no publication changed.
	if run.Completeness != nil && run.Completeness.Status != "complete" {
		run.EmailStatus = "pending"
	}
	if settings.Email == "" && run.EmailStatus == "pending" {
		run.EmailStatus = "disabled"
	}
	run.ID = uuid.NewString()
	run.Provider = schedule.Provider
	run.StartedAt = now
	run.CompletedAt = time.Now().UTC()
	commitCtx, cancelCommit := context.WithTimeout(parent, 30*time.Second)
	defer cancelCommit()
	return r.Repo.Commit(commitCtx, schedule, next, run, NextDaily(run.CompletedAt, schedule.Time, settings.Timezone))
}

func Report(run Run) (string, string, string) {
	var out strings.Builder
	fmt.Fprintf(&out, "Catalogue check: %s\nChecked: %s\nStatus: %s\nPublished version: %d\n", run.Provider, run.CompletedAt.Format(time.RFC3339), run.Status, run.Version)
	writeCompleteness(&out, run.Completeness)
	for _, source := range run.Sources {
		if source.Review != nil {
			fmt.Fprintf(&out, "\nREVIEWED DOCUMENT CHANGE\nSource: %s\nReviewed: %s\nReason: %s\nSnapshot: %s\n", source.Source.URL, source.Review.Date, source.Review.Reason, source.Review.Hash)
		}
	}
	out.WriteString("\nAPPLIED CHANGES\n")
	if len(run.Changes) == 0 {
		out.WriteString("None.\n")
	}
	for _, c := range run.Changes {
		fmt.Fprintf(&out, "%s: %s\n  Before: %s\n  After: %s\n  Source: %s\n", c.Model, c.Field, c.Before, c.After, c.Source)
	}
	out.WriteString("\nNEW WARNINGS — ACTION REQUIRED\n")
	newKeys := map[string]bool{}
	if len(run.NewWarnings) == 0 {
		out.WriteString("None.\n")
	}
	for _, w := range run.NewWarnings {
		newKeys[w.Key] = true
		fmt.Fprintf(&out, "%s [%s]: %s\nEvidence: %s\n", w.Model, w.Source, w.Reason, w.Evidence)
	}
	out.WriteString("\nPREVIOUS WARNINGS — STILL OPEN\n")
	for _, w := range run.OpenWarnings {
		if !newKeys[w.Key] {
			fmt.Fprintf(&out, "%s [%s]: %s\nFirst seen: %s; last seen: %s; checks: %d\n", w.Model, w.Source, w.Reason, w.FirstSeen.Format(time.RFC3339), w.LastSeen.Format(time.RFC3339), w.Occurrences)
		}
	}
	out.WriteString("\nRESOLVED WARNINGS\n")
	for _, w := range run.ResolvedWarnings {
		fmt.Fprintf(&out, "%s [%s]: %s\n", w.Model, w.Source, w.Reason)
	}
	text := out.String()
	markup := "<!doctype html><html><body><h1>Model catalogue report</h1><pre style=\"white-space:pre-wrap\">" + html.EscapeString(text) + "</pre></body></html>"
	markup = strings.ReplaceAll(markup, "NEW WARNINGS — ACTION REQUIRED", "<strong style=\"color:#9a3412\">NEW WARNINGS — ACTION REQUIRED</strong>")
	markup = strings.ReplaceAll(markup, "PREVIOUS WARNINGS — STILL OPEN", "<strong style=\"color:#9a3412\">PREVIOUS WARNINGS — STILL OPEN</strong>")
	return fmt.Sprintf("Catalogue: %s — %s", run.Provider, run.Status), text, markup
}
