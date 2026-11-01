package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestReportDetailKeepsResourceAndSourceEvidencePrivate(t *testing.T) {
	s, _ := newPersonalTestServer()
	task := detailTask()
	task.Options.ResourceReferences = []core.ResourceReference{{BindingID: "local-binding", ReferenceID: "editing_session_1"}}
	task.Options.ResourceSnapshots = []core.ResourceSnapshot{{BindingID: "local-binding", RemoteID: "private-native-session", ConnectionIdentity: "private-connection-revision", Session: &core.MediaSessionEvidence{SourceSHA256: "private-source-hash", Faces: []core.SessionFace{{ID: "private-native-face"}}}}}
	task.Options.ResourceCreation = &core.ResourceCreateRequest{VoiceSources: []core.ResourceSourceInput{{URL: "https://private-source.test/signed?token=private-token", SHA256: "private-creation-hash"}}}
	task.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{"private-reference": {SHA256: "private-inspection-hash"}}
	task.Options.OverrideSound = &core.SoundOverride{StorageKey: "private-sound-key"}
	s.service = &detailReportService{&accountService{tasks: []core.Task{task}}}
	w := sendTest(s.handler(), http.MethodGet, "/v1/admin/reports/"+task.ID, "", "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, private := range []string{"_reference_resources", "_resource_creation", "_reference_media", "_override_sound", "private-native-session", "private-connection-revision", "private-source-hash", "private-native-face", "private-token", "private-creation-hash", "private-inspection-hash", "private-sound-key"} {
		if strings.Contains(w.Body.String(), private) {
			t.Errorf("private request evidence leaked: %s", private)
		}
	}
	for _, public := range []string{"local-binding", "editing_session_1", "Private extension"} {
		if !strings.Contains(w.Body.String(), public) {
			t.Errorf("public immutable request missing: %s", public)
		}
	}
	if task.Options.ResourceSnapshots[0].RemoteID != "private-native-session" || task.Options.ResourceCreation == nil {
		t.Fatal("response redaction mutated persisted request")
	}
}
