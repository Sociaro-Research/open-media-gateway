package catalogsync

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// CountSelector is an exact bounded interval, including zero. Only validated
// typed resources/roles participate; client-supplied metadata never supplies it.
type ResourceCountSelector struct {
	Kind    string `json:"kind"`
	Subtype string `json:"subtype,omitempty"`
	Min     int    `json:"min"`
	Max     int    `json:"max"`
}
type RoleCountSelector struct {
	Kind string `json:"kind"`
	Role string `json:"role"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
}

func (r Rate) ValidateConditions() error {
	if len(r.Parameters) > 32 || len(r.ResourceInputs) > 16 || len(r.ReferenceRoles) > 16 {
		return fmt.Errorf("too many tariff selectors")
	}
	for key, value := range r.Parameters {
		if strings.TrimSpace(key) == "" || len(key) > 64 || len(value) > 256 {
			return fmt.Errorf("invalid typed parameter selector")
		}
	}
	for _, c := range r.ResourceInputs {
		if c.Kind == "" || len(c.Kind) > 64 || len(c.Subtype) > 64 || c.Min < 0 || c.Max < c.Min || c.Max > 64 {
			return fmt.Errorf("invalid resource count selector")
		}
	}
	for _, c := range r.ReferenceRoles {
		if c.Kind != "image" && c.Kind != "video" && c.Kind != "audio" || c.Role == "" || len(c.Role) > 64 || c.Min < 0 || c.Max < c.Min || c.Max > 64 {
			return fmt.Errorf("invalid reference-role selector")
		}
	}
	return nil
}
func (r Rate) MatchesConditions(model core.MediaModel, request core.MediaRequest) bool {
	if r.ValidateConditions() != nil {
		return false
	}
	counts, err := core.CountMediaInputs(model, request.Options)
	if err != nil {
		return false
	}
	if r.AudioInput != nil && *r.AudioInput != (counts.Audios > 0) {
		return false
	}
	if r.VoiceInput != nil && *r.VoiceInput != (request.Options.Voice != "") {
		return false
	}
	if r.ImageInput != nil && *r.ImageInput != (counts.Images > 0) {
		return false
	}
	for key, want := range r.Parameters {
		actual, ok := request.Options.Parameters[key]
		if !ok {
			for _, field := range model.Parameters {
				if field.Key == key {
					actual = field.Default
					break
				}
			}
		}
		if actual == nil || fmt.Sprint(actual) != want {
			return false
		}
	}
	for _, selector := range r.ResourceInputs {
		count := 0
		for _, resource := range request.Options.ResourceSnapshots {
			if resource.Kind == selector.Kind && (selector.Subtype == "" || resource.Subtype == selector.Subtype) {
				count++
			}
		}
		if count < selector.Min || count > selector.Max {
			return false
		}
	}
	for _, selector := range r.ReferenceRoles {
		count := 0
		total := counts.Count(selector.Kind)
		for i := 0; i < total; i++ {
			if core.ReferenceRole(model, request.Options, selector.Kind, i) == selector.Role {
				count++
			}
		}
		if count < selector.Min || count > selector.Max {
			return false
		}
	}
	return true
}
func (r Rate) ConditionKey() string {
	resources := append([]ResourceCountSelector(nil), r.ResourceInputs...)
	roles := append([]RoleCountSelector(nil), r.ReferenceRoles...)
	sort.Slice(resources, func(i, j int) bool {
		a, _ := json.Marshal(resources[i])
		b, _ := json.Marshal(resources[j])
		return string(a) < string(b)
	})
	sort.Slice(roles, func(i, j int) bool {
		a, _ := json.Marshal(roles[i])
		b, _ := json.Marshal(roles[j])
		return string(a) < string(b)
	})
	raw, _ := json.Marshal(struct {
		Voice          *bool
		Image          *bool
		Parameters     map[string]string
		Resources      []ResourceCountSelector
		Roles          []RoleCountSelector
		VideoResources bool
	}{Voice: r.VoiceInput, Image: r.ImageInput, Parameters: r.Parameters, Resources: resources, Roles: roles, VideoResources: r.VideoInputIncludesResources})
	if r.AudioInput != nil {
		raw = append(raw, []byte(fmt.Sprintf("/audio_input=%t", *r.AudioInput))...)
	}
	if r.VoiceInput != nil {
		return string(raw) + fmt.Sprintf("/voice=%t", *r.VoiceInput)
	}
	return string(raw)
}
func (r Rate) ConditionSpecificity() int {
	n := len(r.Parameters) + len(r.ResourceInputs) + len(r.ReferenceRoles)
	if r.AudioInput != nil {
		n++
	}
	if r.VoiceInput != nil {
		n++
	}
	if r.ImageInput != nil {
		n++
	}
	return n
}
