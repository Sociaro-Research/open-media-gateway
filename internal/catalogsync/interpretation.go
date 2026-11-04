package catalogsync

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

const interpretationError = "Operational source interpretation failed; last confirmed information was preserved"
const sourceFailureReason = "Source check failed; last confirmed information was preserved"

// ModelReviewEvidence describes a public document label, not a runtime model ID
// or a supported protocol. The snapshot identifies the rejected document.
type ModelReviewEvidence struct {
	SourceID   string `json:"source_id"`
	URL        string `json:"url"`
	Snapshot   string `json:"snapshot"`
	ModelLabel string `json:"model_label"`
	Line       int    `json:"line"`
}

type modelReviewError struct {
	source Source
	label  string
	line   int
}

func (*modelReviewError) Error() string { return interpretationError }

var publicModelLabel = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 .+()_-]{0,79}$`)
var publicSourceID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,99}$`)
var credentialWord = regexp.MustCompile(`(?i)(bearer|authorization|password|secret|api[_ -]?key|token|sk-)`)
var opaqueLabelToken = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)
var sourceHTTPError = regexp.MustCompile(`^Source returned HTTP [1-5][0-9]{2}$`)

func validPublicLabel(label string) bool {
	return publicModelLabel.MatchString(label) && strings.TrimSpace(label) == label &&
		!credentialWord.MatchString(label) && !opaqueLabelToken.MatchString(label)
}

func publicDocumentSource(source Source) bool {
	u, err := url.Parse(source.URL)
	return err == nil && source.Parser == "document" && publicSourceID.MatchString(source.ID) &&
		u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" &&
		u.Fragment == "" && !u.ForceQuery && len(source.URL) <= 2048 &&
		!strings.ContainsAny(source.URL, "\r\n\t<>\"'`")
}

// NewModelReviewError carries only a bounded model-column label and document
// line. Never pass arbitrary parser errors, conditions or upstream response text.
// Invalid evidence deliberately falls back to the generic safe error.
func NewModelReviewError(source Source, label string, line int) error {
	if !publicDocumentSource(source) || !validPublicLabel(label) || line < 1 || line > 100000 {
		return errors.New(interpretationError)
	}
	return &modelReviewError{source: source, label: label, line: line}
}

func interpretationReview(err error, source Source, hash string) *ModelReviewEvidence {
	var typed *modelReviewError
	if !errors.As(err, &typed) || typed == nil || typed.source != source {
		return nil
	}
	e := &ModelReviewEvidence{SourceID: source.ID, URL: source.URL, Snapshot: hash, ModelLabel: typed.label, Line: typed.line}
	if !validModelReview(e, source, hash) {
		return nil
	}
	return e
}

func validModelReview(e *ModelReviewEvidence, source Source, hash string) bool {
	return e != nil && publicDocumentSource(source) && e.SourceID == source.ID && e.URL == source.URL &&
		e.Snapshot == hash && validSnapshotHash(hash) && validPublicLabel(e.ModelLabel) && e.Line > 0 && e.Line <= 100000
}

func failureStatus(ob Observation) string {
	// Old persisted runs have no stage. Only the exact historical safe message
	// proves interpretation failure; unknown legacy errors remain retrieval errors.
	if ob.FailureStage == "interpretation" || (ob.FailureStage == "" && ob.Error == interpretationError) {
		return "interpretation_failed"
	}
	return "retrieval_failed"
}

type sourceFailureEvidence struct {
	Error  string               `json:"error"`
	Hash   string               `json:"hash"`
	Stage  string               `json:"failure_stage,omitempty"`
	Review *ModelReviewEvidence `json:"model_review,omitempty"`
	Action string               `json:"action,omitempty"`
}

func safeSourceError(message string) string {
	switch message {
	case interpretationError, "Invalid source request", "Source request failed", "Source exceeded the size limit or could not be read", "Extracted document exceeded the size limit", "Provider model discovery failed; check the configured connection":
		return message
	}
	if sourceHTTPError.MatchString(message) {
		return message
	}
	return sourceFailureReason
}

func sourceFailure(ob Observation) sourceFailureEvidence {
	e := sourceFailureEvidence{Error: safeSourceError(ob.Error), Stage: failureStatus(ob)}
	if validSnapshotHash(ob.FetchedHash) {
		e.Hash = ob.FetchedHash
	}
	if e.Stage == "interpretation_failed" && validModelReview(ob.ModelReview, ob.Source, e.Hash) {
		e.Review = ob.ModelReview
	}
	if e.Review != nil {
		e.Action = "Adapter review/integration required for this published model or condition; support and entitlement are unverified"
	}
	return e
}

// Revalidate persisted evidence at the reporting boundary, including legacy runs.
// Only source-failure warnings use this allowlist; other warning formats retain
// their existing structured evidence. Never print an arbitrary upstream error.
func reportWarningEvidence(w Warning, sources []Observation) json.RawMessage {
	if w.Reason != sourceFailureReason {
		return w.Evidence
	}
	var saved sourceFailureEvidence
	if json.Unmarshal(w.Evidence, &saved) != nil {
		return encoded(sourceFailureEvidence{Error: sourceFailureReason})
	}
	clean := sourceFailureEvidence{Error: safeSourceError(saved.Error)}
	if validSnapshotHash(saved.Hash) {
		clean.Hash = saved.Hash
	}
	if saved.Stage == "interpretation_failed" || saved.Stage == "retrieval_failed" {
		clean.Stage = saved.Stage
	}
	if clean.Stage == "" && saved.Error == interpretationError {
		clean.Stage = "interpretation_failed"
	}
	if clean.Stage == "interpretation_failed" {
		for _, ob := range sources {
			if ob.Source.ID == w.Source && ob.Error != "" && failureStatus(ob) == "interpretation_failed" && ob.FetchedHash == clean.Hash &&
				validModelReview(saved.Review, ob.Source, clean.Hash) && validModelReview(ob.ModelReview, ob.Source, clean.Hash) && *saved.Review == *ob.ModelReview {
				clean.Review = saved.Review
				clean.Action = "Adapter review/integration required for this published model or condition; support and entitlement are unverified"
				break
			}
		}
	}
	return encoded(clean)
}
