package references

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

func TestGW253ResolveDirectRequiresCanonicalFallback(t *testing.T) {
	// Pure storage fixture: this test never creates an HTTP client or sends a request.
	opener := &openerStub{owner: resolverOwner, data: []byte("stored image")}
	input := core.MediaOptions{VideoURLs: []string{resolverURL("https://substituted.invalid") + "&source=other-bytes"}}
	_, err := NewResolver(opener, "secret").ResolveDirect(identity.WithOwnerID(context.Background(), resolverOwner), input)
	if err == nil {
		t.Fatal("caller URL was retained without a configured canonical source")
	}
	if opener.calls != 0 {
		t.Fatal("direct URL fallback read media bytes")
	}
}

func TestGW253SignedCapabilityParsing(t *testing.T) {
	canonical := resolverURL("https://gateway.example")
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"canonical", canonical, true},
		{"relative", resolverURL(""), true},
		{"altered host", resolverURL("https://substituted.invalid"), true},
		{"extra query", canonical + "&source=different", true},
		{"fragment", canonical + "#different", true},
		{"duplicate signature", canonical + "&signature=other", false},
		{"malformed query", canonical + "&source=%xx", false},
		{"changed id", strings.Replace(canonical, resolverID, "469eda6a-8f12-4b5b-b5f7-b505e3cea208", 1), false},
		{"changed owner", strings.Replace(canonical, resolverOwner, "47440eea-86ed-49fe-a70a-353f305988dd", 1), false},
		{"extra path", strings.Replace(canonical, "?signature", "/other?signature", 1), false},
		{"encoded path", strings.Replace(canonical, "/references/", "/%72eferences/", 1), false},
		{"changed signature", canonical + "other", false},
		{"external", "https://vendor.example/video.mp4?signature=other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, id, ok := ParseSignedReference(tc.raw, []byte("secret"))
			if ok != tc.valid {
				t.Fatalf("recognized=%v want %v", ok, tc.valid)
			}
			if !ok {
				return
			}
			got, err := CanonicalReferenceURL("https://gateway.example", []byte("secret"), owner, id)
			if err != nil || got != canonical {
				t.Fatalf("canonical=%q err=%v", got, err)
			}
		})
	}
}

func TestGW253CanonicalOriginMustBeConfigured(t *testing.T) {
	for _, origin := range []string{"", "//gateway.example", "ftp://gateway.example", "https://user:pass@gateway.example", "https://gateway.example/other", "https://gateway.example?source=other", "https://gateway.example#other"} {
		if got, err := CanonicalReferenceURL(origin, []byte("secret"), resolverOwner, resolverID); err == nil {
			t.Fatalf("unsafe or missing origin %q produced %q", origin, got)
		}
	}
	if got, err := CanonicalReferenceURL("http://gateway.example/", []byte("secret"), resolverOwner, resolverID); err != nil || got != resolverURL("http://gateway.example") {
		t.Fatalf("configured HTTP origin: %q %v", got, err)
	}
}

type sourceBindingDirectOpener struct {
	openerStub
	target      string
	directCalls int
	directErr   error
}

func (o *sourceBindingDirectOpener) DirectReferenceURL(ctx context.Context, id string) (string, error) {
	o.directCalls++
	if identity.OwnerID(ctx) != resolverOwner || id != resolverID {
		return "", errors.New("incorrect authorized source")
	}
	return o.target, o.directErr
}

func TestGW253ResolutionBindsEveryMediaFieldAndPreservesAcceptedInput(t *testing.T) {
	canonical := resolverURL("https://gateway.example")
	altered := resolverURL("https://substituted.invalid") + "&source=different#other"
	for _, tc := range []struct {
		name, target string
		direct       bool
	}{
		{"no direct storage interface", canonical, false},
		{"local storage empty direct URL", canonical, true},
		{"direct storage", "https://bucket.example/owner/object?storage-signature=synthetic", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &openerStub{owner: resolverOwner}
			var opener ImageOpener = local
			direct := &sourceBindingDirectOpener{openerStub: openerStub{owner: resolverOwner}}
			if tc.direct {
				opener = direct
			}
			if tc.target != canonical {
				direct.target = tc.target
			}
			evidence := map[string]core.InputMediaEvidence{canonical: {Type: "video", Duration: 4}}
			input := core.MediaOptions{ImageURLs: []string{altered}, VideoURLs: []string{altered}, AudioURLs: []string{altered}, VideoURL: altered, AudioURL: altered, ReferenceVoices: map[string]string{altered: altered}, ReferenceMediaEvidence: evidence}
			before, _ := json.Marshal(input)
			output, err := NewResolver(opener, "secret", "https://gateway.example").ResolveDirect(identity.WithOwnerID(context.Background(), resolverOwner), input)
			if err != nil {
				t.Fatal(err)
			}
			if output.ImageURLs[0] != tc.target || output.VideoURLs[0] != tc.target || output.AudioURLs[0] != tc.target || output.VideoURL != tc.target || output.AudioURL != tc.target || output.ReferenceVoices[tc.target] != tc.target {
				t.Fatalf("wrong source: %+v", output)
			}
			output.ImageURLs[0] = "changed"
			output.VideoURLs[0] = "changed"
			output.AudioURLs[0] = "changed"
			output.ReferenceVoices[tc.target] = "changed"
			after, _ := json.Marshal(input)
			if string(after) != string(before) || input.ReferenceMediaEvidence[canonical].Duration != 4 {
				t.Fatal("dispatch mutated accepted input/evidence")
			}
			if local.calls != 0 || direct.calls != 0 {
				t.Fatal("URL resolution transferred media bytes")
			}
			if tc.direct && direct.directCalls != 1 {
				t.Fatalf("duplicate URL was not cached: %d", direct.directCalls)
			}
		})
	}
}

func TestGW253ExternalAndTamperedCapabilitiesNeverUseStorageEvidence(t *testing.T) {
	canonical := resolverURL("https://gateway.example")
	raw := []string{"https://vendor.example/video.mp4", strings.Replace(canonical, resolverID, "469eda6a-8f12-4b5b-b5f7-b505e3cea208", 1), canonical + "&signature=other", strings.Replace(canonical, "/references/", "/%72eferences/", 1)}
	opener := &sourceBindingDirectOpener{}
	out, err := NewResolver(opener, "secret", "https://gateway.example").ResolveDirect(context.Background(), core.MediaOptions{VideoURLs: raw})
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		if out.VideoURLs[i] != raw[i] {
			t.Fatal("external URL was rewritten")
		}
	}
	if opener.directCalls != 0 || opener.calls != 0 {
		t.Fatal("unverified URL reached storage")
	}
}

func TestGW253ResolutionRejectsForeignOwnerAndStorageErrors(t *testing.T) {
	opener := &sourceBindingDirectOpener{directErr: errors.New("storage unavailable")}
	resolver := NewResolver(opener, "secret", "https://gateway.example")
	input := core.MediaOptions{VideoURLs: []string{resolverURL("https://substituted.invalid")}}
	if _, err := resolver.ResolveDirect(identity.WithOwnerID(context.Background(), "foreign"), input); err == nil || opener.directCalls != 0 {
		t.Fatal("foreign reference reached storage")
	}
	if _, err := resolver.ResolveDirect(identity.WithOwnerID(context.Background(), resolverOwner), input); !errors.Is(err, opener.directErr) {
		t.Fatal("storage failure used caller URL fallback", err)
	}
}

func TestGW253InlineBudgetFallbackUsesCanonicalSource(t *testing.T) {
	opener := &openerStub{owner: resolverOwner, data: []byte("unused"), reportedSize: 40 << 20}
	altered := resolverURL("https://substituted.invalid") + "&source=different"
	output, err := NewResolver(opener, "secret", "https://gateway.example").Resolve(identity.WithOwnerID(context.Background(), resolverOwner), []string{altered})
	if err != nil || output[0] != resolverURL("https://gateway.example") || !opener.closed {
		t.Fatalf("budget fallback source=%v err=%v", output, err)
	}
}

type sourceBindingAliasSigner struct{ calls map[string]int }

func (s *sourceBindingAliasSigner) Open(context.Context, string) (Image, storage.Object, error) {
	return Image{}, storage.Object{}, errors.New("alias URL resolution must never open media")
}
func (s *sourceBindingAliasSigner) DirectReferenceURL(ctx context.Context, id string) (string, error) {
	if identity.OwnerID(ctx) != resolverOwner {
		return "", errors.New("wrong owner")
	}
	s.calls[id]++
	// A changing signature proves aliases must share one storage resolution.
	return fmt.Sprintf("https://bucket.example/%s?call=%d", id, s.calls[id]), nil
}

func TestGW253ReviewResolverVoiceAliasesHaveDeterministicIntent(t *testing.T) {
	visualURL := resolverURL("https://gateway.example")
	voiceID := "469eda6a-8f12-4b5b-b5f7-b505e3cea208"
	otherVoiceID := "569eda6a-8f12-4b5b-b5f7-b505e3cea208"
	capability := func(id string) string {
		return "https://gateway.example/references/" + resolverOwner + "/" + id + "?signature=" + Signature([]byte("secret"), resolverOwner, id)
	}
	voiceURL, otherVoiceURL := capability(voiceID), capability(otherVoiceID)
	random := rand.New(rand.NewSource(253))
	for _, direct := range []bool{false, true} {
		for _, conflict := range []bool{false, true} {
			expectedError := ""
			for attempt := 0; attempt < 64; attempt++ {
				secondVoice := strings.Replace(voiceURL, "gateway.example", "voice-alias.invalid", 1) + "&alias=second"
				if conflict {
					secondVoice = otherVoiceURL
				}
				entries := [][2]string{{strings.Replace(visualURL, "gateway.example", "visual-alias.invalid", 1), voiceURL}, {visualURL + "&alias=second", secondVoice}}
				voices := map[string]string{}
				for _, i := range random.Perm(len(entries)) {
					voices[entries[i][0]] = entries[i][1]
				}
				input := core.MediaOptions{ImageURLs: []string{visualURL}, ReferenceVoices: voices}
				before, _ := json.Marshal(input)
				var opener ImageOpener = &openerStub{owner: resolverOwner}
				signer := &sourceBindingAliasSigner{calls: map[string]int{}}
				if direct {
					opener = signer
				}
				output, err := NewResolver(opener, "secret", "https://gateway.example").ResolveDirect(identity.WithOwnerID(context.Background(), resolverOwner), input)
				after, _ := json.Marshal(input)
				if !bytes.Equal(before, after) {
					t.Fatal("resolver mutated accepted aliases")
				}
				if conflict {
					if !errors.Is(err, core.ErrOptionsInvalid) || !strings.Contains(err.Error(), "conflicting voices") {
						t.Fatalf("conflicting aliases accepted: %v %+v", err, output.ReferenceVoices)
					}
					if expectedError == "" {
						expectedError = err.Error()
					}
					if err.Error() != expectedError {
						t.Fatal("map order changed rejection", err)
					}
				} else {
					expectedVisual, expectedVoice := visualURL, voiceURL
					if direct {
						expectedVisual = fmt.Sprintf("https://bucket.example/%s?call=1", resolverID)
						expectedVoice = fmt.Sprintf("https://bucket.example/%s?call=1", voiceID)
					}
					if err != nil || len(output.ReferenceVoices) != 1 || output.ReferenceVoices[expectedVisual] != expectedVoice {
						t.Fatal("identical aliases changed intent", err, output.ReferenceVoices)
					}
					output.ReferenceVoices[expectedVisual] = "changed"
					unchanged, _ := json.Marshal(input)
					if !bytes.Equal(before, unchanged) {
						t.Fatal("resolved voice map shares accepted storage")
					}
					if direct && (signer.calls[resolverID] != 1 || signer.calls[voiceID] != 1) {
						t.Fatal("aliases received different storage resolutions", signer.calls)
					}
				}
			}
		}
	}
}

func TestGW253ReviewDirectStorageEvidenceFollowsTransientURL(t *testing.T) {
	canonical := resolverURL("https://gateway.example")
	for _, raw := range []string{canonical, resolverURL("https://visual-alias.invalid") + "&alias=other"} {
		input := core.MediaOptions{VideoURLs: []string{raw}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{canonical: {Type: "video", Duration: 4}}}
		opener := &sourceBindingDirectOpener{target: "https://bucket.example/owner/video?signature=synthetic"}
		output, err := NewResolver(opener, "secret", "https://gateway.example").ResolveDirect(identity.WithOwnerID(context.Background(), resolverOwner), input)
		if err != nil {
			t.Fatal(err)
		}
		duration, known := output.InputVideoDuration()
		if !known || duration != 4 || len(output.ReferenceMediaEvidence) != 1 || output.ReferenceMediaEvidence[opener.target].Duration != 4 {
			t.Fatalf("direct signing lost inspected evidence: duration=%v known=%v evidence=%v", duration, known, output.ReferenceMediaEvidence)
		}
		output.ReferenceMediaEvidence[opener.target] = core.InputMediaEvidence{Type: "video", Duration: 99}
		if len(input.ReferenceMediaEvidence) != 1 || input.ReferenceMediaEvidence[canonical].Duration != 4 || input.VideoURLs[0] != raw {
			t.Fatal("transient evidence shared accepted map")
		}
	}
}

func TestGW253ReviewRemappedEvidenceAliasesMustAgree(t *testing.T) {
	canonical := resolverURL("https://gateway.example")
	alias := resolverURL("https://visual-alias.invalid") + "&alias=other"
	for _, conflict := range []bool{false, true} {
		for attempt := 0; attempt < 32; attempt++ {
			evidence := map[string]core.InputMediaEvidence{}
			keys := []string{canonical, alias}
			if attempt%2 == 1 {
				keys[0], keys[1] = keys[1], keys[0]
			}
			for _, raw := range keys {
				duration := 4.0
				if conflict && raw == alias {
					duration = 99
				}
				evidence[raw] = core.InputMediaEvidence{Type: "video", Duration: duration}
			}
			input := core.MediaOptions{VideoURLs: []string{canonical}, ReferenceMediaEvidence: evidence}
			before, _ := json.Marshal(input)
			opener := &sourceBindingDirectOpener{target: "https://bucket.example/owner/video?signature=synthetic"}
			output, err := NewResolver(opener, "secret", "https://gateway.example").ResolveDirect(identity.WithOwnerID(context.Background(), resolverOwner), input)
			if conflict {
				if !errors.Is(err, core.ErrOptionsInvalid) || !strings.Contains(err.Error(), "conflicting evidence") {
					t.Fatal("conflicting aliases overwrote inspected evidence", err, output.ReferenceMediaEvidence)
				}
			} else {
				duration, known := output.InputVideoDuration()
				if err != nil || !known || duration != 4 || len(output.ReferenceMediaEvidence) != 1 {
					t.Fatal("identical inspected aliases did not collapse", err, output.ReferenceMediaEvidence)
				}
			}
			after, _ := json.Marshal(input)
			if !bytes.Equal(before, after) {
				t.Fatal("evidence remapping changed accepted input")
			}
		}
	}
}
