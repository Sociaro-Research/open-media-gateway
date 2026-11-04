package companyinterface_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestCompanyFacadeFaceAndSessionActionUseInspectedCentralState(t *testing.T) {
	f, library, ctx := companyResourceFixture(t)
	v := f.current(t)
	ref, e := library.Upload(ctx, "session.mp4", bytes.NewReader(testmedia.MP4(4000, 1000, 120)))
	if e != nil {
		t.Fatal(e)
	}
	inspected, e := library.Inspect(ctx, ref.ID)
	if e != nil {
		t.Fatal(e)
	}
	face := fmt.Sprintf(`{"provider":"kling","workflow_model":%q,"reference_id":%q}`, kling.LipSync, ref.ID)
	expectFixture(t, resourceDirect(f, "POST", "/v1/media-inputs/faces/estimate", face, v.Tokens.PlaygroundKey, ""), 200)
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/media-inputs/faces/estimate", face, v.Tokens.PlaygroundKey, true, nil), 200)
	duration := inspected.Info.Duration
	repo := postgres.NewProviderResourceRepository(f.centralDB)
	job, _, e := repo.ReserveCreate(ctx, providerresources.Job{ProviderID: "kling", ConnectionID: "kling", Revision: resourceFixtureRevision, Intent: providerresources.Intent{Kind: "editing_session", Subtype: "selection", Name: "Inspected editing session", Sources: []core.ResourceSourceEvidence{{ReferenceID: ref.ID, MediaType: "video", SHA256: inspected.SHA256, Duration: &duration}}}}, "session-fixture")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = repo.StartJob(ctx, job.ID); e != nil {
		t.Fatal(e)
	}
	session := &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceVideoDuration: duration, SourceSHA256: inspected.SHA256, FrameCount: 120, FPS: 30}
	binding, e := repo.CompleteCreate(ctx, job.ID, "private-editing-session", session)
	if e != nil {
		t.Fatal(e)
	}
	action := fmt.Sprintf(`{"workflow_model":%q,"expected_revision":0,"frame":{"frame_index":0,"points":[{"x":0.123456789012345,"y":0.5}]},"max_expected_usd":0.01}`, kling.MultiElementsEditing)
	actionPath := "/v1/provider-resources/" + binding.ID + "/actions/add"
	expectFixture(t, resourceDirect(f, "POST", actionPath+"/estimate", action, v.Tokens.PlaygroundKey, ""), 200)
	expectFixture(t, facadeRequest(t, f, "POST", actionPath+"/estimate", action, v.Tokens.PlaygroundKey, true, nil), 200)
	for _, tc := range []struct{ path, body, idem, model, consumer string }{{"/v1/media-inputs/faces", face, "face-once", kling.FaceDetection, kling.LipSync}, {actionPath, action, "action-once", kling.SessionAdd, kling.MultiElementsEditing}} {
		t.Run(tc.idem, func(t *testing.T) {
			before := countFixture(t, f.centralDB, "tasks")
			direct := resourceDirect(f, "POST", tc.path, tc.body, v.Tokens.PlaygroundKey, tc.idem)
			expectFixture(t, direct, 202)
			local := facadeRequest(t, f, "POST", tc.path, tc.body, v.Tokens.PlaygroundKey, true, func(r *http.Request) { r.Header.Set("Idempotency-Key", tc.idem) })
			expectFixture(t, local, 202)
			var a, b struct {
				ID        string
				StatusURL string `json:"status_url"`
			}
			json.Unmarshal(direct.Body.Bytes(), &a)
			json.Unmarshal(local.Body.Bytes(), &b)
			if a.ID == "" || a.ID != b.ID || b.StatusURL != ci.LocalCustomerPrefix+"v1/generations/"+a.ID || direct.Header().Get("Location") != "" || local.Header().Get("Location") != b.StatusURL || countFixture(t, f.centralDB, "tasks") != before+1 {
				t.Fatal("resource admission/replay mapping changed")
			}
			task, e := postgres.NewTaskRepository(f.centralDB).Get(ctx, a.ID)
			if e != nil {
				t.Fatal(e)
			}
			if task.Model != tc.model || task.ProviderIdentity != resourceFixtureRevision || task.OrganizationID != v.Tokens.Company || task.ActorID != v.Tokens.Actor {
				t.Fatal("trusted preparation attribution changed")
			}
			if tc.idem == "action-once" && (task.Options.SessionAction == nil || task.Options.SessionAction.Frame.Points[0].X != .123456789012345 || len(task.Options.ResourceSnapshots) != 1 || task.Options.ResourceSnapshots[0].RemoteID != "private-editing-session") {
				t.Fatal("action lost precise inspected session intent")
			}
			if strings.Contains(local.Body.String(), inspected.SHA256) || strings.Contains(local.Body.String(), "private-editing-session") {
				t.Fatal("private provenance leaked")
			}
		})
	}
	_, foreignKey := resourceCoworkerKey(t, f, "member")
	expectFixture(t, facadeRequest(t, f, "POST", actionPath, action, foreignKey, true, nil), 404)
	forged := strings.TrimSuffix(face, "}") + `,"source_url":"https://foreign.invalid/input"}`
	expectFixture(t, facadeRequest(t, f, "POST", "/v1/media-inputs/faces", forged, v.Tokens.PlaygroundKey, true, nil), 403)
}
