package catalogsync

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func encoded(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return reflect.DeepEqual(a, b)
	}
	return reflect.DeepEqual(x, y)
}
func contract(m core.MediaModel) core.MediaModel {
	m.Metadata = nil
	m.Pricing = core.MediaModelPricing{}
	m.DisplayName = ""
	m.PublicID = ""
	return m
}
func rateKey(r Rate) string {
	key := fmt.Sprintf("%s/%s/%s/%s/%d", r.Region, r.Resolution, r.Quality, r.Unit, r.Per)
	if r.Audio != nil {
		key += fmt.Sprintf("/audio=%t", *r.Audio)
	}
	// Omitted selectors in existing versions mean no video input.
	if r.VideoInput != nil && *r.VideoInput {
		key += "/video_input=true"
	}
	return key
}
func ValidDate(d string) bool {
	if d == "" {
		return true
	}
	layout := "2006-01-02"
	if len(d) == 7 {
		layout = "2006-01"
	}
	_, e := time.Parse(layout, d)
	return e == nil
}

// Reconcile re-evaluates every pending candidate, including unchanged evidence.
// A code upgrade can therefore resolve warnings without an upstream edit.
func Reconcile(previous State, native Capture, observations []Observation, now time.Time, checkRate func(core.MediaModel, Rate) error) (State, Run) {
	// Dated snapshots share a tariff only through an explicit adapter review.
	// Keep the original source/hash; validate every copied tariff against the
	// snapshot's own estimator. An explicitly published snapshot tariff wins.
	// Detach nested tariff components too: callers may reuse or refresh their
	// observations after publication without changing a returned version.
	var detached []Observation
	_ = json.Unmarshal(encoded(observations), &detached)
	observations = detached
	for i, ob := range observations {
		if ob.Error != "" {
			continue
		}
		ids := map[string]bool{}
		for _, e := range ob.Entries {
			ids[e.ID] = true
		}
		entries := append([]Entry(nil), ob.Entries...)
		aliases := make([]string, 0, len(native.TariffAliases))
		for id := range native.TariffAliases {
			aliases = append(aliases, id)
		}
		sort.Strings(aliases)
		for _, id := range aliases {
			if ids[id] {
				continue
			}
			for _, e := range ob.Entries {
				if e.ID == native.TariffAliases[id] && len(e.Rates) > 0 {
					entries = append(entries, Entry{ID: id, Rates: append([]Rate(nil), e.Rates...)})
				}
			}
		}
		observations[i].Entries = entries
	}
	var next State
	_ = json.Unmarshal(encoded(previous), &next)
	if next.Models == nil {
		next.Models = map[string]Entry{}
	}
	if next.Observations == nil {
		next.Observations = map[string]Observation{}
	}
	run := Run{Changes: []Change{}, NewWarnings: []Warning{}, OpenWarnings: []Warning{}, ResolvedWarnings: []Warning{}, Sources: observations, EmailStatus: "not_needed"}
	warnings := map[string]Warning{}
	oldWarnings := map[string]Warning{}
	for _, w := range previous.Warnings {
		if w.ResolvedAt == nil {
			oldWarnings[w.Key] = w
		}
	}
	warn := func(model, source, reason string, evidence any) {
		key := model + "|" + source + "|" + reason
		// Retain independent unsupported facts and regional/resolution tariffs.
		if fact, ok := evidence.(core.ModelFact); ok {
			key += "|" + fact.Label
		}
		if details, ok := evidence.(map[string]any); ok {
			if rate, ok := details["rate"].(Rate); ok {
				key += "|" + rateKey(rate)
			}
		}
		w := Warning{Key: key, Model: model, Source: source, Reason: reason, Evidence: encoded(evidence), FirstSeen: now, LastSeen: now, Occurrences: 1}
		if old, ok := oldWarnings[key]; ok {
			w.FirstSeen = old.FirstSeen
			w.Occurrences = old.Occurrences + 1
		}
		warnings[key] = w
	}
	change := func(model, field, source string, before, after any) {
		if !reflect.DeepEqual(before, after) {
			run.Changes = append(run.Changes, Change{Model: model, Field: field, Source: source, Before: encoded(before), After: encoded(after)})
		}
	}
	known := map[string]core.MediaModel{}
	checkedSources := map[string]bool{}
	apiFailed := false
	// Resolve field conflicts before mutating the catalogue, preventing an
	// alternating pair of sources from producing daily false changes.
	type candidate struct {
		value, source string
		conflict      bool
	}
	values := map[string]candidate{}
	type tariffCandidate struct {
		rate     Rate
		source   string
		conflict bool
	}
	tariffs := map[string]tariffCandidate{}
	seenTariffs := map[string]map[string]bool{}
	for _, ob := range observations {
		checkedSources[ob.Source.ID] = true
		if ob.Source.ID == "_api" && ob.Error != "" {
			apiFailed = true
		}
		entries := ob.Entries
		if ob.Error != "" {
			entries = previous.Observations[ob.Source.ID].Entries
		}
		for _, entry := range entries {
			for _, rate := range entry.Rates {
				key := entry.ID + "/" + rateKey(rate)
				rate.Source = ""
				if seenTariffs[ob.Source.ID] == nil {
					seenTariffs[ob.Source.ID] = map[string]bool{}
				}
				seenTariffs[ob.Source.ID][key] = true
				old, ok := tariffs[key]
				if !ok {
					tariffs[key] = tariffCandidate{rate: rate, source: ob.Source.ID}
				} else if !reflect.DeepEqual(old.rate, rate) {
					old.conflict = true
					tariffs[key] = old
				}
			}
			if entry.Metadata == nil {
				continue
			}
			m := entry.Metadata
			for field, value := range map[string]string{"description": m.Description, "release_date": m.ReleaseDate, "version_date": m.VersionDate, "shutdown_date": m.ShutdownDate, "snapshot": m.Snapshot} {
				if value == "" {
					continue
				}
				key := entry.ID + "/" + field
				old, ok := values[key]
				if !ok {
					values[key] = candidate{value: value, source: ob.Source.ID}
				} else if old.value != value {
					if field == "release_date" {
						if value < old.value {
							values[key] = candidate{value: value, source: ob.Source.ID}
						}
					} else {
						old.conflict = true
						values[key] = old
					}
				}
			}
		}
	}
	inactive := map[string]bool{}
	apiIDs := map[string]bool{}
	for _, id := range native.IDs {
		apiIDs[id] = true
	}
	models := append([]core.MediaModel{}, native.Models...)
	present := map[string]bool{}
	for _, m := range models {
		present[m.ID] = true
	}
	reviewed := map[string]bool{}
	reviewedMetadata := map[string]*core.ModelMetadata{}
	for _, m := range native.Reviewed {
		reviewed[m.ID] = true
		reviewedMetadata[m.ID] = m.Metadata
		if !present[m.ID] {
			models = append(models, m)
			present[m.ID] = true
		}
	}
	for _, m := range native.Inactive {
		inactive[m.ID] = true
	}
	for _, m := range append(models, native.Inactive...) {
		if m.Manual {
			continue
		}
		known[m.ID] = m
		if _, ok := next.Models[m.ID]; !ok {
			descriptor := contract(m)
			entry := Entry{ID: m.ID, Metadata: m.Metadata, Contract: &descriptor, Inactive: inactive[m.ID]}
			if entry.Inactive {
				entry.Contract = nil
			}
			// Provider adapters may cache their descriptors. Never mutate them.
			var detached Entry
			_ = json.Unmarshal(encoded(entry), &detached)
			entry = detached
			next.Models[m.ID] = entry
			change(m.ID, "model", "adapter", nil, entry)
		} else {
			entry := next.Models[m.ID]
			descriptor := contract(m)
			var nextContract *core.MediaModel
			if !inactive[m.ID] {
				nextContract = &descriptor
			}
			change(m.ID, "contract", "adapter", entry.Contract, nextContract)
			change(m.ID, "inactive", "adapter", entry.Inactive, inactive[m.ID])
			entry.Contract = nextContract
			entry.Inactive = inactive[m.ID]
			change(m.ID, "excluded_reason", "adapter", entry.ExcludedReason, "")
			entry.ExcludedReason = ""
			if m.Metadata != nil && entry.Metadata != nil {
				facts := append([]core.ModelFact(nil), m.Metadata.Facts...)
				// Offline adapter descriptors have no API facts. Losing those
				// transiently must not publish an alternating remove/add cycle.
				// Explicit (even empty) live facts still replace previous values.
				labels := map[string]bool{}
				for _, fact := range facts {
					labels[fact.Label] = true
				}
				for _, fact := range entry.Metadata.Facts {
					if strings.HasPrefix(fact.Label, "API ") && !labels[fact.Label] {
						facts = append(facts, fact)
					}
				}
				change(m.ID, "facts", "adapter", entry.Metadata.Facts, facts)
				_ = json.Unmarshal(encoded(facts), &entry.Metadata.Facts)
			}
			next.Models[m.ID] = entry
		}
		// A code-reviewed lifecycle date must also reach an existing catalogue.
		// Fill only missing dates; current structured evidence and previously
		// confirmed dates take precedence over the compiled baseline.
		entry := next.Models[m.ID]
		if metadata := reviewedMetadata[m.ID]; metadata != nil && len(metadata.ShutdownDate) == 10 && ValidDate(metadata.ShutdownDate) && values[m.ID+"/shutdown_date"].value == "" && (entry.Metadata == nil || entry.Metadata.ShutdownDate == "") {
			if entry.Metadata == nil {
				entry.Metadata = &core.ModelMetadata{}
			}
			change(m.ID, "shutdown_date", "adapter", "", metadata.ShutdownDate)
			entry.Metadata.ShutdownDate = metadata.ShutdownDate
			for _, source := range metadata.Sources {
				found := false
				for _, old := range entry.Metadata.Sources {
					found = found || old.URL == source.URL
				}
				if !found {
					entry.Metadata.Sources = append(entry.Metadata.Sources, source)
				}
			}
			next.Models[m.ID] = entry
		}
		if reviewed[m.ID] && !inactive[m.ID] && !apiFailed {
			entry := next.Models[m.ID]
			status := "not_discovered"
			if apiIDs[m.ID] {
				status = "discovered"
			}
			if (entry.DiscoveryStatus == "discovered" || entry.DiscoveryStatus == "missing") && status == "not_discovered" {
				status = "missing"
				warn(m.ID, "_api", "Previously integrated model is absent from provider discovery; availability requires review", map[string]string{"id": m.ID})
			}
			change(m.ID, "discovery_status", "_api", entry.DiscoveryStatus, status)
			entry.DiscoveryStatus = status
			next.Models[m.ID] = entry
		}
	}
	for _, excluded := range native.Excluded {
		if excluded.ID == "" || excluded.Reason == "" {
			continue
		}
		if _, active := known[excluded.ID]; active {
			continue
		}
		known[excluded.ID] = core.MediaModel{ID: excluded.ID}
		entry := next.Models[excluded.ID]
		change(excluded.ID, "excluded_reason", "adapter", entry.ExcludedReason, excluded.Reason)
		var disabled *core.MediaModel
		change(excluded.ID, "contract", "adapter", entry.Contract, disabled)
		change(excluded.ID, "metadata", "adapter", entry.Metadata, excluded.Metadata)
		entry.ID, entry.ExcludedReason, entry.Contract = excluded.ID, excluded.Reason, nil
		entry.Metadata = excluded.Metadata
		next.Models[excluded.ID] = entry
	}
	discovered := map[string]bool{}
	for _, id := range native.IDs {
		discovered[id] = true
		if _, ok := known[id]; !ok {
			warn(id, "_api", "Model requires an adapter contract before it can be enabled", map[string]string{"id": id})
		}
	}
	if !apiFailed {
		for id := range previous.Models {
			if _, ok := known[id]; !ok && !discovered[id] {
				warn(id, "_api", "Previously integrated model is absent from provider discovery; availability requires review", map[string]string{"id": id})
			}
		}
	}
	// Preserve candidates from temporarily failed sources. A failure must not
	// silently resolve an outstanding model/protocol warning.
	for i, ob := range observations {
		old := next.Observations[ob.Source.ID]
		// Never accept review metadata supplied by a source or an older run.
		ob.Review = nil
		review, reviewed := native.DocumentReviews[ob.Source.URL]
		reviewed = reviewed && ob.Source.Parser == "document" && ob.Error == "" && ob.Hash != "" && review.Hash == ob.Hash && review.Reason != "" && review.Date != "" && ValidDate(review.Date)
		if reviewed {
			ob.Review = &review
		}
		run.Sources[i].Review = ob.Review
		if ob.Error != "" {
			warn("", ob.Source.ID, "Source check failed; last confirmed information was preserved", map[string]string{"error": ob.Error, "hash": ob.FetchedHash})
			if old.Source.ID != "" {
				ob.Entries = old.Entries
				if ob.Text == "" {
					ob.Text = old.Text
				}
				ob.Hash = old.Hash
			}
		}
		if !reviewed && ob.Error == "" && old.Hash != "" && old.Hash != ob.Hash && (len(ob.Entries) == 0 || ob.Source.Parser == "openai-model") && ob.Source.ID != "_api" {
			warn("", ob.Source.ID, "Documentation changed without a supported structured interpretation", map[string]string{"url": ob.Source.URL, "previous_hash": old.Hash, "hash": ob.Hash})
		} else if !reviewed && (ob.Error != "" || len(ob.Entries) == 0 || ob.Source.Parser == "openai-model") {
			// A failed fetch cannot review an earlier document change. Retain
			// its original evidence through failures and quiet recovery days.
			for _, w := range oldWarnings {
				if w.Source == ob.Source.ID && strings.HasPrefix(w.Reason, "Documentation changed") {
					warn(w.Model, w.Source, w.Reason, json.RawMessage(w.Evidence))
				}
			}
		}
		next.Observations[ob.Source.ID] = ob
		for _, entry := range ob.Entries {
			model, ok := known[entry.ID]
			if !ok {
				warn(entry.ID, ob.Source.ID, "Model requires an adapter contract before it can be enabled", entry)
				continue
			}
			current := next.Models[entry.ID]
			if current.ExcludedReason != "" {
				continue
			}
			if entry.Contract != nil && !reflect.DeepEqual(contract(*entry.Contract), contract(model)) {
				warn(entry.ID, ob.Source.ID, "Capabilities or reference requirements differ from the adapter contract", entry.Contract)
			}
			if entry.Metadata != nil {
				m := entry.Metadata
				if !ValidDate(m.ReleaseDate) || !ValidDate(m.VersionDate) || !ValidDate(m.ShutdownDate) {
					warn(entry.ID, ob.Source.ID, "Invalid model date", m)
					continue
				}
				if current.Metadata == nil {
					current.Metadata = &core.ModelMetadata{}
				}
				for _, field := range []struct {
					name  string
					dst   *string
					value string
				}{
					{"description", &current.Metadata.Description, m.Description}, {"release_date", &current.Metadata.ReleaseDate, m.ReleaseDate}, {"version_date", &current.Metadata.VersionDate, m.VersionDate}, {"shutdown_date", &current.Metadata.ShutdownDate, m.ShutdownDate}, {"snapshot", &current.Metadata.Snapshot, m.Snapshot},
				} {
					if field.value != "" {
						chosen := values[entry.ID+"/"+field.name]
						if chosen.conflict {
							warn(entry.ID, ob.Source.ID, "Official sources disagree on "+field.name, field.value)
							continue
						}
						if chosen.value != field.value {
							continue
						}
						change(entry.ID, field.name, ob.Source.URL, *field.dst, field.value)
						*field.dst = field.value
					}
				}
				current.Metadata.CheckedAt = now.UTC().Format("2006-01-02")
				if len(m.Facts) > 0 {
					// Published capabilities are evidence, never executable controls.
					for _, fact := range m.Facts {
						if strings.HasPrefix(fact.Label, "Provider:") {
							matched := false
							var nativeFacts []core.ModelFact
							if model.Metadata != nil {
								nativeFacts = model.Metadata.Facts
							}
							for _, before := range nativeFacts {
								if before.Label == fact.Label {
									matched = true
								}
								if before.Label == fact.Label && before.Value != fact.Value {
									warn(entry.ID, ob.Source.ID, "Published capability details require adapter review", fact)
								}
							}
							if !matched {
								warn(entry.ID, ob.Source.ID, "Published capability details require adapter review", fact)
							}
						} else if strings.HasPrefix(fact.Label, "Published tariff") && (len(entry.Rates) == 0 || fact.Label == "Published tariff condition") {
							warn(entry.ID, ob.Source.ID, "Published tariff needs a structured pricing rule", fact)
						}
					}
				}
				source := core.ModelSource{Title: ob.Source.ID, URL: ob.Source.URL}
				found := false
				for _, s := range current.Metadata.Sources {
					if s.URL == source.URL {
						found = true
					}
				}
				if !found {
					current.Metadata.Sources = append(current.Metadata.Sources, source)
				}
			}
			for _, rate := range entry.Rates {
				if current.Inactive {
					warn(entry.ID, ob.Source.ID, "Tariff belongs to a deactivated model; automatic activation is prohibited", map[string]any{"rate": rate})
					continue
				}
				chosen := tariffs[entry.ID+"/"+rateKey(rate)]
				if chosen.conflict {
					warn(entry.ID, ob.Source.ID, "Official sources disagree on tariff", map[string]any{"rate": rate})
					continue
				}
				if chosen.source != ob.Source.ID {
					continue
				}
				rate.Source = ob.Source.URL
				if err := checkRate(model, rate); err != nil {
					warn(entry.ID, ob.Source.ID, "Tariff requires supported units, region and estimation rules", map[string]any{"rate": rate, "reason": err.Error()})
					continue
				}
				index := -1
				for i, r := range current.Rates {
					if rateKey(r) == rateKey(rate) {
						index = i
						break
					}
				}
				var before any
				if index >= 0 {
					before = current.Rates[index]
				}
				change(entry.ID, "tariff", ob.Source.URL, before, rate)
				if index < 0 {
					current.Rates = append(current.Rates, rate)
				} else {
					current.Rates[index] = rate
				}
			}
			next.Models[entry.ID] = current
		}
	}
	for id, entry := range previous.Models {
		// Reviewed retirement preserves old prices as historical evidence, not
		// as executable tariffs requiring rediscovery after provider removal.
		if next.Models[id].Inactive || next.Models[id].ExcludedReason != "" {
			continue
		}
		for _, rate := range entry.Rates {
			for _, prior := range previous.Observations {
				if prior.Source.URL != rate.Source || rate.Source == "" {
					continue
				}
				sourceID := prior.Source.ID
				current, checked := next.Observations[sourceID]
				if checkedSources[sourceID] && checked && current.Error != "" {
					continue
				}
				if !checkedSources[sourceID] || !seenTariffs[sourceID][id+"/"+rateKey(rate)] {
					warn(id, sourceID, "Previously confirmed tariff is absent from the configured source; last price requires review", map[string]any{"rate": rate})
				}
			}
		}
	}
	// A removed source cannot resolve its earlier warnings without review.
	for key, w := range oldWarnings {
		if !checkedSources[w.Source] && w.Source != "_api" || apiFailed && w.Source == "_api" {
			warnings[key] = w
		}
	}
	next.Warnings = []Warning{}
	keys := make([]string, 0, len(warnings))
	for key := range warnings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		w := warnings[key]
		next.Warnings = append(next.Warnings, w)
		run.OpenWarnings = append(run.OpenWarnings, w)
		old, ok := oldWarnings[key]
		if !ok || !sameJSON(old.Evidence, w.Evidence) {
			run.NewWarnings = append(run.NewWarnings, w)
		}
	}
	for key, w := range oldWarnings {
		if _, ok := warnings[key]; !ok {
			w.ResolvedAt = &now
			run.ResolvedWarnings = append(run.ResolvedWarnings, w)
		}
	}
	if len(run.Changes) > 0 {
		next.Version++
		run.Status = "applied"
	} else {
		run.Status = "unchanged"
	}
	if len(run.OpenWarnings) > 0 {
		run.Status = "warning"
	}
	for _, ob := range observations {
		if ob.Error != "" {
			run.Status = "error"
		}
	}
	run.Version = next.Version
	if len(run.Changes) > 0 || len(run.NewWarnings) > 0 || len(run.ResolvedWarnings) > 0 {
		run.EmailStatus = "pending"
	}
	return next, run
}
