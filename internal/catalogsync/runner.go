package catalogsync

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/google/uuid"
)

type Runner struct {
	Repo      Repository
	Providers Provider
	Client    *http.Client
	Mailer    Mailer
	// CheckOrigin comes only from the configured public UI origin, never a request.
	CheckOrigin string

	mu          sync.Mutex
	lastAttempt string
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
		if err := ctx.Err(); err != nil {
			return err
		}
		var failures []error
		if bootstrap, ok := r.Providers.(interface{ EnsureCatalogSchedules(context.Context) error }); ok {
			if err := bootstrap.EnsureCatalogSchedules(ctx); err != nil {
				failures = append(failures, fmt.Errorf("catalogue schedules: %w", err))
				if errors.Is(err, ErrRepositoryUnavailable) {
					return errors.Join(failures...)
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		settings, err := r.Repo.Settings(ctx)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		schedules, err := r.Repo.Schedules(ctx)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		// Rotate even failed attempts without altering durable schedule intent or
		// evidence. A persistent first-name failure must not starve later work.
		schedules = append([]Schedule(nil), schedules...)
		sort.Slice(schedules, func(i, j int) bool { return schedules[i].Provider < schedules[j].Provider })
		r.mu.Lock()
		start := sort.Search(len(schedules), func(i int) bool { return schedules[i].Provider > r.lastAttempt })
		r.mu.Unlock()
		attempts := 0
		for i := 0; i < len(schedules); i++ {
			schedule := schedules[(start+i)%len(schedules)]
			if err := ctx.Err(); err != nil {
				return errors.Join(append(failures, err)...)
			}
			if !schedule.Requested && (!schedule.Enabled || schedule.NextCheck == nil || schedule.NextCheck.After(now)) {
				continue
			}
			r.mu.Lock()
			r.lastAttempt = schedule.Provider
			r.mu.Unlock()
			attempts++
			if err := r.check(ctx, schedule, settings, now); err != nil {
				failures = append(failures, fmt.Errorf("catalogue provider %s: %w", schedule.Provider, err))
				if errors.Is(err, ErrRepositoryUnavailable) {
					return errors.Join(failures...)
				}
				if err := ctx.Err(); err != nil {
					return errors.Join(append(failures, err)...)
				}
				// Check the shared repository before attempting independent work.
				// A provider's invalid state/transaction is local; an unavailable
				// settings store stops the pass rather than amplifying an outage.
				if _, err := r.Repo.Settings(ctx); err != nil {
					return errors.Join(append(failures, err)...)
				}
				if attempts < 2 {
					continue
				}
			}
			// One successful check, or two failed attempts, bounds each tick.
			break
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if settings.Email != "" && r.Mailer != nil {
			pending, err := r.Repo.PendingEmails(ctx)
			if err != nil {
				return errors.Join(append(failures, err)...)
			}
			for _, run := range pending {
				if err := ctx.Err(); err != nil {
					return errors.Join(append(failures, err)...)
				}
				subject, text, markup := Report(run)
				status := "sent"
				if err := r.Mailer.SendCatalogReport(ctx, settings.Email, subject, text, markup, "catalog-"+run.ID); err != nil {
					status = "retry"
				}
				if err := r.Repo.EmailResult(ctx, run.ID, status); err != nil {
					return errors.Join(append(failures, err)...)
				}
			}
		}
		return errors.Join(failures...)
	})
	return err
}
func (r *Runner) check(parent context.Context, schedule Schedule, settings Settings, now time.Time) error {
	checkOrigin := normalizeCheckOrigin(r.CheckOrigin)
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	previous, err := r.Repo.State(ctx, schedule.Provider)
	if err != nil {
		return err
	}
	if err := parent.Err(); err != nil {
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
	sources := append([]Source(nil), schedule.Sources...)
	if resolver, ok := r.Providers.(interface {
		ResolveCatalogSource(context.Context, string, Source) Source
	}); ok {
		for i, source := range sources {
			sources[i] = resolver.ResolveCatalogSource(ctx, schedule.Provider, source)
		}
	}
	for _, source := range captureSources(sources, capture.LifecycleSources) {
		if err := parent.Err(); err != nil {
			return err
		}

		call, cancel := context.WithTimeout(ctx, 30*time.Second)
		ob := Fetch(call, r.Client, source)
		if ob.Error == "" && source.Parser == "document" {
			if interpreter, ok := r.Providers.(interface {
				InterpretCatalogSource(context.Context, string, Source, string) ([]Entry, error)
			}); ok {
				entries, err := interpreter.InterpretCatalogSource(call, schedule.Provider, source, ob.Text)
				if err != nil {
					ob.Error = interpretationError
					ob.FailureStage = "interpretation"
					ob.ModelReview = interpretationReview(err, source, ob.Hash)
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
	run.CheckOrigin = checkOrigin
	run.Provider = schedule.Provider
	run.StartedAt = now
	run.CompletedAt = time.Now().UTC()
	if err := parent.Err(); err != nil {
		return err
	}
	commitCtx, cancelCommit := context.WithTimeout(parent, 30*time.Second)
	defer cancelCommit()
	return r.Repo.Commit(commitCtx, schedule, next, run, NextDaily(run.CompletedAt, schedule.Time, settings.Timezone))
}

// Revalidate stored provenance before display, including imported historical runs.
func normalizeCheckOrigin(raw string) string {
	if len(raw) > 300 {
		return ""
	}
	origin, err := installation.NormalizeOrigin(raw)
	if err != nil {
		return ""
	}
	return origin
}

func Report(run Run) (string, string, string) {
	origin := normalizeCheckOrigin(run.CheckOrigin)
	if origin == "" {
		origin = "unknown (not recorded)"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Catalogue check: %s\nCheck instance: %s\nRun ID: %s\nChecked: %s\nStatus: %s\nPublished version: %d\n", run.Provider, origin, run.ID, run.CompletedAt.Format(time.RFC3339), run.Status, run.Version)
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
		fmt.Fprintf(&out, "%s [%s]: %s\nEvidence: %s\n", w.Model, w.Source, w.Reason, reportWarningEvidence(w, run.Sources))
	}
	out.WriteString("\nPREVIOUS WARNINGS — STILL OPEN\n")
	for _, w := range run.OpenWarnings {
		if !newKeys[w.Key] {
			fmt.Fprintf(&out, "%s [%s]: %s\nFirst seen: %s; last seen: %s; checks: %d\n", w.Model, w.Source, w.Reason, w.FirstSeen.Format(time.RFC3339), w.LastSeen.Format(time.RFC3339), w.Occurrences)
			if w.Reason == sourceFailureReason {
				fmt.Fprintf(&out, "Evidence: %s\n", reportWarningEvidence(w, run.Sources))
			}
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
