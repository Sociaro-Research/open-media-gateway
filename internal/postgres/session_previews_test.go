package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestSessionPreviewMustBelongToSameArchivedResult(t *testing.T) {
	r := core.ProviderResource{Kind: "face_session", Session: &core.MediaSessionEvidence{ExpiresAt: time.Now().Add(time.Hour), SourceVideoDuration: 6, SourceSHA256: strings.Repeat("a", 64), Faces: []core.SessionFace{{ID: "private-face", StartMS: 0, EndMS: 6000, PreviewArtifactID: "2"}}}}
	artifacts := []core.StoredArtifact{{ID: "2", MediaType: "image", StorageKey: "attempt/artifacts/2/image.png", SizeBytes: 32, SHA256: strings.Repeat("b", 64)}}
	if err := validateSessionPreviewArtifacts([]core.ProviderResource{r}, artifacts); err != nil {
		t.Fatal("actual worker selector rejected", err)
	}
	for name, items := range map[string][]core.StoredArtifact{"missing": nil, "other artifact": {{ID: "3", MediaType: "image", StorageKey: "k", SizeBytes: 32, SHA256: "hash"}}, "non-image": {{ID: "2", MediaType: "video", StorageKey: "k", SizeBytes: 32, SHA256: "hash"}}, "unarchived": {{ID: "2", MediaType: "image"}}, "duplicate": {artifacts[0], artifacts[0]}} {
		t.Run(name, func(t *testing.T) {
			if validateSessionPreviewArtifacts([]core.ProviderResource{r}, items) == nil {
				t.Fatal("unbound preview accepted")
			}
		})
	}
	for _, id := range []string{"0", "65", "02", "../../other", "https://provider.test/image.png"} {
		r.Session.Faces[0].PreviewArtifactID = id
		if validateSessionPreviewArtifacts([]core.ProviderResource{r}, artifacts) == nil {
			t.Fatal("unsafe selector accepted", id)
		}
	}
}
