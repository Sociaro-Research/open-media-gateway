package postgres

import (
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
)

func TestProviderResourceFailedElementDeletionRetainsBoundVoice(t *testing.T) {
	for _, failure := range []string{"provider terminal failure", "adapter pre-I/O rejection"} {
		t.Run(failure, func(t *testing.T) {
			f := newProviderResourceFixture(t)
			create := f.start(t, "element-with-voice")
			element, err := f.repo.CompleteResource(f.ctx, create.ID, core.ProviderResource{
				RemoteID: "synthetic-element", Kind: "element", Subtype: "video_character", Status: core.ProviderSucceeded,
				BoundVoice: &core.ProviderResource{RemoteID: "synthetic-bound-voice", Kind: "voice", Status: core.ProviderSucceeded},
			})
			if err != nil || element.BoundVoiceBindingID == "" {
				t.Fatal("create element with bound voice", element, err)
			}
			deletion, created, err := f.repo.BeginDelete(f.ctx, element.ID, "failed-element-deletion")
			if err != nil || !created {
				t.Fatal("reserve element deletion", deletion, created, err)
			}
			if _, started, err := f.repo.StartJob(f.ctx, deletion.ID); err != nil || !started {
				t.Fatal("start element deletion", started, err)
			}
			// Both explicit remote failure and proven pre-I/O rejection use
			// FailJob. Neither outcome establishes that the element was deleted.
			if err := f.repo.FailJob(f.ctx, deletion.ID, failure, ""); err != nil {
				t.Fatal("record element deletion failure", err)
			}
			failedElement, err := f.repo.GetBinding(f.ctx, element.ID)
			if err != nil || failedElement.Status != providerresources.Failed {
				t.Fatal("failed deletion binding", failedElement, err)
			}
			voiceID := element.BoundVoiceBindingID
			if _, _, err := f.repo.BeginDelete(f.ctx, voiceID, "delete-voice-after-element"); !errors.Is(err, providerresources.ErrInUse) {
				t.Fatalf("failed element deletion released its still-bound voice: %v", err)
			}
			voice, err := f.repo.GetBinding(f.ctx, voiceID)
			if err != nil || voice.Status != providerresources.Succeeded {
				t.Fatal("rejected voice deletion changed its binding", voice, err)
			}

			retry, created, err := f.repo.BeginDelete(f.ctx, element.ID, "confirmed-element-deletion")
			if err != nil || !created {
				t.Fatal("reserve explicit element retry", retry, created, err)
			}
			if _, started, err := f.repo.StartJob(f.ctx, retry.ID); err != nil || !started {
				t.Fatal("start explicit element retry", started, err)
			}
			if err := f.repo.CompleteDelete(f.ctx, retry.ID); err != nil {
				t.Fatal("confirm element deletion", err)
			}
			// Reuse the rejected request's key: the blocked transaction must
			// neither reserve a job nor make the voice permanently undeletable.
			voiceDeletion, created, err := f.repo.BeginDelete(f.ctx, voiceID, "delete-voice-after-element")
			if err != nil || !created || voiceDeletion.BindingID != voiceID {
				t.Fatal("confirmed element deletion did not release its voice", voiceDeletion, created, err)
			}
		})
	}
}
