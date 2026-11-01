package catalogsync

import (
	"encoding/json"
	"sort"
	"strings"
)

// Resolve informational facts before publication. Missing/failed observations
// retain the source's last-confirmed candidate rather than choosing a rival.
type operationalCandidate struct {
	refs     []CoverageSource
	value    json.RawMessage
	conflict bool
	current  bool
}

// Only explicitly unordered alternatives use set semantics. Raw source evidence
// is never changed; positions, frame roles and unknown arrays keep their order.
func canonicalOperationalValue(field string, value json.RawMessage) json.RawMessage {
	set := field == "output.resolutions" || field == "output.ratios" || field == "output.formats" || field == "voices" || field == "reference_modes"
	parts := strings.Split(field, ".")
	if len(parts) == 3 && parts[0] == "workflow" && parts[2] == "ratios" {
		set = true
	}
	if len(parts) == 4 && parts[0] == "workflow" && (parts[2] == "image" || parts[2] == "video" || parts[2] == "audio") && (parts[3] == "content_types" || parts[3] == "codecs") {
		set = true
	}
	canonicalStrings := func(raw json.RawMessage) json.RawMessage {
		var values []string
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return raw
		}
		sort.Strings(values)
		unique := []string{}
		for _, v := range values {
			if len(unique) == 0 || unique[len(unique)-1] != v {
				unique = append(unique, v)
			}
		}
		return encoded(unique)
	}
	if set {
		return canonicalStrings(value)
	}
	if field == "reference_prompt" {
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) == nil && object != nil {
			if types, exists := object["types"]; exists {
				object["types"] = canonicalStrings(types)
			}
			return encoded(object)
		}
	}
	return value
}

func sameOperationalValue(field string, a, b json.RawMessage) bool {
	return sameJSON(canonicalOperationalValue(field, a), canonicalOperationalValue(field, b))
}

func usableOperationalValue(value json.RawMessage) bool {
	return len(value) > 0 && json.Valid(value) && !sameJSON(value, json.RawMessage("null"))
}

// This history is generated from confirmed observations, never trusted from a
// fetched document. It is stale evidence until that source supplies the field again.
func operationalHistory(ob Observation) map[string]map[string]CoverageSource {
	history := map[string]map[string]CoverageSource{}
	for model, fields := range ob.OperationalHistory {
		for field, ref := range fields {
			if !validSnapshotHash(ref.Hash) || !usableOperationalValue(ref.Value) {
				continue
			}
			if history[model] == nil {
				history[model] = map[string]CoverageSource{}
			}
			history[model][field] = ref
		}
	}
	// Compatibility with states saved before per-field provenance was introduced.
	if validSnapshotHash(ob.Hash) {
		for _, entry := range ob.Entries {
			for field, value := range entry.OperationalFields {
				if !usableOperationalValue(value) {
					continue
				}
				if history[entry.ID] == nil {
					history[entry.ID] = map[string]CoverageSource{}
				}
				if _, exists := history[entry.ID][field]; !exists {
					history[entry.ID][field] = CoverageSource{Source: ob.Source, Hash: ob.Hash, Status: "retrieved", Value: value}
				}
			}
		}
	}
	return history
}

func rememberOperationalHistory(old, ob Observation) map[string]map[string]CoverageSource {
	history := operationalHistory(old)
	if ob.Error == "" && validSnapshotHash(ob.Hash) {
		for _, entry := range ob.Entries {
			for field, value := range entry.OperationalFields {
				if !usableOperationalValue(value) {
					continue
				}
				if history[entry.ID] == nil {
					history[entry.ID] = map[string]CoverageSource{}
				}
				history[entry.ID][field] = CoverageSource{Source: ob.Source, Hash: ob.Hash, Status: "retrieved", Value: value}
			}
		}
	}
	if len(history) == 0 {
		return nil
	}
	return history
}

func collectOperationalCandidates(previous State, observations []Observation) map[string]map[string]operationalCandidate {
	current := map[string]Observation{}
	sourceIDs := map[string]bool{}
	for id := range previous.Observations {
		sourceIDs[id] = true
	}
	for _, ob := range observations {
		current[ob.Source.ID] = ob
		sourceIDs[ob.Source.ID] = true
	}
	ids := []string{}
	for id := range sourceIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := map[string]map[string]operationalCandidate{}
	for _, id := range ids {
		ob, checked := current[id]
		old := previous.Observations[id]
		freshFields := map[string]map[string]json.RawMessage{}
		for _, entry := range ob.Entries {
			if freshFields[entry.ID] == nil {
				freshFields[entry.ID] = map[string]json.RawMessage{}
			}
			for key, value := range entry.OperationalFields {
				freshFields[entry.ID][key] = value
			}
		}
		history := operationalHistory(old)
		models := map[string]bool{}
		for model := range freshFields {
			models[model] = true
		}
		for model := range history {
			models[model] = true
		}
		for model := range models {
			keys := map[string]bool{}
			for key := range freshFields[model] {
				keys[key] = true
			}
			for key := range history[model] {
				keys[key] = true
			}
			for key := range keys {
				if result[model] == nil {
					result[model] = map[string]operationalCandidate{}
				}
				choice, exists := result[model][key]
				if !exists {
					choice.current = true
				}
				value := freshFields[model][key]
				fresh := checked && ob.Error == "" && validSnapshotHash(ob.Hash) && usableOperationalValue(value)
				ref := CoverageSource{Source: ob.Source, Hash: ob.Hash, Status: "retrieved", Value: value}
				if !fresh {
					choice.current = false
					confirmed := history[model][key]
					ref = CoverageSource{Source: confirmed.Source, LastConfirmedHash: confirmed.Hash, Status: "stale", Value: confirmed.Value}
					if ref.Source.ID == "" {
						ref.Source = ob.Source
					}
					if checked && ob.Error != "" {
						ref.Status = "preserved"
					}
				}
				choice.refs = append(choice.refs, ref)
				if usableOperationalValue(ref.Value) {
					if choice.value == nil {
						choice.value = ref.Value
					} else if !sameOperationalValue(key, choice.value, ref.Value) {
						choice.conflict = true
					}
				}
				result[model][key] = choice
			}
		}
	}
	return result
}
