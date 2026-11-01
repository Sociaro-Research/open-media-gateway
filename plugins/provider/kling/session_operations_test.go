package kling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func sessionFixtureContext() context.Context {
	return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "session-owner"), core.ProviderConnectionScope{ID: "kling--session-fixture", OwnerID: "provider-owner", Revision: "session-revision"})
}
func sessionFixture(id string) (core.MediaModel, core.MediaRequest) {
	m, _ := sessionModel(id)
	r := core.MediaRequest{Model: id, TaskID: "d45de672-3245-49b7-85b5-410d89a284bf"}
	sourceID := "b0bfffd5-89d9-4b1f-abdf-bf5b1c75c12c"
	if id == SessionInit {
		source := core.ResourceSourceInput{ReferenceID: sourceID, URL: "https://cdn.example/source.mp4", MediaType: "video", ContentType: "video/mp4", SizeBytes: 4096, Width: 1280, Height: 720, SHA256: strings.Repeat("a", 64), Duration: ptr(4.0), FPS: 30, FrameCount: 120}
		r.Options.ResourceIntent = &core.ResourceIntent{Kind: "editing_session", Subtype: "selection", Name: "Synthetic session", ReferenceIDs: []string{sourceID}}
		r.Options.ResourceCreation = &core.ResourceCreateRequest{Kind: "editing_session", Subtype: "selection", Name: "Synthetic session", Videos: []core.ResourceSourceInput{source}}
		return m, r
	}
	bindingID := "a6dd8e89-3b7c-40cf-8dc8-e7534c42371d"
	e := &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceVideoDuration: 4, SourceSHA256: strings.Repeat("a", 64), FrameCount: 120, FPS: 30, SelectionRevision: 0}
	r.Options.ResourceReferences = []core.ResourceReference{{BindingID: bindingID, ReferenceID: "editing"}}
	r.Options.ResourceSnapshots = []core.ResourceSnapshot{{BindingID: bindingID, ReferenceID: "editing", Kind: "editing_session", Subtype: "selection", RemoteID: "90071992547409931", ConnectionIdentity: "session-revision", Session: e}}
	r.Options.SessionAction = &core.SessionActionIntent{BindingID: bindingID, Action: m.SessionOperation.Action, ExpectedRevision: 0}
	if id == SessionAdd || id == SessionDelete {
		r.Options.SessionAction.Frame = &core.FrameSelection{FrameIndex: 0, Points: []core.SelectionPoint{{X: 0.7738498789346246, Y: 0.297142857142857}}}
	}
	if id == SessionDelete {
		e.Selections = []core.FrameSelection{*r.Options.SessionAction.Frame}
		e.Selections[0].Points = append([]core.SelectionPoint(nil), r.Options.SessionAction.Frame.Points...)
		e.HasSelection = true
	}
	return m, r
}
func sessionMaskReply(t *testing.T, session string, remain bool) string {
	t.Helper()
	masks := "[]"
	if remain {
		var out bytes.Buffer
		if err := png.Encode(&out, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		masks = fmt.Sprintf(`[{"object_id":0,"rle_mask":{"size":[2,2],"counts":"04"},"png_mask":{"size":[2,2],"base64":%q}}]`, base64.StdEncoding.EncodeToString(out.Bytes()))
	}
	return fmt.Sprintf(`{"code":0,"data":{"status":0,"session_id":%q,"res":{"frame_index":0,"rle_mask_list":%s}}}`, session, masks)
}
func sessionAssertPrivateJSON(t *testing.T, task core.ProviderTask) {
	t.Helper()
	for _, a := range task.Artifacts {
		if a.Text != "" {
			if !json.Valid([]byte(a.Text)) || strings.Contains(a.Text, "90071992547409931") || strings.Contains(a.Text, "https://") || strings.Contains(a.Text, "session_id") || strings.Contains(a.Text, "session-revision") {
				t.Fatalf("native identity/source leaked into public summary: %s", a.Text)
			}
		}
	}
}

func TestSessionOperationsDescriptorsAndConfirmedPricing(t *testing.T) {
	if len(sessionModels()) != 5 || isSessionOperation("kling-unreviewed-session") {
		t.Fatal(sessionModels())
	}
	a := &Adapter{operationPrices: map[string]money.USD{}}
	for _, m := range sessionModels() {
		t.Run(m.ID, func(t *testing.T) {
			_, r := sessionFixture(m.ID)
			if !isSessionOperation(m.ID) || !m.NoPrompt || !m.NoSeed || !m.NoWatermark || m.SupportsAudio {
				t.Fatal(m)
			}
			if m.ID == SessionInit {
				if m.SessionOperation != nil || m.ResourceOperation == nil || m.ResourceOperation.Kind != "editing_session" || !reflect.DeepEqual(m.ResourceOperation.Subtypes, []string{"selection"}) {
					t.Fatal(m)
				}
			} else if m.SessionOperation == nil || m.SessionOperation.FrameIndexBase != 0 || m.SessionOperation.MaxFrames != 10 || m.SessionOperation.MaxPoints != 10 || m.SessionOperation.Mutates != (m.ID != SessionPreview) {
				t.Fatal(m)
			}
			normalized, err := core.NormalizeModelOptions(m, r.Options)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.validateSessionOperation(sessionFixtureContext(), m, normalized); err != nil {
				t.Fatal(err)
			}
			if _, _, err := a.quoteSessionOperation(m, r.Options); !errors.Is(err, core.ErrPricingUnavailable) {
				t.Fatal("unknown tariff became free", err)
			}
			key := strings.ReplaceAll(strings.TrimPrefix(m.ID, "kling-"), "-", "_")
			a.operationPrices[key] = 12345
			cost, _, err := a.quoteSessionOperation(m, r.Options)
			if err != nil || cost.ExpectedUSD != 12345 || cost.EstimatedUnits != 1 || cost.Pricing.Unit != "request" || !cost.Pricing.AccountSpecific || cost.ActualUSD != nil {
				t.Fatal(cost, err)
			}
			delete(a.operationPrices, key)
		})
	}
}

func TestSessionOperationsInitUsesAcceptedInspectedVideo(t *testing.T) {
	m, r := sessionFixture(SessionInit)
	before := core.CloneResourceCreation(r.Options.ResourceCreation)
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		if req.Method != "POST" || req.URL.Path != "/v1/videos/multi-elements/init-selection" {
			t.Fatal(req.URL)
		}
		var p map[string]any
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if len(p) != 1 || p["video_url"] != before.Videos[0].URL {
			t.Fatalf("unreviewed native init controls: %#v", p)
		}
		return response(200, `{"code":0,"data":{"status":0,"session_id":"90071992547409931","fps":30,"total_frame":120,"width":720,"height":1280,"original_duration":4000,"normalized_video":"https://cdn.example/normalized.mp4"}}`), nil
	}))
	started := time.Now().UTC()
	task, err := a.generateSessionOperation(sessionFixtureContext(), m, r)
	if err != nil || task.ID != "" || len(task.Resources) != 1 || len(task.Artifacts) != 2 || task.ResultURL != "https://cdn.example/normalized.mp4" {
		t.Fatal(task, err)
	}
	resource := task.Resources[0]
	if resource.Kind != "editing_session" || resource.Subtype != "selection" || resource.Name != "Synthetic session" || resource.Session.SelectionRevision != 0 || resource.Session.HasSelection || resource.Session.FrameCount != 120 || resource.Session.FPS != 30 || resource.Session.ExpiresAt.Before(started.Add(24*time.Hour)) || resource.Session.ExpiresAt.After(time.Now().UTC().Add(24*time.Hour)) {
		t.Fatal(resource)
	}
	if task.Usage == nil || task.Usage.Currency != "" || task.Usage.EstimatedCost != nil {
		t.Fatal(task.Usage)
	}
	*r.Options.ResourceCreation.Videos[0].Duration = 99
	if *resource.Sources[0].Duration != 4 || resource.Session.SourceVideoDuration != 4 {
		t.Fatal("caller changed accepted evidence", resource)
	}
	if !reflect.DeepEqual(before.Videos[0].FrameCount, resource.Sources[0].FrameCount) {
		t.Fatal(resource)
	}
	sessionAssertPrivateJSON(t, task)
}

func TestSessionOperationsMutationsPreserveFrameZeroAndExactFloats(t *testing.T) {
	for _, id := range []string{SessionAdd, SessionDelete, SessionClear} {
		t.Run(id, func(t *testing.T) {
			m, r := sessionFixture(id)
			original := core.CloneMediaSession(r.Options.ResourceSnapshots[0].Session)
			if r.Options.ResourceSnapshots[0].Session.Selections == nil {
				original.Selections = nil
			}
			if !reflect.DeepEqual(original, r.Options.ResourceSnapshots[0].Session) {
				t.Fatal("immutability baseline differs before dispatch")
			}
			calls := 0
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				calls++
				suffix := map[string]string{SessionAdd: "add-selection", SessionDelete: "delete-selection", SessionClear: "clear-selection"}[id]
				if req.Method != "POST" || req.URL.Path != "/v1/videos/multi-elements/"+suffix {
					t.Fatal(req.URL)
				}
				var p map[string]any
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if p["session_id"] != "90071992547409931" {
					t.Fatal(p)
				}
				if id == SessionClear {
					if len(p) != 1 {
						t.Fatal(p)
					}
					return response(200, `{"code":0,"data":{"status":0,"session_id":"90071992547409931"}}`), nil
				}
				points := p["points"].([]any)
				point := points[0].(map[string]any)
				if len(p) != 3 || p["frame_index"] != float64(0) || len(points) != 1 || point["x"] != 0.7738498789346246 || point["y"] != 0.297142857142857 {
					t.Fatal("frame zero or exact coordinates changed", p)
				}
				return response(200, sessionMaskReply(t, "90071992547409931", id == SessionAdd)), nil
			}))
			task, err := a.generateSessionOperation(sessionFixtureContext(), m, r)
			if err != nil || calls != 1 || task.ID != "" || len(task.Resources) != 1 || task.Resources[0].Session.SelectionRevision != 1 {
				t.Fatal(task, err, calls)
			}
			if !reflect.DeepEqual(original, r.Options.ResourceSnapshots[0].Session) {
				t.Fatal("accepted snapshot mutated")
			}
			if task.Resources[0].Session.HasSelection != (id == SessionAdd) {
				t.Fatal(task.Resources[0].Session)
			}
			if id == SessionAdd {
				if len(task.Artifacts) != 3 || task.Artifacts[0].MediaType != "json" || task.Artifacts[1].Format != "png" || task.Artifacts[2].ID != "session_state" {
					t.Fatal(task.Artifacts)
				}
				var summary auxiliaryMaskSummary
				if err := json.Unmarshal([]byte(task.Artifacts[0].Text), &summary); err != nil || summary.Masks[0].ArtifactID != "2" {
					t.Fatal("PNG archive index changed", summary, err)
				}
			}
			sessionAssertPrivateJSON(t, task)
		})
	}
}

func TestSessionOperationsPreviewIsReadOnlyAndPreservesTracking(t *testing.T) {
	m, r := sessionFixture(SessionPreview)
	original := core.CloneMediaSession(r.Options.ResourceSnapshots[0].Session)
	if r.Options.ResourceSnapshots[0].Session.Selections == nil {
		original.Selections = nil
	}
	if !reflect.DeepEqual(original, r.Options.ResourceSnapshots[0].Session) {
		t.Fatal("immutability baseline differs before dispatch")
	}
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		if req.Method != "POST" || req.URL.Path != "/v1/videos/multi-elements/preview-selection" {
			t.Fatal(req.URL)
		}
		var p map[string]any
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if len(p) != 1 || p["session_id"] != "90071992547409931" {
			t.Fatal(p)
		}
		return response(200, `{"code":0,"data":{"status":0,"session_id":"90071992547409931","res":{"video":"https://cdn.example/preview.mp4","video_cover":"https://cdn.example/cover.png","tracking_output":"https://cdn.example/unknown-export"}}}`), nil
	}))
	task, err := a.generateSessionOperation(sessionFixtureContext(), m, r)
	if err != nil || task.ID != "" || len(task.Resources) != 0 || len(task.Artifacts) != 4 || task.ResultURL != "https://cdn.example/preview.mp4" {
		t.Fatal("preview attempted resource update or dropped a variant", task, err)
	}
	if task.Artifacts[0].MediaType != "video" || task.Artifacts[1].MediaType != "image" || task.Artifacts[2].MediaType != "binary" || task.Artifacts[2].SourceReference == "" || task.Artifacts[2].URL != "" || task.Artifacts[2].Text != "" || strings.Contains(task.Artifacts[2].SourceReference, "https://") {
		t.Fatal("tracking source not safely sealed", task.Artifacts)
	}
	if !reflect.DeepEqual(original, r.Options.ResourceSnapshots[0].Session) {
		t.Fatal("preview mutated input binding")
	}
	sessionAssertPrivateJSON(t, task)
}

func TestSessionOperationsRejectBeforeHTTPAndReservation(t *testing.T) {
	cases := []struct {
		name, id string
		edit     func(*core.MediaRequest)
	}{
		{"revision zero valid but mismatch rejected", SessionAdd, func(r *core.MediaRequest) { r.Options.SessionAction.ExpectedRevision = 1 }},
		{"negative revision", SessionClear, func(r *core.MediaRequest) { r.Options.SessionAction.ExpectedRevision = -1 }},
		{"revision overflow", SessionClear, func(r *core.MediaRequest) {
			r.Options.ResourceSnapshots[0].Session.SelectionRevision = math.MaxInt64
			r.Options.SessionAction.ExpectedRevision = math.MaxInt64
		}},
		{"action mismatch", SessionAdd, func(r *core.MediaRequest) { r.Options.SessionAction.Action = "delete" }},
		{"frame outside count", SessionAdd, func(r *core.MediaRequest) { r.Options.SessionAction.Frame.FrameIndex = 120 }},
		{"negative frame", SessionAdd, func(r *core.MediaRequest) { r.Options.SessionAction.Frame.FrameIndex = -1 }},
		{"nonfinite point", SessionAdd, func(r *core.MediaRequest) { r.Options.SessionAction.Frame.Points[0].X = math.NaN() }},
		{"too many points", SessionAdd, func(r *core.MediaRequest) {
			for i := 0; i < 10; i++ {
				r.Options.SessionAction.Frame.Points = append(r.Options.SessionAction.Frame.Points, core.SelectionPoint{X: float64(i) / 10, Y: .5})
			}
		}},
		{"duplicate point", SessionAdd, func(r *core.MediaRequest) {
			r.Options.SessionAction.Frame.Points = append(r.Options.SessionAction.Frame.Points, r.Options.SessionAction.Frame.Points[0])
		}},
		{"already added point", SessionAdd, func(r *core.MediaRequest) {
			r.Options.ResourceSnapshots[0].Session.Selections = []core.FrameSelection{*r.Options.SessionAction.Frame}
			r.Options.ResourceSnapshots[0].Session.HasSelection = true
		}},
		{"cumulative points exceed ten", SessionAdd, func(r *core.MediaRequest) {
			points := []core.SelectionPoint{}
			for i := 0; i < 10; i++ {
				points = append(points, core.SelectionPoint{X: float64(i) / 10, Y: .5})
			}
			r.Options.ResourceSnapshots[0].Session.Selections = []core.FrameSelection{{FrameIndex: 0, Points: points}}
			r.Options.ResourceSnapshots[0].Session.HasSelection = true
		}},
		{"eleventh selected frame", SessionAdd, func(r *core.MediaRequest) {
			e := r.Options.ResourceSnapshots[0].Session
			for i := 1; i <= 10; i++ {
				e.Selections = append(e.Selections, core.FrameSelection{FrameIndex: i, Points: []core.SelectionPoint{{X: .1, Y: .2}}})
			}
			e.HasSelection = true
		}},
		{"delete not exactly same float", SessionDelete, func(r *core.MediaRequest) {
			r.Options.SessionAction.Frame.Points[0].X = math.Nextafter(r.Options.SessionAction.Frame.Points[0].X, 1)
		}},
		{"delete unselected frame", SessionDelete, func(r *core.MediaRequest) { r.Options.SessionAction.Frame.FrameIndex = 1 }},
		{"recover add forbidden", SessionAdd, func(r *core.MediaRequest) { r.Options.SessionAction.Recover = true }},
		{"preview frame forbidden", SessionPreview, func(r *core.MediaRequest) { r.Options.SessionAction.Frame = &core.FrameSelection{FrameIndex: 0} }},
		{"expired session", SessionAdd, func(r *core.MediaRequest) {
			r.Options.ResourceSnapshots[0].Session.ExpiresAt = time.Now().Add(-time.Second)
		}},
		{"unresolved binding", SessionAdd, func(r *core.MediaRequest) { r.Options.ResourceSnapshots = nil }},
		{"wrong binding", SessionAdd, func(r *core.MediaRequest) { r.Options.SessionAction.BindingID = "b0bfffd5-89d9-4b1f-abdf-bf5b1c75c12c" }},
		{"wrong resource kind", SessionAdd, func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Kind = "face_session" }},
		{"raw native id", SessionAdd, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"session_id": "90071992547409931"} }},
		{"raw media URL", SessionInit, func(r *core.MediaRequest) { r.Options.VideoURLs = []string{"https://cdn.example/direct.mp4"} }},
		{"missing inspected FPS", SessionInit, func(r *core.MediaRequest) { r.Options.ResourceCreation.Videos[0].FPS = 0 }},
		{"unsupported inspected FPS", SessionInit, func(r *core.MediaRequest) { r.Options.ResourceCreation.Videos[0].FPS = 29.97 }},
		{"missing frame count", SessionInit, func(r *core.MediaRequest) { r.Options.ResourceCreation.Videos[0].FrameCount = 0 }},
		{"unsupported duration gap", SessionInit, func(r *core.MediaRequest) { r.Options.ResourceCreation.Videos[0].Duration = ptr(6.0) }},
		{"unexpected second source", SessionInit, func(r *core.MediaRequest) {
			r.Options.ResourceCreation.Videos = append(r.Options.ResourceCreation.Videos, r.Options.ResourceCreation.Videos[0])
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, r := sessionFixture(tc.id)
			tc.edit(&r)
			calls := 0
			a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected HTTP") }))
			if err := a.validateSessionOperation(sessionFixtureContext(), m, r.Options); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatalf("invalid action accepted before admission: %v", err)
			}
			if _, err := a.generateSessionOperation(sessionFixtureContext(), m, r); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestSessionOperationsScopeRecoveryAndUncertainMutation(t *testing.T) {
	m, r := sessionFixture(SessionClear)
	r.Options.SessionAction.Recover = true
	r.Options.ResourceSnapshots[0].Session.HasSelection = true // Deliberately uncertain prior state.
	calls := 0
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"code":0,"data":{"status":0,"session_id":"90071992547409931"}}`), nil
	}))
	if _, err := a.generateSessionOperation(sessionFixtureContext(), m, r); err != nil || calls != 1 {
		t.Fatal("explicit recovery clear failed", err, calls)
	}
	r.Options.ResourceSnapshots[0].ConnectionIdentity = "different-revision"
	if _, err := a.generateSessionOperation(sessionFixtureContext(), m, r); !errors.Is(err, core.ErrProviderChanged) || calls != 1 {
		t.Fatal("cross-revision mutation dispatched", err, calls)
	}
	m, r = sessionFixture(SessionAdd)
	calls = 0
	a = adapter(t, transport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("transport details must stay private")
	}))
	if _, err := a.generateSessionOperation(sessionFixtureContext(), m, r); !errors.Is(err, ErrAuxiliarySessionStateUncertain) || strings.Contains(err.Error(), "transport details") || calls != 1 {
		t.Fatal(err, calls)
	}
	if _, err := a.generateSessionOperation(context.Background(), m, r); err == nil || calls != 1 {
		t.Fatal("missing owner/connection scope reached provider", err, calls)
	}
}
