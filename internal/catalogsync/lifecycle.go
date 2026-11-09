package catalogsync

import (
	"fmt"
	"reflect"
	"sort"
	"time"
)

func captureSources(saved, required []Source) []Source {
	result := append([]Source(nil), saved...)
	for i, source := range required {
		found := false
		for _, old := range result {
			found = found || old.URL == source.URL && old.Parser == source.Parser
		}
		if !found {
			source.ID = fmt.Sprintf("_lifecycle-%d", i)
			result = append(result, source)
		}
	}
	return result
}

// Lifecycle is an explicit admission window parsed by a reviewed provider
// module. Release/version/API-creation dates never imply an admission boundary.
// Date-only boundaries use UTC, preserving the catalogue's existing convention.
type Lifecycle struct {
	AvailableDate string `json:"available_date,omitempty"`
	AvailableAt   string `json:"available_at,omitempty"`
	ShutdownDate  string `json:"shutdown_date,omitempty"`
	ShutdownAt    string `json:"shutdown_at,omitempty"`
	Source        string `json:"source,omitempty"`
	Snapshot      string `json:"snapshot,omitempty"`
}

func lifecycleTime(date, instant string) (time.Time, bool) {
	if instant != "" {
		t, err := time.Parse(time.RFC3339, instant)
		return t, err == nil && (date == "" || date == t.UTC().Format("2006-01-02"))
	}
	t, err := time.Parse("2006-01-02", date)
	return t, err == nil
}

func (l Lifecycle) valid() bool {
	start, end := time.Time{}, time.Time{}
	ok := false
	if l.AvailableDate != "" || l.AvailableAt != "" {
		start, ok = lifecycleTime(l.AvailableDate, l.AvailableAt)
		if !ok {
			return false
		}
	}
	if l.ShutdownDate != "" || l.ShutdownAt != "" {
		end, ok = lifecycleTime(l.ShutdownDate, l.ShutdownAt)
		if !ok {
			return false
		}
	}
	return (!start.IsZero() || !end.IsZero()) && (start.IsZero() || end.IsZero() || start.Before(end))
}

func (l Lifecycle) Unavailable(now time.Time) bool {
	if !l.valid() {
		return false
	}
	if start, ok := lifecycleTime(l.AvailableDate, l.AvailableAt); ok && now.Before(start) {
		return true
	}
	end, ok := lifecycleTime(l.ShutdownDate, l.ShutdownAt)
	return ok && !now.Before(end)
}

func sameLifecycle(a, b Lifecycle) bool {
	a.Source, a.Snapshot, b.Source, b.Snapshot = "", "", "", ""
	return reflect.DeepEqual(a, b)
}

func lifecycleCandidates(previous State, native Capture, observations []Observation, now time.Time, warn func(string, string, string, any)) map[string]*Lifecycle {
	result := map[string]*Lifecycle{}
	for id, entry := range previous.Models {
		if entry.Lifecycle != nil {
			copy := *entry.Lifecycle
			result[id] = &copy
		}
	}

	candidates := map[string][]Lifecycle{}
	current := map[string]bool{}
	for _, ob := range observations {
		trusted := false
		for _, source := range native.LifecycleSources {
			trusted = trusted || source.URL == ob.Source.URL && source.Parser == ob.Source.Parser
		}
		evidence := ob
		if ob.Error != "" {
			evidence = previous.Observations[ob.Source.ID]
			if evidence.Source.URL != ob.Source.URL || evidence.Source.Parser != ob.Source.Parser {
				continue
			}
		}
		for _, entry := range evidence.Entries {
			if entry.Lifecycle == nil {
				continue
			}
			if !trusted || !entry.Lifecycle.valid() || evidence.Hash == "" {
				warn(entry.ID, ob.Source.ID, "Published lifecycle requires a reviewed official source and valid boundaries", entry.Lifecycle)
				continue
			}
			candidate := *entry.Lifecycle
			candidate.Source, candidate.Snapshot = ob.Source.URL, evidence.Hash
			candidates[entry.ID] = append(candidates[entry.ID], candidate)
			if ob.Error == "" {
				current[candidate.Source+"/"+candidate.Snapshot] = true
			}
		}
	}
	for id, records := range candidates {
		// A stable source ordering makes conflict keys/evidence independent of fetch
		// order. Failed sources retain their last-confirmed vote, never a fresh one.
		sort.Slice(records, func(i, j int) bool { return string(encoded(records[i])) < string(encoded(records[j])) })
		chosen := records[0]
		conflict := false
		for _, candidate := range records[1:] {
			conflict = conflict || !sameLifecycle(chosen, candidate)
			if !current[chosen.Source+"/"+chosen.Snapshot] && current[candidate.Source+"/"+candidate.Snapshot] {
				chosen = candidate
			}
		}
		if conflict {
			warn(id, "_lifecycle", "Official sources disagree on lifecycle", records)
			continue
		}
		if old := result[id]; old != nil {
			if old.Unavailable(now) && !chosen.Unavailable(now) {
				warn(id, "_lifecycle", "Reactivation of an unavailable model requires review", chosen)
				continue
			}
			if sameLifecycle(*old, chosen) && old.Source == chosen.Source {
				continue
			}
		}
		// A failed fetch cannot introduce a previously unpublished control.
		if current[chosen.Source+"/"+chosen.Snapshot] {
			copy := chosen
			result[id] = &copy
		}
	}
	for id, lifecycle := range result {
		for _, ob := range observations {
			if ob.Source.URL != lifecycle.Source || ob.Error != "" {
				continue
			}
			found := false
			for _, entry := range ob.Entries {
				found = found || entry.ID == id && entry.Lifecycle != nil
			}
			if !found {
				warn(id, "_lifecycle", "Previously confirmed lifecycle is absent from the official source; last boundary requires review", lifecycle)
			}
		}
	}
	return result
}
