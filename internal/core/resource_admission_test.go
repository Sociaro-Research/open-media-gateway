package core

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
)

type resourceResolverFixture struct {
	fail     error
	duration float64
}

func (r *resourceResolverFixture) ResolveResourceReferences(_ context.Context, _ string, refs []ResourceReference) ([]ResourceSnapshot, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	out := []ResourceSnapshot{}
	for _, ref := range refs {
		out = append(out, ResourceSnapshot{BindingID: ref.BindingID, ReferenceID: ref.ReferenceID, Kind: "voice", RemoteID: "9007199254740993", ConnectionIdentity: "accepted-revision", Sources: []ResourceSourceEvidence{{MediaType: "audio", Duration: &r.duration}}})
	}
	return out, nil
}
func TestResourceFailurePreservesAcceptedIdempotentIntentAndVoiceChoice(t *testing.T) {
	model := MediaModel{ID: "managed-speech", MediaType: "speech", SupportsVoice: true, RequiresVoice: true, Voices: []string{"preset"}, ResourceInputSchemas: []ResourceInputSchema{{Kind: "voice", MaxCount: 1}}}
	provider := catalogProvider{models: []MediaModel{model}}
	repo := &memoryRepository{}
	resolver := &resourceResolverFixture{duration: 8}
	svc := NewTaskServiceWithProviders(repo, &memoryQueue{}, map[string]MediaProvider{"catalog": provider}, map[string]string{"catalog": model.ID}, "catalog")
	svc.SetReferenceResourceResolver(resolver)
	input := CreateMediaInput{Prompt: "Hello", Options: MediaOptions{ResourceReferences: []ResourceReference{{BindingID: uuid.NewString(), ReferenceID: "speaker"}}}}
	task, err := svc.CreateMediaRequest(context.Background(), input, "resource-original")
	if err != nil {
		t.Fatal(err)
	}
	if task.Options.Voice != "" || len(task.Options.ResourceSnapshots) != 1 {
		t.Fatal("managed voice replaced by preset", task.Options)
	}
	resolver.duration = 22
	if got := *task.Options.ResourceSnapshots[0].Sources[0].Duration; got != 8 {
		t.Fatal("accepted resource evidence mutated", got)
	}
	resolver.fail = errors.New("resource deleted or connection rotated")
	retry, err := svc.CreateMediaRequest(context.Background(), input, "resource-original")
	if err != nil || retry.ID != task.ID || repo.createCount != 1 {
		t.Fatal("accepted request depended on mutable resolver", err, retry)
	}
	if _, err = svc.CreateMediaRequest(context.Background(), input, "new-resource"); !errors.Is(err, resolver.fail) || repo.createCount != 1 {
		t.Fatal("new invalid resource admitted", err)
	}
	if err = svc.ValidateMediaRequest(context.Background(), input); !errors.Is(err, resolver.fail) {
		t.Fatal("validation hid resolver failure", err)
	}
	input.Prompt = "Changed"
	if _, err = svc.CreateMediaRequest(context.Background(), input, "resource-original"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed intent accepted", err)
	}
	input.Prompt = "Hello"
	input.Options.ResourceReferences[0].ReferenceID = "changed"
	if _, err = svc.CreateMediaRequest(context.Background(), input, "resource-original"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed resource alias accepted", err)
	}
}
