package references

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

func TestInspectedAudioCodecEvidenceReopensWithoutCallerAliasing(t *testing.T) {
	repo, store := &aacUploadRepository{}, &aacUploadStore{}
	library := New(repo, store)
	ctx := identity.WithOwnerID(context.Background(), "codec-fixture-owner")
	item, err := library.Upload(ctx, "actual-aac.m4a", bytes.NewReader(aacUploadM4A(0, false, false)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := library.Inspect(ctx, item.ID)
	if err != nil || first.Info.AudioStreams == nil || *first.Info.AudioStreams != 1 || !reflect.DeepEqual(first.Info.AudioCodecs, []string{"aac"}) {
		t.Fatal(first, err)
	}
	first.Info.AudioCodecs[0] = "caller-change"
	again, err := library.Inspect(ctx, item.ID)
	if err != nil || !reflect.DeepEqual(again.Info.AudioCodecs, []string{"aac"}) {
		t.Fatal("inspection evidence shared caller containers", again, err)
	}
}
func TestResolvedAudioCodecEvidencePreservesCanonicalIdentityAndOwnedContainers(t *testing.T) {
	canonical := resolverURL("https://gateway.example")
	alias := resolverURL("https://visual-alias.invalid") + "&alias=other"
	for _, different := range []bool{false, true} {
		one, two := 2, 2
		proof := core.InputMediaEvidence{Type: "video", AudioStreams: &one, AudioCodecs: []string{"aac", "mp3"}, Duration: 4}
		other := proof
		other.AudioStreams = &two
		other.AudioCodecs = core.CloneAudioCodecs(proof.AudioCodecs)
		if different {
			other.AudioCodecs[1] = "opus"
		}
		input := core.MediaOptions{VideoURLs: []string{canonical}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{canonical: proof, alias: other}}
		opener := &sourceBindingDirectOpener{target: "https://bucket.example/owner/video?signature=synthetic"}
		output, err := NewResolver(opener, "secret", "https://gateway.example").ResolveDirect(identity.WithOwnerID(context.Background(), resolverOwner), input)
		if different {
			if !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal("conflicting codec aliases accepted", err)
			}
			continue
		}
		if err != nil || len(output.ReferenceMediaEvidence) != 1 || !reflect.DeepEqual(output.ReferenceMediaEvidence[opener.target].AudioCodecs, []string{"aac", "mp3"}) {
			t.Fatal("equal codec evidence failed rekey", output, err)
		}
		output.ReferenceMediaEvidence[opener.target].AudioCodecs[0] = "opus"
		if proof.AudioCodecs[0] != "aac" || input.ReferenceMediaEvidence[canonical].AudioCodecs[0] != "aac" {
			t.Fatal("dispatch mutated accepted evidence")
		}
	}
}
