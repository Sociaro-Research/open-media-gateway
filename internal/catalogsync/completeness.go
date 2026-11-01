package catalogsync

import (
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Completeness is independent of publication changes and warning fingerprints.
// It assesses the operational-v1 requirements, not every fact a vendor publishes.
type Completeness struct {
	Requirements string           `json:"requirements"`
	Status       string           `json:"status"`
	Models       []ModelCoverage  `json:"models"`
	Sources      []CoverageSource `json:"sources"`
	Reasons      []string         `json:"reasons"`
}
type ModelCoverage struct {
	Model           string                     `json:"model"`
	Status          string                     `json:"status"`
	Contract        map[string]json.RawMessage `json:"contract"`
	Fields          []FieldCoverage            `json:"fields"`
	DeclaredSources []core.ModelSource         `json:"declared_sources,omitempty"`
}
type FieldCoverage struct {
	Field    string           `json:"field"`
	Required bool             `json:"required"`
	Status   string           `json:"status"`
	Reason   string           `json:"reason"`
	Value    json.RawMessage  `json:"value,omitempty"`
	Sources  []CoverageSource `json:"sources,omitempty"`
}
type CoverageSource struct {
	Value             json.RawMessage `json:"value,omitempty"`
	Source            Source          `json:"source"`
	Status            string          `json:"status"`
	Hash              string          `json:"hash,omitempty"`
	LastConfirmedHash string          `json:"previous_hash,omitempty"`
}

// AssessCompleteness never promotes a URL, a generic fact, a document-change
// acknowledgment or an unchanged hash into proof of an executable field. Only
// an exact structured contract interpretation can verify present requirements.
func AssessCompleteness(previous State, native Capture, observations []Observation, warnings []Warning) *Completeness {
	result := &Completeness{Requirements: "operational-v1", Status: "complete", Models: []ModelCoverage{}, Sources: []CoverageSource{}, Reasons: []string{}}
	checked := map[string]bool{}
	for _, ob := range observations {
		checked[ob.Source.ID] = true
		s := CoverageSource{Source: ob.Source, Hash: ob.Hash, Status: "retrieved"}
		if ob.Error != "" {
			s.Status = "retrieval_failed"
			s.Hash = ob.FetchedHash
			old := previous.Observations[ob.Source.ID]
			if old.Source == ob.Source {
				s.LastConfirmedHash = old.Hash
			}
			result.Reasons = append(result.Reasons, ob.Source.ID+": retrieval failed; current verification unavailable")
		}
		result.Sources = append(result.Sources, s)
	}
	for id, ob := range previous.Observations {
		if !checked[id] {
			result.Sources = append(result.Sources, CoverageSource{Source: ob.Source, Status: "stale", LastConfirmedHash: ob.Hash})
			result.Reasons = append(result.Reasons, id+": source not checked in this run")
		}
	}
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].Source.ID < result.Sources[j].Source.ID })
	inactive := map[string]bool{}
	for _, m := range native.Inactive {
		inactive[m.ID] = true
	}
	for _, e := range native.Excluded {
		inactive[e.ID] = true
	}
	operationalCandidates := collectOperationalCandidates(previous, observations)
	seen := map[string]bool{}
	for _, m := range native.Models {
		if inactive[m.ID] || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		expectedSourceUnavailable := false
		for _, expected := range native.VerificationSources[m.ID] {
			observed := false
			for _, ob := range observations {
				if ob.Source.URL == expected.URL {
					observed = true
					if ob.Error != "" || !validSnapshotHash(ob.Hash) {
						expectedSourceUnavailable = true
					}
				}
			}
			if !observed {
				expectedSourceUnavailable = true
				present := false
				for _, s := range result.Sources {
					present = present || s.Source.URL == expected.URL
				}
				if !present {
					result.Sources = append(result.Sources, CoverageSource{Source: expected, Status: "not_checked"})
				}
				result.Reasons = append(result.Reasons, m.ID+": authoritative operational source not checked: "+expected.URL)
			}
		}
		cm := ModelCoverage{Model: m.ID, Status: "complete", Contract: operationalContract(m), Fields: []FieldCoverage{}}
		if expectedSourceUnavailable {
			cm.Status = "incomplete"
		}
		if m.Metadata != nil {
			cm.DeclaredSources = append([]core.ModelSource{}, m.Metadata.Sources...)
		}
		proof, status, reason := contractEvidence(m, previous, observations, false)
		detailProof, detailStatus, detailReason := contractEvidence(m, previous, observations, true)
		assess := func(field string, value any, present, required bool) {
			f := FieldCoverage{Field: field, Required: required, Status: status, Reason: reason, Value: encoded(value), Sources: proof}
			if !required {
				f.Status = detailStatus
				f.Reason = detailReason
				f.Sources = detailProof
			}
			if refs, evidenceStatus, evidenceReason, found := operationalFieldEvidence(field, encoded(value), operationalCandidates[m.ID][field]); found {
				f.Sources = refs
				f.Status = evidenceStatus
				f.Reason = evidenceReason
			}
			if !present {
				f.Status = "unknown"
				f.Reason = "Required native operational field is not declared; this is not a provider capability exclusion or proof of parser loss"
				if !required {
					f.Reason = "Optional reviewed provider detail is not declared; existing native validation remains applicable"
				}
			}
			if f.Status != "verified" {
				if required {
					cm.Status = "incomplete"
				} else if cm.Status == "complete" {
					cm.Status = "partial"
				}
			}
			cm.Fields = append(cm.Fields, f)
		}
		add := func(field string, value any, present bool) { assess(field, value, present, true) }
		detail := func(field string, value any, present bool) { assess(field, value, present, false) }
		omit := func(field, status, reason string) {
			cm.Fields = append(cm.Fields, FieldCoverage{Field: field, Status: status, Reason: reason})
		}
		add("media_type", m.MediaType, m.MediaType == "image" || m.MediaType == "video" || m.MediaType == "speech" || m.MediaType == "music" || m.MediaType == "audio")
		add("prompt_policy", map[string]bool{"no_prompt": m.NoPrompt, "optional": m.PromptOptional}, true)
		switch m.MediaType {
		case "video", "image":
			if len(m.Resolutions) > 0 {
				add("output.resolutions", m.Resolutions, true)
			} else {
				omit("output.resolutions", "not_applicable", "Adapter declares no resolution selector")
			}
			if len(m.Ratios) > 0 {
				add("output.ratios", m.Ratios, true)
			} else {
				omit("output.ratios", "not_applicable", "Adapter declares no aspect-ratio selector")
			}
		default:
			omit("output.resolutions", "not_applicable", "Output is not an image or video")
			omit("output.ratios", "not_applicable", "Output is not an image or video")
		}
		if m.MediaType == "video" && !m.DurationFromInput {
			add("output.duration", map[string]int{"min": m.MinDuration, "max": m.MaxDuration, "default": m.DefaultDuration, "step": m.DurationStep}, m.MinDuration > 0 && m.MaxDuration >= m.MinDuration && m.DefaultDuration >= m.MinDuration && m.DefaultDuration <= m.MaxDuration)
		} else {
			omit("output.duration", "not_applicable", "No selectable video duration; output follows its native modality or input duration")
		}
		if m.MediaType == "speech" || m.MediaType == "music" || m.MediaType == "audio" {
			add("output.formats", m.Formats, len(m.Formats) > 0)
		} else {
			omit("output.formats", "not_applicable", "No audio-output format selector")
		}
		if m.SupportsVoice || m.RequiresVoice {
			add("voices", m.Voices, len(m.Voices) > 0)
		} else {
			omit("voices", "not_applicable", "Adapter declares no voice selector")
		}
		refs := m.SupportsImageInput || m.SupportsVideoInput || m.SupportsAudioInput || m.RequiresImageInput || m.RequiresVideoInput || m.RequiresAudioInput
		for _, mode := range m.ReferenceModes {
			refs = refs || mode.MaxImages > 0 || mode.MaxVideos > 0 || mode.MaxAudios > 0
		}
		if !refs {
			omit("references", "not_applicable", "No reference-input workflow is declared")
		} else {
			modes := core.ReferenceModesFor(m)
			modeIDs := []string{}
			for _, mode := range modes {
				modeIDs = append(modeIDs, mode.ID)
			}
			add("reference_modes", modeIDs, validReferenceModes(modes))
			if m.ReferencePromptSyntax != "" {
				add("reference_prompt", map[string]any{"syntax": m.ReferencePromptSyntax, "types": m.ReferencePromptTypes}, true)
			} else {
				omit("reference_prompt", "not_applicable", "No marker grammar is declared; ordered inputs do not require markers")
			}
			limits := m.ReferenceMediaLimits
			maxFiles := 0
			if limits != nil {
				maxFiles = limits.MaxFiles
			}
			if limits == nil {
				detail("reference_media_limits", nil, false)
			}
			if limits != nil {
				detail("reference_media_limits.max_files", maxFiles, maxFiles > 0)
			}

			for _, mode := range modes {
				if len(mode.Ratios) > 0 {
					add("workflow."+mode.ID+".ratios", mode.Ratios, true)
				}
				if mode.MaxVisuals > 0 {
					add("workflow."+mode.ID+".visual_count", map[string]int{"min": mode.MinVisuals, "max": mode.MaxVisuals}, true)
				}
				if mode.MaxDurationWithVideo > 0 {
					add("workflow."+mode.ID+".max_duration_with_video", mode.MaxDurationWithVideo, true)
				}
				sumField := "workflow." + mode.ID + videoDurationSumSuffix
				if mode.MaxVideos > 0 && hasOperationalField(m.ID, sumField, previous, observations) {
					envelope, ok := videoDurationEnvelopeFor(m, sumField)
					add(sumField, envelope, ok && envelope.InputMaxSeconds > 0 && envelope.OutputMaxSeconds > 0)
				}
				for _, kind := range []string{"image", "video", "audio"} {
					maxCount := mode.MaxImages
					rule := core.MediaFileLimits{}
					if limits != nil {
						rule = limits.Image
					}
					if kind == "video" {
						maxCount = mode.MaxVideos
						if limits != nil {
							rule = limits.Video
						}
					}
					if kind == "audio" {
						maxCount = mode.MaxAudios
						if limits != nil {
							rule = limits.Audio
						}
					}
					if kind != "audio" && mode.MaxVisuals > 0 && maxCount > mode.MaxVisuals {
						maxCount = mode.MaxVisuals
					}
					prefix := "workflow." + mode.ID + "." + kind
					if maxCount == 0 {
						omit(prefix, "unsupported", "This gateway workflow declares zero inputs of this modality; no upstream capability inference")
						continue
					}
					minCount := mode.MinImages
					if kind == "video" {
						minCount = mode.MinVideos
					}
					if kind == "audio" {
						minCount = mode.MinAudios
					}
					add(prefix+".count", map[string]int{"min": minCount, "max": maxCount}, validReferenceModes([]core.ReferenceMode{mode}))
					if limits == nil {
						continue
					}
					detail(prefix+".content_types", rule.ContentTypes, len(rule.ContentTypes) > 0)
					detail(prefix+".max_bytes", rule.MaxBytes, rule.MaxBytes > 0)
					if rule.NoAlpha {
						detail(prefix+".no_alpha", rule.NoAlpha, true)
					}
					if len(rule.Codecs) > 0 {
						detail(prefix+".codecs", rule.Codecs, true)
					}
					if rule.MinFPS > 0 || rule.MaxFPS > 0 {
						detail(prefix+".fps", map[string]any{"min": rule.MinFPS, "max": rule.MaxFPS}, true)
					}
					if kind == "image" || kind == "video" {
						detail(prefix+".dimensions", map[string]any{"min_dimension": rule.MinDimension, "max_dimension": rule.MaxDimension, "min_pixels": rule.MinPixels, "max_pixels": rule.MaxPixels, "min_ratio": rule.MinRatio, "max_ratio": rule.MaxRatio}, rule.MinDimension > 0 || rule.MaxDimension > 0 || rule.MinPixels > 0 || rule.MaxPixels > 0)
					}
					if kind != "image" {
						detail(prefix+".duration", map[string]any{"min": rule.MinDuration, "max": rule.MaxDuration, "min_total": rule.MinTotalDuration, "max_total": rule.MaxTotalDuration}, rule.MaxDuration > 0 || rule.MaxTotalDuration > 0)
					}
				}
			}
		}
		result.Models = append(result.Models, cm)
		if cm.Status == "incomplete" {
			result.Status = "incomplete"
		} else if cm.Status == "partial" && result.Status == "complete" {
			result.Status = "partial"
		}
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].Model < result.Models[j].Model })
	if len(result.Models) == 0 {
		result.Reasons = append(result.Reasons, "No connected supported models were captured; coverage cannot be established")
	}
	for _, warning := range warnings {
		if warning.Model == "" || seen[warning.Model] && !inactive[warning.Model] {
			result.Reasons = append(result.Reasons, "Unresolved source or supported-model warnings require review; see unchanged warning evidence")
			break
		}
	}
	if len(result.Reasons) > 0 {
		result.Status = "incomplete"
	}
	sort.Strings(result.Reasons)
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].Source.ID < result.Sources[j].Source.ID })
	var detached Completeness
	_ = json.Unmarshal(encoded(result), &detached)
	return &detached
}

// Keep protocol evidence separate from tariffs and presentation metadata. In
// particular, a deliberately omitted price must never be serialized as zero.
func operationalContract(m core.MediaModel) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded(contract(m)), &fields)
	delete(fields, "pricing")
	delete(fields, "display_name")
	return fields
}

func validSnapshotHash(hash string) bool {
	bytes, err := hex.DecodeString(hash)
	return err == nil && len(bytes) == 32
}

func validReferenceModes(modes []core.ReferenceMode) bool {
	if len(modes) == 0 {
		return false
	}
	for _, mode := range modes {
		if mode.ID == "" || mode.MinImages < 0 || mode.MaxImages < mode.MinImages || mode.MinVideos < 0 || mode.MaxVideos < mode.MinVideos || mode.MinAudios < 0 || mode.MaxAudios < mode.MinAudios {
			return false
		}
	}
	return true
}
func contractEvidence(m core.MediaModel, previous State, observations []Observation, includeDetails bool) ([]CoverageSource, string, string) {
	comparable := func(value core.MediaModel) core.MediaModel {
		value = contract(value)
		if !includeDetails {
			value.ReferenceMediaLimits = nil
		}
		return value
	}
	matches := func(ob Observation) bool {
		// Only versioned structured interpretation, never discovery or prose, is proof.
		if ob.Source.Parser != "manifest-v1" || !strings.HasPrefix(ob.Source.URL, "https://") || !validSnapshotHash(ob.Hash) {
			return false
		}
		for _, e := range ob.Entries {
			if e.ID == m.ID && e.Contract != nil && sameJSON(encoded(comparable(*e.Contract)), encoded(comparable(m))) {
				return true
			}
		}
		return false
	}
	var preserved []CoverageSource
	preservedStatus := "stale"
	for _, ob := range observations {
		if ob.Error == "" && matches(ob) {
			reason := "Exact structured native contract matches this run's source snapshot; optional file details are assessed separately"
			if includeDetails {
				reason = "Exact structured optional-detail interpretation matches this run's source snapshot"
			}
			return []CoverageSource{{Source: ob.Source, Status: "retrieved", Hash: ob.Hash}}, "verified", reason
		}
		old := previous.Observations[ob.Source.ID]
		if matches(old) {
			status := "stale"
			if ob.Error != "" {
				status = "preserved"
				preservedStatus = "preserved"
			}
			preserved = append(preserved, CoverageSource{Source: old.Source, Status: status, LastConfirmedHash: old.Hash})
		}
	}
	for _, old := range previous.Observations {
		checked := false
		for _, ob := range observations {
			checked = checked || ob.Source.ID == old.Source.ID
		}
		if !checked && matches(old) {
			preserved = append(preserved, CoverageSource{Source: old.Source, Status: "stale", LastConfirmedHash: old.Hash})
		}
	}
	if len(preserved) > 0 {
		sort.Slice(preserved, func(i, j int) bool { return preserved[i].Source.ID < preserved[j].Source.ID })
		return preserved, preservedStatus, "Last confirmed contract retained; current source interpretation is unavailable or no longer matches"
	}
	return nil, "unknown", "Adapter value is present, but no exact structured source interpretation verifies this operational field"
}

func writeCompleteness(out *strings.Builder, c *Completeness) {
	if c == nil {
		out.WriteString("\nOPERATIONAL COMPLETENESS: not assessed (historical run)\n")
		return
	}
	out.WriteString("\nOPERATIONAL COMPLETENESS: " + c.Status + " (" + c.Requirements + ")\n")
	if c.Status == "partial" {
		out.WriteString("Required contract fields verified; optional provider details remain unverified. This does not exclude model capabilities.\n")
	}
	for _, reason := range c.Reasons {
		out.WriteString("- " + reason + "\n")
	}
	for _, source := range c.Sources {
		out.WriteString("Source: " + source.Source.ID + " " + source.Source.URL + " — " + source.Status + "; snapshot " + source.Hash + "; last confirmed " + source.LastConfirmedHash + "\n")
	}
	for _, model := range c.Models {
		out.WriteString("\nModel: " + model.Model + " — " + model.Status + "\n")
		out.WriteString("Current adapter contract: " + string(encoded(model.Contract)) + "\n")
		for _, source := range model.DeclaredSources {
			out.WriteString("Adapter source reference (not field verification): " + source.URL + "\n")
		}
		for _, field := range model.Fields {
			requirement := "optional detail"
			if field.Required {
				requirement = "required contract field"
			}
			out.WriteString(field.Field + ": " + field.Status + " (" + requirement + ") — " + field.Reason + "\n")
			for _, source := range field.Sources {
				out.WriteString("  Evidence: " + source.Source.URL + "; snapshot " + source.Hash + "; last confirmed " + source.LastConfirmedHash + "\n")
			}
		}
	}
}
