package kling

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
)

func TestFlash4MissingOperationalSourcesAreExplicit(t *testing.T) {
	definition := Definition()
	expected := definition.CatalogVerificationSources
	want := map[string][]string{
		TextToVideo4Flash:  {"kling4-flash-text-to-video|text-to-video"},
		ImageToVideo4Flash: {"kling4-flash-image-to-video|image-to-video"},
		OmniVideo4Flash:    {"kling4-flash-omni|video-omni", "kling4-flash-elements|elements"},
	}
	if len(expected) != 3 || len(expected[TextToVideo4Flash]) != 1 || len(expected[ImageToVideo4Flash]) != 1 || len(expected[OmniVideo4Flash]) != 2 {
		t.Fatal("missing exact operation source bindings", expected)
	}
	registered := map[catalogsync.Source]bool{}
	for _, source := range definition.CatalogSources {
		registered[source] = true
	}
	for _, sources := range expected {
		for _, source := range sources {
			if !registered[source] {
				t.Fatal("unregistered expected source", source)
			}
		}
	}
	for id, bindings := range want {
		for i, binding := range bindings {
			parts := strings.Split(binding, "|")
			if expected[id][i] != (catalogsync.Source{ID: parts[0], URL: docsBase + "api/video/4-0-flash/" + parts[1] + ".md", Parser: "document"}) {
				t.Fatal("wrong exact Flash source", id, expected[id])
			}
		}
	}
	capture := catalogsync.Capture{Models: flash4Models(), VerificationSources: expected}
	coverage := catalogsync.AssessCompleteness(catalogsync.State{}, capture, nil, nil)
	if len(coverage.Sources) != 4 {
		t.Fatal("missing operation-specific sources", coverage.Sources)
	}
	observations := []catalogsync.Observation{}
	for _, source := range coverage.Sources {
		if source.Status != "not_checked" {
			t.Fatal("omitted persisted source presented as retrieved", source)
		}
		observations = append(observations, catalogsync.Observation{Source: source.Source, Hash: strings.Repeat("a", 64)})
	}
	for _, model := range capture.Models {
		found := false
		for _, reason := range coverage.Reasons {
			found = found || strings.Contains(reason, model.ID+": authoritative operational source not checked:")
		}
		if !found {
			t.Fatal("missing source reason for exact model", model.ID)
		}
	}
	// Registration/retrieval is not structured verification or an acknowledgment.
	retrieved := catalogsync.AssessCompleteness(catalogsync.State{}, capture, observations, nil)
	if retrieved.Status == "complete" {
		t.Fatal("source URLs/hashes alone verified a contract")
	}
	failed := append([]catalogsync.Observation(nil), observations...)
	unrelated := append([]catalogsync.Observation(nil), observations...)
	for i := range failed {
		failed[i].Error = "Source returned HTTP 503"
		failed[i].Hash = ""
		unrelated[i].Source.URL = strings.ReplaceAll(unrelated[i].Source.URL, "4-0-flash/", "4-0/")
	}
	failedCoverage := catalogsync.AssessCompleteness(catalogsync.State{}, capture, failed, nil)
	if failedCoverage.Status == "complete" {
		t.Fatal("failed source verified Flash contract")
	}
	for _, source := range failedCoverage.Sources {
		if source.Status != "retrieval_failed" {
			t.Fatal("failed source confused with absent source", source)
		}
	}
	unrelatedCoverage := catalogsync.AssessCompleteness(catalogsync.State{}, capture, unrelated, nil)
	missing := 0
	for _, source := range unrelatedCoverage.Sources {
		if source.Status == "not_checked" {
			missing++
		}
	}
	if unrelatedCoverage.Status == "complete" || missing != 4 {
		t.Fatal("standard 4.0 URLs substituted for Flash authority", unrelatedCoverage)
	}
	prices := []catalogsync.Entry{}
	for _, entry := range pricingEntries(t, "video") {
		prices = append(prices, entry)
	}
	priceOnly := catalogsync.AssessCompleteness(catalogsync.State{}, capture, []catalogsync.Observation{{Source: catalogsync.Source{ID: "pricing", URL: priceMarkdownSource, Parser: "document"}, Hash: strings.Repeat("a", 64), Entries: prices}}, nil)
	if priceOnly.Status == "complete" {
		t.Fatal("reviewed tariffs substituted for operational source verification")
	}
}

func TestFlash4DoesNotWidenElementCreation(t *testing.T) {
	m, ok := resourceGenerationModel(ResourceCreateElement)
	if !ok {
		t.Fatal("element creation missing")
	}
	for _, variant := range m.ResourceOperation.Variants {
		if variant.Subtype != "video_character" {
			continue
		}
		limit := variant.Limits.Video
		// Preserve the pre-existing creation byte limit and 1080p contract,
		// independently of the larger generation-reference acceptance envelope.
		if limit.MinDuration != 3 || limit.MaxDuration != 8 || limit.MaxBytes != 200<<20 || !reflect.DeepEqual(variant.AllowedDimensions, []core.MediaDimensions{{Width: 1920, Height: 1080}, {Width: 1080, Height: 1920}}) {
			t.Fatal("Flash generation widened element creation", variant)
		}
		return
	}
	t.Fatal("video element creation contract missing")
}

func TestFlash4InspectedReferenceFileBoundaries(t *testing.T) {
	image := referencevalidation.File{Type: "image", Name: "synthetic.png", Info: mediafile.Info{ContentType: "image/png", Size: 30000000, Width: 300, Height: 300}}
	for _, id := range []string{ImageToVideo4Flash, OmniVideo4Flash} {
		m, _ := modelFor(id)
		if err := referencevalidation.ValidateFiles(m, []referencevalidation.File{image}, true); err != nil {
			t.Fatal("inclusive image boundary rejected", id, err)
		}
		for _, tc := range []struct {
			name   string
			change func(*referencevalidation.File)
		}{
			{"image bytes", func(f *referencevalidation.File) { f.Info.Size++ }},
			{"image dimension", func(f *referencevalidation.File) { f.Info.Width = 299 }},
			{"image format", func(f *referencevalidation.File) { f.Info.ContentType = "image/webp" }},
			{"image ratio", func(f *referencevalidation.File) { f.Info.Width = 751 }},
		} {
			t.Run(id+"/"+tc.name, func(t *testing.T) {
				f := image
				tc.change(&f)
				if referencevalidation.ValidateFiles(m, []referencevalidation.File{f}, true) == nil {
					t.Fatal("invalid inspected image accepted")
				}
			})
		}
	}
	m, _ := modelFor(OmniVideo4Flash)
	video := referencevalidation.File{Type: "video", Name: "synthetic.mp4", Info: mediafile.Info{ContentType: "video/mp4", Size: 300000000, Width: 700, Height: 700, Duration: 20, FPS: 24}}
	if err := referencevalidation.ValidateFiles(m, []referencevalidation.File{video}, true); err != nil {
		t.Fatal("inclusive video boundary rejected", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*referencevalidation.File)
	}{
		{"video bytes", func(f *referencevalidation.File) { f.Info.Size++ }},
		{"video format", func(f *referencevalidation.File) { f.Info.ContentType = "video/webm" }},
		{"low dimension", func(f *referencevalidation.File) { f.Info.Width = 699 }},
		{"high dimension", func(f *referencevalidation.File) { f.Info.Width = 4554 }},
		{"area", func(f *referencevalidation.File) { f.Info.Width = 4000; f.Info.Height = 3000 }},
		{"ratio", func(f *referencevalidation.File) { f.Info.Width = 1751 }},
		{"low fps", func(f *referencevalidation.File) { f.Info.FPS = 23.99 }},
		{"high fps", func(f *referencevalidation.File) { f.Info.FPS = 60.01 }},
		{"short duration", func(f *referencevalidation.File) { f.Info.Duration = 2.99 }},
		{"long duration", func(f *referencevalidation.File) { f.Info.Duration = 20.01 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := video
			tc.change(&f)
			if referencevalidation.ValidateFiles(m, []referencevalidation.File{f}, true) == nil {
				t.Fatal("invalid inspected video accepted")
			}
		})
	}
	video.Info.Duration = 10
	if err := referencevalidation.ValidateFiles(m, []referencevalidation.File{video, video}, true); err != nil {
		t.Fatal("20 second aggregate rejected", err)
	}
	second := video
	second.Info.Duration = 10.01
	if referencevalidation.ValidateFiles(m, []referencevalidation.File{video, second}, true) == nil {
		t.Fatal("20.01 second aggregate accepted")
	}
}
