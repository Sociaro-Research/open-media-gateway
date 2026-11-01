package referencevalidation

import (
	"errors"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
)

func roleBool(v bool) *bool { return &v }
func roleInt(v int) *int    { return &v }
func roleModel() core.MediaModel {
	return core.MediaModel{ID: "role-fixture", ReferenceRoleSchemas: []core.ReferenceRoleSchema{{Kind: "video", Roles: []string{"reference", "source"}, Default: "reference"}}, ReferenceRoleLimits: []core.MediaRoleLimits{{Kind: "video", Role: "reference", Limits: core.MediaFileLimits{MinDuration: 3, MaxDuration: 10, MinDimension: 300, MinFrames: 24}}, {Kind: "video", Role: "source", Limits: core.MediaFileLimits{MinDuration: 3, MaxDuration: 30, MinDimension: 500, MinFrames: 24, RequireAudio: roleBool(false)}}}}
}
func roleVideo(duration float64) File {
	return File{Type: "video", Name: "Synthetic video", Info: mediafile.Info{ContentType: "video/mp4", Width: 720, Height: 720, Duration: duration, FPS: 24, FrameCount: 96, AudioStreams: roleInt(0), VideoStreams: roleInt(1), Size: 1000}}
}
func roleOptions() core.MediaOptions {
	return core.MediaOptions{VideoURLs: []string{"https://cdn.example/reference.mp4", "https://cdn.example/source.mp4"}, ReferenceRoles: map[string]string{"video_2": "source"}}
}
func TestRoleFilesPreserveDistinctSourceAndReferenceLimits(t *testing.T) {
	model := roleModel()
	options := roleOptions()
	files := []File{roleVideo(5), roleVideo(20)}
	if err := ValidateRoleFiles(model, options, files); err != nil {
		t.Fatal("source borrowed shorter reference limit", err)
	}
	files[0].Info.Duration = 20
	if err := ValidateRoleFiles(model, options, files); !errors.Is(err, ErrInvalid) {
		t.Fatal("reference borrowed longer source limit", err)
	}
	// Global limits are enforced separately by ValidateRequestFiles. This helper
	// must neither inherit them into a role nor mutate the original descriptor.
	model.ReferenceMediaLimits = &core.ReferenceMediaLimits{Video: core.MediaFileLimits{MaxDuration: 1, MinDimension: 2000}}
	files[0].Info.Duration = 5
	if err := ValidateRoleFiles(model, options, files); err != nil {
		t.Fatal("global fallback amplified role limits", err)
	}
	if model.ReferenceMediaLimits.Video.MaxDuration != 1 || model.ReferenceRoleLimits[1].Limits.MaxDuration != 30 {
		t.Fatal("role validation mutated model")
	}
}
func TestRoleFilesUseOriginalPositionsForInspectedSubsets(t *testing.T) {
	model := roleModel()
	options := roleOptions()
	source := roleVideo(20)
	source.ReferenceIndex = 2
	if err := ValidateRoleFiles(model, options, []File{source}); err != nil {
		t.Fatal("owned video_2 shifted into external video_1 role", err)
	}
	source.ReferenceIndex = 0
	if err := ValidateRoleFiles(model, options, []File{source}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "original input position") {
		t.Fatal("ambiguous subset inferred a role", err)
	}
	for _, index := range []int{-1, 3} {
		source.ReferenceIndex = index
		if err := ValidateRoleFiles(model, options, []File{source}); !errors.Is(err, ErrInvalid) {
			t.Fatal(index, err)
		}
	}
	source.ReferenceIndex = 2
	if err := ValidateRoleFiles(model, options, []File{source, source}); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate reference address accepted", err)
	}
}
func TestRoleFrameAndAudioChecksRequireIndependentEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		frames  int64
		audio   *int
		require *bool
		valid   bool
	}{
		{"unknown frames", 0, roleInt(0), roleBool(false), false}, {"too few frames", 23, roleInt(0), roleBool(false), false}, {"minimum frames", 24, roleInt(0), roleBool(false), true}, {"unknown audio", 24, nil, roleBool(false), false}, {"known no audio", 24, roleInt(0), roleBool(false), true}, {"audio present", 24, roleInt(1), roleBool(false), false}, {"audio required absent", 24, roleInt(0), roleBool(true), false}, {"audio required present", 24, roleInt(2), roleBool(true), true}, {"negative audio count", 24, roleInt(-1), roleBool(true), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := roleModel()
			model.ReferenceRoleLimits[1].Limits.RequireAudio = tc.require
			file := roleVideo(4)
			file.ReferenceIndex = 2
			file.Info.FrameCount = tc.frames
			file.Info.AudioStreams = tc.audio
			err := ValidateRoleFiles(model, roleOptions(), []File{file})
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
	model := roleModel()
	model.ReferenceRoleLimits[1].Limits.MinFrames = 0
	model.ReferenceRoleLimits[1].Limits.MaxFrames = 100
	file := roleVideo(4)
	file.ReferenceIndex = 2
	file.Info.FrameCount = 101
	if err := ValidateRoleFiles(model, roleOptions(), []File{file}); !errors.Is(err, ErrInvalid) {
		t.Fatal("frame maximum ignored", err)
	}
	model.ReferenceRoleLimits = nil
	file.Info.FrameCount = 0
	file.Info.AudioStreams = nil
	if err := ValidateRoleFiles(model, core.MediaOptions{}, []File{file}); err != nil {
		t.Fatal("undeclared role constraints invented", err)
	}
}
func TestRoleFilesUsePerKindOrderAndExistingRoleLimits(t *testing.T) {
	model := roleModel()
	model.ReferenceRoleLimits = append(model.ReferenceRoleLimits, core.MediaRoleLimits{Kind: "image", Role: "reference", Limits: core.MediaFileLimits{ContentTypes: []string{"image/png"}, MaxBytes: 100}})
	options := roleOptions()
	options.ImageURLs = []string{"https://cdn.example/image.png"}
	image := File{Type: "image", Info: mediafile.Info{ContentType: "image/png", Size: 99}}
	files := []File{roleVideo(5), image, roleVideo(20)}
	if err := ValidateRoleFiles(model, options, files); err != nil {
		t.Fatal("kind ordering shifted video roles", err)
	}
	image.Info.Size = 100
	if err := ValidateRoleFiles(model, options, []File{image}); !errors.Is(err, ErrInvalid) {
		t.Fatal("existing strict size boundary was changed", err)
	}
}
