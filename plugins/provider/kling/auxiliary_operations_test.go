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
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func auxiliaryFixture(id string) core.MediaRequest {
	r := core.MediaRequest{Model: id, TaskID: "gateway_auxiliary_fixture"}
	switch id {
	case ImageRecognition:
		r.Options.ImageURLs = []string{"https://cdn.example/input.png"}
	case FaceDetection:
		r.Options.VideoURLs = []string{"https://cdn.example/input.mp4"}
		r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: 4}}
	case LipSync, MultiElementsEditing:
		kind := "face_session"
		session := &core.MediaSessionEvidence{ExpiresAt: time.Now().Add(time.Hour), SourceVideoDuration: 6, SourceSHA256: strings.Repeat("1", 64), Faces: []core.SessionFace{{ID: "0", StartMS: 0, EndMS: 6000}}, FrameCount: 120, FPS: 30, SelectionRevision: 1, HasSelection: true}
		if id == MultiElementsEditing {
			kind = "editing_session"
			session.SourceVideoDuration = 4
			r.Prompt = "Add <<<image_1>>> to <<<video_1>>>"
			r.Options.ImageURLs = []string{"https://cdn.example/cropped.png"}
			r.Options.Parameters = map[string]any{"edit_mode": "addition"}
			r.Options.ReferenceMode = "addition"
		} else {
			r.Options.AudioURLs = []string{"https://cdn.example/sound.mp3"}
			r.Options.Parameters = map[string]any{"face_index": 1, "sound_start_time": 0, "sound_end_time": 3000, "sound_insert_time": 1000}
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.AudioURLs[0]: {Type: "audio", Duration: 4}}
		}
		binding := "11111111-1111-4111-8111-111111111111"
		r.Options.ResourceReferences = []core.ResourceReference{{BindingID: binding, ReferenceID: "session"}}
		r.Options.ResourceSnapshots = []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "session", Kind: kind, RemoteID: "9007199254740993123", ConnectionIdentity: "fixture-revision", Session: session}}
	}
	return r
}
func auxiliaryContext() context.Context {
	return legacyScopeFixture("fixture-owner", "fixture-provider-owner", "kling--fixture", "fixture-revision")
}
func TestAuxiliarySynchronousRecognitionAndFaceDetection(t *testing.T) {
	for _, id := range []string{ImageRecognition, FaceDetection} {
		t.Run(id, func(t *testing.T) {
			r := auxiliaryFixture(id)
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if req.Method != "POST" {
					t.Fatal(req.Method)
				}
				if id == ImageRecognition {
					if req.URL.Path != "/v1/videos/image-recognize" || len(payload) != 1 || payload["image"] != r.Options.ImageURLs[0] {
						t.Fatal(req.URL, payload)
					}
					return response(200, `{"code":0,"data":{"final_unit_deduction":"0.1","final_balance_deduction":{"quota":"0.01","list_price":"0.014"},"task_result":{"images":[{"type":"object_seg","is_contain":true,"url":"https://cdn.example/object.png","secret":"sensitive"},{"type":"face_seg","is_contain":false}]},"secret":"sensitive"}}`), nil
				}
				if req.URL.Path != "/v1/videos/identify-face" || len(payload) != 1 || payload["video_url"] != r.Options.VideoURLs[0] {
					t.Fatal(req.URL, payload)
				}
				return response(200, `{"code":0,"data":{"session_id":"native-private-session","face_data":[{"face_id":"0","face_image":"https://cdn.example/face.png","start_time":0,"end_time":4000,"secret":"sensitive"}],"secret":"sensitive"}}`), nil
			})
			task, err := a.generateAuxiliary(auxiliaryContext(), r)
			if err != nil || task.ID != "" || task.ResultURL != "" || len(task.Artifacts) != 2 || task.Artifacts[0].MediaType != "json" || !json.Valid([]byte(task.Artifacts[0].Text)) || task.Artifacts[1].MediaType != "image" {
				t.Fatal(task, err)
			}
			summary := task.Artifacts[0].Text
			if strings.Contains(summary, "https:") || strings.Contains(summary, "sensitive") || strings.Contains(summary, "native-private-session") || !strings.Contains(summary, `"artifact_id":"2"`) {
				t.Fatal("raw provider metadata leaked into durable summary", summary)
			}
			quote, _, err := a.quoteAuxiliary(r)
			want := int64(14000)
			if id == FaceDetection {
				want = 7000
			}
			if err != nil || int64(quote.ExpectedUSD) != want || quote.Pricing.Unit != "request" || quote.ActualUSD != nil {
				t.Fatal(quote, err)
			}
		})
	}
}
func TestAuxiliaryManagedSessionPayloads(t *testing.T) {
	for _, id := range []string{LipSync, MultiElementsEditing} {
		t.Run(id, func(t *testing.T) {
			r := auxiliaryFixture(id)
			r.Options.Watermark = ptr(true)
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				var p map[string]any
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if req.Method != "POST" || req.URL.Path != auxiliaryVideoPath(id) || p["session_id"] != "9007199254740993123" || p["external_task_id"] != r.TaskID || p["watermark_info"].(map[string]any)["enabled"] != true || p["callback_url"] != nil || p["resolution"] != nil {
					t.Fatal(req.URL, p)
				}
				if id == LipSync {
					faces := p["face_choose"].([]any)
					f := faces[0].(map[string]any)
					if len(faces) != 1 || f["face_id"] != "0" || f["sound_file"] != r.Options.AudioURLs[0] || f["audio_id"] != nil || f["sound_start_time"] != float64(0) || f["sound_end_time"] != float64(3000) || f["sound_insert_time"] != float64(1000) || f["sound_volume"] != float64(1) || f["original_audio_volume"] != float64(1) {
						t.Fatal(p)
					}
				} else {
					if p["duration"] != "5" || p["mode"] != "std" || p["edit_mode"] != "addition" || p["prompt"] != r.Prompt || len(p["image_list"].([]any)) != 1 {
						t.Fatal(p)
					}
				}
				return response(200, `{"code":0,"data":{"task_id":"9007199254740993999","task_status":"submitted"}}`), nil
			})
			task, err := a.generateAuxiliary(auxiliaryContext(), r)
			if err != nil {
				t.Fatal(err)
			}
			handle, err := a.parseLegacy(auxiliaryContext(), task.ID)
			if err != nil || handle.ID != "9007199254740993999" || handle.Operation != id {
				t.Fatal(handle, err)
			}
			if _, _, err := a.quoteAuxiliary(r); !errors.Is(err, core.ErrPricingUnavailable) {
				t.Fatal("undocumented rounding or mode/resolution mapping became a tariff", err)
			}
		})
	}
}
func TestAuxiliarySessionAndInputValidation(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached provider")
		return nil, nil
	})
	for _, id := range []string{ImageRecognition, FaceDetection, LipSync, MultiElementsEditing} {
		cases := map[string]func(*core.MediaRequest){"raw ID": func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"session_id": "unchecked"} }, "resolution": func(r *core.MediaRequest) { r.Options.Resolution = "720p" }, "audio switch": func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(false) }}
		if id == LipSync || id == MultiElementsEditing {
			cases["missing snapshot"] = func(r *core.MediaRequest) { r.Options.ResourceSnapshots = nil }
			cases["missing session evidence"] = func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Session = nil }
			cases["expired"] = func(r *core.MediaRequest) {
				r.Options.ResourceSnapshots[0].Session.ExpiresAt = time.Now().Add(-time.Second)
			}
			cases["no trusted checksum"] = func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Session.SourceSHA256 = "" }
			if id == LipSync {
				cases["zero based face"] = func(r *core.MediaRequest) { r.Options.Parameters["face_index"] = 0 }
				cases["unknown face"] = func(r *core.MediaRequest) { r.Options.Parameters["face_index"] = 2 }
				cases["short crop"] = func(r *core.MediaRequest) { r.Options.Parameters["sound_end_time"] = 1999 }
				cases["audio overflow"] = func(r *core.MediaRequest) { r.Options.Parameters["sound_end_time"] = 5000 }
				cases["video overflow"] = func(r *core.MediaRequest) { r.Options.Parameters["sound_insert_time"] = 5000 }
				cases["no face overlap"] = func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Session.Faces[0].StartMS = 3001 }
				cases["volume overflow"] = func(r *core.MediaRequest) { r.Options.Parameters["sound_volume"] = 2.1 }
			} else {
				cases["unmarked removal"] = func(r *core.MediaRequest) {
					r.Options.ImageURLs = nil
					r.Options.ReferenceMode = "removal"
					r.Options.Parameters["edit_mode"] = "removal"
					r.Options.ResourceSnapshots[0].Session.HasSelection = false
				}
				cases["input duration gap"] = func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Session.SourceVideoDuration = 6 }
				cases["wrong output duration"] = func(r *core.MediaRequest) { r.Options.Duration = 10 }
				cases["missing native mention"] = func(r *core.MediaRequest) { r.Prompt = "edit the video" }
			}
		} else {
			cases["prompt"] = func(r *core.MediaRequest) { r.Prompt = "ignored" }
			cases["watermark"] = func(r *core.MediaRequest) { r.Options.Watermark = ptr(false) }
		}
		for name, mutate := range cases {
			t.Run(id+"/"+name, func(t *testing.T) {
				r := auxiliaryFixture(id)
				mutate(&r)
				if _, err := a.generateAuxiliary(auxiliaryContext(), r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestAuxiliaryMalformedRecognitionNeverReturnsPartialArtifacts(t *testing.T) {
	for _, raw := range []string{`{}`, `{"task_result":{"images":[{"type":"unknown","is_contain":false}]}}`, `{"task_result":{"images":[{"type":"object_seg","is_contain":true}]}}`, `{"task_result":{"images":[{"type":"object_seg","url":"https://cdn.example/image.png"}]}}`, `{"task_result":{"images":[{"type":"object_seg","is_contain":true,"url":"https://127.0.0.1/image.png"}]}}`} {
		if task, err := normalizeImageRecognition(json.RawMessage(raw)); err == nil || len(task.Artifacts) != 0 {
			t.Fatal(raw, task, err)
		}
	}
	for _, raw := range []string{`{}`, `{"session_id":"session","face_data":[{"face_id":"0","start_time":0,"end_time":0}]}`, `{"session_id":"session","face_data":[{"face_id":"0","start_time":0,"end_time":60001}]}`, `{"session_id":"session","face_data":[{"face_id":"0","start_time":0,"end_time":4000,"face_image":"http://cdn.example/image.png"}]}`} {
		if task, _, err := normalizeFaceDetection(json.RawMessage(raw)); err == nil || len(task.Artifacts) != 0 {
			t.Fatal(raw, task, err)
		}
	}
}
func TestAuxiliaryLegacyPollingPreservesOutputsAndStates(t *testing.T) {
	for _, operation := range []string{LipSync, MultiElementsEditing} {
		for _, state := range []string{"submitted", "processing", "failed", "succeed"} {
			t.Run(operation+"/"+state, func(t *testing.T) {
				resultJSON := `"task_result":{"videos":[{"id":"9007199254740993123","url":"https://cdn.example/video.mp4","watermark_url":"https://cdn.example/watermark.mp4","duration":"5"}]}`
				a := adapter(t, func(req *http.Request) (*http.Response, error) {
					if req.Method != "GET" || req.URL.Path != auxiliaryVideoPath(operation)+"/remote" {
						t.Fatal(req.URL)
					}
					return response(200, fmt.Sprintf(`{"code":0,"data":{"task_id":"remote","task_status":%q,"task_status_msg":"sensitive",%s}}`, state, resultJSON)), nil
				})
				got, err := a.getAuxiliaryTask(auxiliaryContext(), operation, "remote")
				if err != nil {
					t.Fatal(err)
				}
				if state == "succeed" && (got.Status != core.ProviderSucceeded || len(got.Artifacts) != 2 || got.ResultURL != "https://cdn.example/video.mp4" || got.Usage == nil || got.Usage.Metrics[0].Quantity != 5) {
					t.Fatal(got)
				}
				if strings.Contains(got.Error, "sensitive") {
					t.Fatal("provider diagnostic leaked")
				}
			})
		}
	}
	for _, raw := range []string{`{"task_id":"different","task_status":"submitted"}`, `{"task_id":"remote","task_status":"succeeded"}`, `{"task_id":"remote","task_status":"succeed","task_result":{"videos":[]}}`, `{"task_id":"remote","task_status":"succeed","task_result":{"videos":[{"id":"id","url":"https://cdn.example/video.mp4","duration":"NaN"}]}}`} {
		if _, err := normalizeAuxiliaryVideo(LipSync, "remote", json.RawMessage(raw)); err == nil {
			t.Fatal(raw)
		}
	}
}

func auxiliarySource() core.ResourceSourceInput {
	duration := 4.0
	return core.ResourceSourceInput{ReferenceID: "11111111-1111-4111-8111-111111111111", URL: "https://cdn.example/source.mp4", MediaType: "video", ContentType: "video/mp4", SizeBytes: 1000, Width: 720, Height: 1280, SHA256: strings.Repeat("1", 64), Duration: &duration}
}
func TestAuxiliarySessionInitializationUsesInspectedSource(t *testing.T) {
	for _, kind := range []string{"face_session", "editing_session"} {
		t.Run(kind, func(t *testing.T) {
			source := auxiliarySource()
			expiry := time.Now().Add(30 * time.Minute)
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				var p map[string]any
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if len(p) != 1 || p["video_url"] != source.URL || req.Method != "POST" {
					t.Fatal(p, req.Method)
				}
				if kind == "face_session" {
					if req.URL.Path != "/v1/videos/identify-face" {
						t.Fatal(req.URL)
					}
					return response(200, `{"code":0,"data":{"session_id":"private-session","face_data":[{"face_id":"private-face","start_time":0,"end_time":4000,"face_image":"https://cdn.example/face.png"}]}}`), nil
				}
				if req.URL.Path != "/v1/videos/multi-elements/init-selection" {
					t.Fatal(req.URL)
				}
				return response(200, `{"code":0,"data":{"status":0,"session_id":"private-session","fps":30,"total_frame":120,"width":720,"height":1280,"original_duration":4000,"normalized_video":"https://cdn.example/normalized.mp4"}}`), nil
			})
			got, err := a.initializeAuxiliarySession(auxiliaryContext(), kind, source, expiry)
			if err != nil || got.Resource.Kind != kind || got.Resource.RemoteID != "private-session" || got.Resource.Session.SourceVideoDuration != 4 || got.Resource.Session.SourceSHA256 != source.SHA256 || len(got.Artifacts) == 0 {
				t.Fatal(got, err)
			}
			*source.Duration = 99
			if got.Resource.Session.SourceVideoDuration != 4 || *got.Resource.Sources[0].Duration != 4 {
				t.Fatal("caller changed accepted source evidence")
			}
			if kind == "face_session" {
				if !got.Resource.Session.ExpiresAt.Equal(expiry) || got.Resource.Session.Faces[0].ID != "private-face" || got.Resource.Session.Faces[0].PreviewArtifactID != "2" {
					t.Fatal(got.Resource.Session)
				}
			} else if got.Resource.Session.FrameCount != 120 || got.Resource.Session.FPS != 30 || time.Until(got.Resource.Session.ExpiresAt) < 23*time.Hour {
				t.Fatal(got.Resource.Session)
			}
		})
	}
	calls := 0
	a := adapter(t, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })
	if _, err := a.initializeAuxiliarySession(auxiliaryContext(), "face_session", auxiliarySource(), time.Time{}); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal(err)
	}
	source := auxiliarySource()
	source.SHA256 = ""
	if _, err := a.initializeAuxiliarySession(auxiliaryContext(), "editing_session", source, time.Time{}); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("unverified source reached provider")
	}
}
func TestAuxiliarySelectionLifecycleRetainsTypedMasksAndState(t *testing.T) {
	r := auxiliaryFixture(MultiElementsEditing)
	binding := r.Options.ResourceSnapshots[0]
	binding.Session.HasSelection = false
	binding.Session.SelectionRevision = 0
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	encodedPNG := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	action := "add"
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		var p map[string]any
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p["session_id"] != binding.RemoteID || req.URL.Path != "/v1/videos/multi-elements/"+map[string]string{"add": "add-selection", "delete": "delete-selection", "clear": "clear-selection", "preview": "preview-selection"}[action] {
			t.Fatal(req.URL, p)
		}
		if action == "add" || action == "delete" {
			if p["frame_index"] != float64(0) || len(p["points"].([]any)) != 1 {
				t.Fatal("frame zero or typed points omitted", p)
			}
		} else if len(p) != 1 {
			t.Fatal(p)
		}
		switch action {
		case "add":
			return response(200, fmt.Sprintf(`{"code":0,"data":{"status":0,"session_id":%q,"res":{"frame_index":0,"rle_mask_list":[{"object_id":0,"rle_mask":{"size":[2,2],"counts":"04"},"png_mask":{"size":[2,2],"base64":%q}}]}}}`, binding.RemoteID, encodedPNG)), nil
		case "delete":
			return response(200, fmt.Sprintf(`{"code":0,"data":{"status":0,"session_id":%q,"res":{"frame_index":0,"rle_mask_list":[]}}}`, binding.RemoteID)), nil
		case "clear":
			return response(200, fmt.Sprintf(`{"code":0,"data":{"status":0,"session_id":%q}}`, binding.RemoteID)), nil
		default:
			return response(200, fmt.Sprintf(`{"code":0,"data":{"status":0,"session_id":%q,"res":{"video":"https://cdn.example/mask.mp4","video_cover":"https://cdn.example/cover.png","tracking_output":"https://cdn.example/tracking-export"}}}`, binding.RemoteID)), nil
		}
	})
	frame := &core.FrameSelection{FrameIndex: 0, Points: []core.SelectionPoint{{X: .25, Y: .5}}}
	got, err := a.mutateAuxiliarySelection(auxiliaryContext(), binding, "add", frame)
	if err != nil || !got.Resource.Session.HasSelection || got.Resource.Session.SelectionRevision != 1 || len(got.Resource.Session.Selections) != 1 || len(got.Artifacts) != 2 || !json.Valid([]byte(got.Artifacts[0].Text)) || !strings.HasPrefix(got.Artifacts[1].URL, "data:image/png;base64,") {
		t.Fatal(got, err)
	}
	if len(binding.Session.Selections) != 0 || binding.Session.HasSelection {
		t.Fatal("mutation changed accepted snapshot")
	}
	frame.Points[0].X = .9
	if got.Resource.Session.Selections[0].Points[0].X != .25 {
		t.Fatal("caller point slice mutated session evidence")
	}
	frame.Points[0].X = .25
	binding.Session = got.Resource.Session
	action = "delete"
	got, err = a.mutateAuxiliarySelection(auxiliaryContext(), binding, "delete", frame)
	if err != nil || got.Resource.Session.HasSelection || got.Resource.Session.SelectionRevision != 2 || len(got.Resource.Session.Selections) != 0 {
		t.Fatal(got, err)
	}
	binding.Session = got.Resource.Session
	action = "clear"
	got, err = a.mutateAuxiliarySelection(auxiliaryContext(), binding, "clear", nil)
	if err != nil || got.Resource.Session.SelectionRevision != 3 {
		t.Fatal(got, err)
	}
	action = "preview"
	got, err = a.previewAuxiliarySelection(auxiliaryContext(), binding)
	if err != nil || len(got.Artifacts) != 2 || got.TrackingURL != "https://cdn.example/tracking-export" || got.Resource.Session != nil && got.Resource.Session == binding.Session {
		t.Fatal(got, err)
	}
}
func TestAuxiliarySelectionBoundsAndExactDeletionBeforeNetwork(t *testing.T) {
	r := auxiliaryFixture(MultiElementsEditing)
	binding := r.Options.ResourceSnapshots[0]
	binding.Session.Selections = []core.FrameSelection{{FrameIndex: 0, Points: []core.SelectionPoint{{X: .5, Y: .5}}}}
	calls := 0
	a := adapter(t, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })
	for name, frame := range map[string]*core.FrameSelection{
		"outside frame":         {FrameIndex: 120, Points: []core.SelectionPoint{{X: .5, Y: .5}}},
		"negative coordinate":   {FrameIndex: 1, Points: []core.SelectionPoint{{X: -.1, Y: .5}}},
		"nonfinite coordinate":  {FrameIndex: 1, Points: []core.SelectionPoint{{X: math.NaN(), Y: .5}}},
		"duplicate coordinates": {FrameIndex: 1, Points: []core.SelectionPoint{{X: .5, Y: .5}, {X: .5, Y: .5}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := a.mutateAuxiliarySelection(auxiliaryContext(), binding, "add", frame); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
	if _, err := a.mutateAuxiliarySelection(auxiliaryContext(), binding, "delete", &core.FrameSelection{FrameIndex: 0, Points: []core.SelectionPoint{{X: .50001, Y: .5}}}); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("inexact deletion accepted", err)
	}
	for i := 1; i < 10; i++ {
		binding.Session.Selections = append(binding.Session.Selections, core.FrameSelection{FrameIndex: i, Points: []core.SelectionPoint{{X: .5, Y: .5}}})
	}
	if _, err := a.mutateAuxiliarySelection(auxiliaryContext(), binding, "add", &core.FrameSelection{FrameIndex: 10, Points: []core.SelectionPoint{{X: .5, Y: .5}}}); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("eleventh frame accepted", err)
	}
	foreign := legacyScopeFixture("fixture-owner", "fixture-provider-owner", "kling--fixture", "different-revision")
	if _, err := a.previewAuxiliarySelection(foreign, binding); !errors.Is(err, core.ErrProviderChanged) {
		t.Fatal("foreign revision accepted session", err)
	}
	if calls != 0 {
		t.Fatal("invalid session mutation reached provider")
	}
}
func TestAuxiliaryManagedAudioSelectsOnlyResolvedRemoteID(t *testing.T) {
	r := auxiliaryFixture(LipSync)
	r.Options.AudioURLs = nil
	r.Options.ReferenceMode = "managed_audio"
	binding := "22222222-2222-4222-8222-222222222222"
	duration := 4.0
	r.Options.ResourceReferences = append(r.Options.ResourceReferences, core.ResourceReference{BindingID: binding, ReferenceID: "audio"})
	r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, core.ResourceSnapshot{BindingID: binding, ReferenceID: "audio", Kind: "audio", RemoteID: "9007199254740993999", ConnectionIdentity: "fixture-revision", Sources: []core.ResourceSourceEvidence{{MediaType: "audio", Duration: &duration}}})
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		var p auxiliaryLipPayload
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Faces[0].AudioID != "9007199254740993999" || p.Faces[0].SoundFile != "" {
			t.Fatal(p)
		}
		return response(200, `{"code":0,"data":{"task_id":"lip-task","task_status":"submitted"}}`), nil
	})
	if _, err := a.generateAuxiliary(auxiliaryContext(), r); err != nil {
		t.Fatal(err)
	}
}

func TestAuxiliaryUncertainMutationDoesNotCommitPrivateState(t *testing.T) {
	r := auxiliaryFixture(MultiElementsEditing)
	binding := r.Options.ResourceSnapshots[0]
	binding.Session.Selections = nil
	binding.Session.SelectionRevision = 0
	binding.Session.HasSelection = false
	for _, raw := range []string{`{"code":0,"data":{"status":0,"session_id":"different"}}`, `{"code":0,"data":{"status":0,"session_id":"9007199254740993123","res":{"frame_index":0,"rle_mask_list":[]}}}`} {
		calls := 0
		a := adapter(t, func(*http.Request) (*http.Response, error) { calls++; return response(200, raw), nil })
		got, err := a.mutateAuxiliarySelection(auxiliaryContext(), binding, "add", &core.FrameSelection{FrameIndex: 0, Points: []core.SelectionPoint{{X: .5, Y: .5}}})
		if !errors.Is(err, ErrAuxiliarySessionStateUncertain) || calls != 1 || got.Resource.Session != nil || binding.Session.SelectionRevision != 0 || binding.Session.HasSelection || len(binding.Session.Selections) != 0 {
			t.Fatal("uncertain provider mutation became accepted state", got, err, calls)
		}
	}
}
