package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestResourceIntentDiagnosticsKeepConstraints(t *testing.T) {
	model := MediaModel{ResourceOperation: &ResourceOperationSchema{Kind: "voice", Subtypes: []string{"custom"}, NameMaxCharacters: 4, SupportsDescription: true, DescriptionRequired: true, DescriptionMaxCharacters: 8, MaxTags: 2}}
	valid := ResourceIntent{Kind: "voice", Subtype: "custom", Name: "😀😀😀😀", Description: "safe", ReferenceIDs: []string{uuid.NewString()}, Tags: []string{"a", "b"}}
	if err := ValidateResourceCreationIntent(model, MediaOptions{ResourceIntent: &valid}, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		change      func(*ResourceIntent)
		field, hint string
	}{
		{"name", func(i *ResourceIntent) { i.Name = " " }, "name", "nonempty"},
		{"name length", func(i *ResourceIntent) { i.Name = "秘密名字長" }, "name", "maximum 4"},
		{"description", func(i *ResourceIntent) { i.Description = "" }, "description", "required"},
		{"description length", func(i *ResourceIntent) { i.Description = strings.Repeat("秘", 9) }, "description", "maximum 8"},
		{"subtype", func(i *ResourceIntent) { i.Subtype = "private-value" }, "subtype", "custom"},
		{"uuid", func(i *ResourceIntent) { i.ReferenceIDs = []string{"private-value"} }, "reference_ids.0", "UUID"},
		{"duplicate", func(i *ResourceIntent) { i.ReferenceIDs = append(i.ReferenceIDs, i.ReferenceIDs[0]) }, "reference_ids.1", "duplicate"},
		{"tags", func(i *ResourceIntent) { i.Tags = []string{"a", "b", "c"} }, "tags", "at most 2"},
		{"tag length", func(i *ResourceIntent) { i.Tags = []string{strings.Repeat("秘", 43)} }, "tags.0", "128 bytes"},
		{"source", func(i *ResourceIntent) { i.ReferenceIDs = nil }, "reference_ids", "source"},
		{"task", func(i *ResourceIntent) { i.GeneratedTaskID = "private-value" }, "generated_task_id", "UUID"},
		{"binding", func(i *ResourceIntent) { i.BoundVoiceBindingID = "private-value" }, "bound_voice_binding_id", "UUID"},
		{"artifact", func(i *ResourceIntent) { i.GeneratedArtifactID = "private-value" }, "generated_artifact_id", "generated_task_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent := CloneResourceIntent(&valid)
			tc.change(intent)
			err := ValidateResourceCreationIntent(model, MediaOptions{ResourceIntent: intent}, true)
			if !errors.Is(err, ErrOptionsInvalid) || !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), tc.hint) || strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), "秘") {
				t.Fatal(err)
			}
		})
	}
}
