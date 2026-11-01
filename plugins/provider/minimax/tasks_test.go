package minimax

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

func nativeResponse(id, model, state, kind string) map[string]any {
	modality := "video"
	content := map[string]string{"url": "https://media.example/result.mp4"}
	if kind == "h3_context_ir" {
		modality = "text"
		content = map[string]string{"prompt": "Structured enhanced prompt"}
	}
	resolution := "768P"
	if kind == "regeneration" {
		resolution = "2K"
	}
	if kind == "h3_context_ir" {
		resolution = ""
	}
	return map[string]any{"task": map[string]any{"id": id, "model": model, "status": state, "task_type": kind, "modality": modality, "resolution": resolution, "duration": 5, "content": content, "usage": map[string]int{"output_seconds": 5, "input_seconds": 0, "input_image_count": 0}}}
}
func TestH3EveryStateAndFamily(t *testing.T) {
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"} {
		for _, state := range []string{"queued", "running", "succeeded", "failed", "cancelled"} {
			t.Run(id+state, func(t *testing.T) {
				a := fixture(t, func(r *http.Request) (*http.Response, error) {
					if r.Method != "GET" || r.URL.Path != "/v2/query/video_generation/123" {
						t.Fatal(r.Method, r.URL)
					}
					return response(200, nativeResponse("123", id, state, "generation")), nil
				})
				token := a.seal(owned("alice"), handle{ID: "123", Model: id, Kind: "h3"})
				out, e := a.GetTask(owned("alice"), token)
				if e != nil {
					t.Fatal(e)
				}
				want := map[string]core.ProviderStatus{"queued": core.ProviderQueued, "running": core.ProviderRunning, "succeeded": core.ProviderSucceeded, "failed": core.ProviderFailed, "cancelled": core.ProviderFailed}[state]
				if out.Status != want {
					t.Fatal(out)
				}
				if state == "succeeded" && (out.ResultURL == "" || out.Usage == nil) {
					t.Fatal(out)
				}
			})
		}
	}
}
func TestLegacyAndAgentPolling(t *testing.T) {
	for _, op := range operations() {
		if op.kind != "legacy" && op.kind != "agent" {
			continue
		}
		kind, model := op.kind, op.id
		for _, state := range []string{"Preparing", "Queueing", "Processing", "Success", "Fail"} {
			t.Run(model+"/"+state, func(t *testing.T) {
				calls := 0
				a := fixture(t, func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.Path == "/v1/files/retrieve" {
						if r.URL.Query().Get("file_id") != "456" {
							t.Fatal(r.URL)
						}
						return response(200, map[string]any{"file": map[string]any{"file_id": "456", "purpose": "video_generation", "download_url": "https://media.example/output.mp4"}, "base_resp": map[string]int{"status_code": 0}}), nil
					}
					out := success("123")
					out["status"] = state
					out["file_id"] = "456"
					out["video_url"] = "https://media.example/template.mp4"
					return response(200, out), nil
				})
				out, e := a.GetTask(owned("alice"), a.seal(owned("alice"), handle{ID: "123", Model: model, Kind: kind}))
				if e != nil {
					t.Fatal(e)
				}
				want := map[string]core.ProviderStatus{"Preparing": core.ProviderQueued, "Queueing": core.ProviderQueued, "Processing": core.ProviderRunning, "Success": core.ProviderSucceeded, "Fail": core.ProviderFailed}[state]
				if out.Status != want || state == "Success" && out.ResultURL == "" {
					t.Fatal(out)
				}
				expected := 1
				if state == "Success" && kind == "legacy" {
					expected = 2
				}
				if calls != expected {
					t.Fatal(calls, expected)
				}
			})
		}
	}
}
func TestContextAndBothRegenerationProtocols(t *testing.T) {
	ctx := owned("alice")
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "Original prompt", Options: core.MediaOptions{ReferenceMode: "first_frame", ImageURLs: []string{"https://media.example/image.png"}}}
	a := fixture(t, func(h *http.Request) (*http.Response, error) {
		if h.Method == "GET" {
			return response(200, nativeResponse("123", "MiniMax-H3", "succeeded", "generation")), nil
		}
		p := body(t, h)
		switch h.URL.Path {
		case "/v2/h3_context_ir":
			if p["resolution"] != nil || p["duration"] != float64(5) || len(p["content"].([]any)) != 2 {
				t.Fatal(p)
			}
		case "/v2/video_regeneration":
			if p["resolution"] != "2K" {
				t.Fatal(p)
			}
			if p["source_task_id"] != nil {
				if p["source_task_id"] != "123" || p["content"] != nil {
					t.Fatal(p)
				}
			} else {
				content := p["content"].([]any)
				if len(content) != 3 || content[2].(map[string]any)["role"] != "base_video" {
					t.Fatal(p)
				}
			}
		default:
			t.Fatal(h.URL.Path)
		}
		return response(200, map[string]string{"task_id": "456"}), nil
	})
	token, e := a.CreateContext(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	if h, e := a.open(ctx, token); e != nil || h.Kind != "context" {
		t.Fatal(h, e)
	}
	op, _ := operationFor(r.Model)
	payload, _ := requestBody(op, r)
	source := a.seal(ctx, handle{ID: "123", Model: r.Model, Kind: "h3", InputHash: contentHash(payload["content"])})
	for _, method := range []string{"task", "video"} {
		var token string
		var e error
		if method == "task" {
			token, e = a.RegenerateTask(ctx, source, nil)
		} else {
			token, e = a.RegenerateVideo(ctx, source, r, nil)
		}
		if e != nil {
			t.Fatal(method, e)
		}
		h, e := a.open(ctx, token)
		if e != nil || h.Kind != "regeneration" {
			t.Fatal(h, e)
		}
	}
	r.Prompt = "Changed prompt"
	if _, e := a.RegenerateVideo(ctx, source, r, nil); e == nil {
		t.Fatal("changed original input accepted")
	}
	if _, e := a.RegenerateTask(owned("bob"), source, nil); e == nil {
		t.Fatal("foreign original accepted")
	}
}
func TestNativeTextResultAndCancellation(t *testing.T) {
	ctx := owned("alice")
	for _, kind := range []string{"context", "regeneration", "h3"} {
		t.Run(kind, func(t *testing.T) {
			a := fixture(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == "DELETE" {
					return response(200, map[string]string{"task_id": "123", "action": "cancelled", "status": "cancelled"}), nil
				}
				taskType := map[string]string{"context": "h3_context_ir", "regeneration": "regeneration", "h3": "generation"}[kind]
				return response(200, nativeResponse("123", "MiniMax-H3", "succeeded", taskType)), nil
			})
			token := a.seal(ctx, handle{ID: "123", Model: "MiniMax-H3", Kind: kind})
			out, e := a.QueryNativeTask(ctx, token)
			if e != nil || out.ID != token {
				t.Fatal(out, e)
			}
			if kind == "context" && out.Content.Prompt == "" {
				t.Fatal("context prompt lost")
			}
			if action, e := a.CancelOrDeleteTask(ctx, token); e != nil || action != "cancelled" {
				t.Fatal(action, e)
			}
		})
	}
}
func TestMalformedTaskResponsesAreRejected(t *testing.T) {
	for _, mutate := range []func(map[string]any){func(p map[string]any) { p["id"] = "999" }, func(p map[string]any) { p["model"] = "other" }, func(p map[string]any) { p["status"] = "mystery" }, func(p map[string]any) { p["task_type"] = "h3_context_ir" }, func(p map[string]any) { p["content"] = map[string]string{"url": "http://insecure.example/file"} }, func(p map[string]any) { p["usage"] = map[string]int{"output_seconds": -1} }} {
		a := fixture(t, func(*http.Request) (*http.Response, error) {
			out := nativeResponse("123", "MiniMax-H3", "succeeded", "generation")
			mutate(out["task"].(map[string]any))
			return response(200, out), nil
		})
		token := a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: "h3"})
		if _, e := a.GetTask(owned("alice"), token); e == nil {
			t.Fatal("malformed task accepted")
		}
	}
}
func TestTaskListNeverLeaksUnrequestedIDs(t *testing.T) {
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		ids := r.URL.Query()["filter.task_ids"]
		if len(ids) != 1 || ids[0] != "123" {
			t.Fatal(r.URL)
		}
		return response(200, map[string]any{"total": 1, "items": []any{nativeResponse("999", "MiniMax-H3", "running", "generation")["task"]}}), nil
	})
	token := a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: "h3"})
	if _, _, e := a.ListTasks(owned("alice"), []string{token}, 1, 20); e == nil {
		t.Fatal("unrequested task leaked")
	}
	if _, _, e := a.ListTasks(owned("bob"), []string{token}, 1, 20); e == nil {
		t.Fatal("cross-owner list accepted")
	}
}
func TestProtocolMalformedAndOversize(t *testing.T) {
	for _, raw := range []string{`not json`, `{"task_id":"../123"}`, `{"base_resp":{},"task_id":"123"}`} {
		a := fixture(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}, nil
		})
		if _, e := a.Generate(owned("alice"), core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"}); e == nil {
			t.Fatal("accepted", raw)
		}
	}
	if _, e := normalizeH3Usage(json.RawMessage(`{"output_seconds":"five"}`), "MiniMax-H3"); e == nil {
		t.Fatal("malformed usage")
	}
}

func TestUnknownUsageNeverMetersAsZero(t *testing.T) {
	u, e := normalizeH3Usage(json.RawMessage(`{"output_seconds":5,"input_seconds":null,"input_image_count":0}`), "MiniMax-H3")
	if e != nil {
		t.Fatal(e)
	}
	q := core.TaskCost{Pricing: RegenerationTariff()}
	if q.MeterUsage(*u) {
		t.Fatal("null input seconds became zero cost")
	}
	for _, raw := range []string{`{"input_image_count":1.5}`, `{"total_seconds":4,"input_seconds":2,"output_seconds":5}`, `{"total_tokens":20,"prompt_tokens":10,"completion_tokens":15}`, `{"output_seconds":"5"}`} {
		if _, e := normalizeH3Usage(json.RawMessage(raw), "MiniMax-H3"); e == nil {
			t.Fatal("invalid usage", raw)
		}
	}
}
func TestListedTaskContractMatchesOwnedHandle(t *testing.T) {
	for _, change := range []func(map[string]any){func(m map[string]any) { m["model"] = "other" }, func(m map[string]any) { m["task_type"] = "regeneration" }, func(m map[string]any) { m["status"] = "unknown" }, func(m map[string]any) { m["modality"] = "text" }} {
		a := fixture(t, func(*http.Request) (*http.Response, error) {
			task := nativeResponse("123", "MiniMax-H3", "succeeded", "generation")["task"].(map[string]any)
			change(task)
			return response(200, map[string]any{"total": 1, "items": []any{task}}), nil
		})
		token := a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: "h3"})
		if _, _, e := a.ListTasks(owned("alice"), []string{token}, 1, 20); e == nil {
			t.Fatal("malformed listed task accepted")
		}
	}
}

func TestAllDirectPaidHelpersRejectMissingHostScope(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unscoped helper submitted a paid task")
		return nil, nil
	})
	ctx := context.Background()
	if _, e := a.CreateContext(ctx, core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"}); e == nil {
		t.Fatal("unscoped Context-IR accepted")
	}
	if _, e := a.GenerateImages(ctx, core.MediaRequest{Model: "image-01", Prompt: "scene"}, 1, "url"); e == nil {
		t.Fatal("unscoped image helper accepted")
	}
}

func TestContextOperationThroughCommonProviderContract(t *testing.T) {
	ctx := owned("alice")
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			if r.URL.Path != "/v2/h3_context_ir" {
				t.Fatal(r.URL)
			}
			p := body(t, r)
			if p["model"] != "MiniMax-H3" || p["resolution"] != nil {
				t.Fatal(p)
			}
			return response(200, map[string]string{"task_id": "123"}), nil
		}
		p := nativeResponse("123", "MiniMax-H3", "succeeded", "h3_context_ir")
		p["task"].(map[string]any)["usage"] = map[string]int{"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150}
		return response(200, p), nil
	})
	m, ok := modelFor(ContextIR)
	if !ok || m.MediaType != "text" || m.SupportsAudio || m.Pricing.Known {
		t.Fatal(m)
	}
	o, e := core.NormalizeModelOptions(m, core.MediaOptions{ReferenceMode: "references", ImageURLs: []string{"https://media.example/frame.png"}})
	if e != nil {
		t.Fatal(e)
	}
	task, e := a.Generate(ctx, core.MediaRequest{Model: ContextIR, Prompt: "Prepare this scene", Options: o})
	if e != nil {
		t.Fatal(e)
	}
	result, e := a.GetTask(ctx, task.ID)
	if e != nil || result.Status != core.ProviderSucceeded || len(result.Artifacts) != 1 || result.Artifacts[0].Text == "" || result.Artifacts[0].URL != "" || result.Artifacts[0].MediaType != "text" {
		t.Fatal(result, e)
	}
	cost := core.TaskCost{Pricing: ContextIRTariff()}
	if !cost.MeterUsage(*result.Usage) || *cost.MeteredUSD != 270 {
		t.Fatal("token charges incorrect", cost)
	}
}

func TestSourceTaskRegenerationThroughCommonProviderContract(t *testing.T) {
	ctx := owned("alice")
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			if r.URL.Path != "/v2/video_regeneration" {
				t.Fatal(r.URL)
			}
			p := body(t, r)
			if p["model"] != "MiniMax-H3" || p["source_task_id"] != "123" || p["resolution"] != "2K" {
				t.Fatal(p)
			}
			return response(200, map[string]string{"task_id": "456"}), nil
		}
		id, kind := "123", "generation"
		if strings.HasSuffix(r.URL.Path, "456") {
			id, kind = "456", "regeneration"
		}
		p := nativeResponse(id, "MiniMax-H3", "succeeded", kind)
		p["task"].(map[string]any)["usage"] = map[string]int{"output_seconds": 5, "input_seconds": 4, "input_image_count": 6, "total_seconds": 9}
		return response(200, p), nil
	})
	source := a.seal(ctx, handle{ID: "123", Kind: "h3", Model: "MiniMax-H3"})
	m, _ := modelFor(Regeneration)
	h, _ := a.open(ctx, source)
	h.Kind = "source_binding"
	binding := uuid.NewString()
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	o, e := core.NormalizeModelOptions(m, core.MediaOptions{ResourceReferences: []core.ResourceReference{{BindingID: binding, ReferenceID: "source_task"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "source_task", Kind: "file_resource", Subtype: "regeneration_source", RemoteID: a.seal(ctx, h), ConnectionIdentity: scope.Revision}}})
	if e != nil {
		t.Fatal(e)
	}
	r := core.MediaRequest{Model: m.ID, Options: o}
	if e = a.ValidateRequest(ctx, r); e != nil {
		t.Fatal(e)
	}
	quote, _, e := a.Quote(ctx, m, r)
	if e != nil || quote.ExpectedUSD != 475000 {
		t.Fatal(quote, e)
	}
	task, e := a.Generate(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	out, e := a.GetTask(ctx, task.ID)
	if e != nil || out.Status != core.ProviderSucceeded || out.ResultURL == "" {
		t.Fatal(out, e)
	}
	if !quote.MeterUsage(*out.Usage) || *quote.MeteredUSD != 475000 {
		t.Fatal(quote)
	}
	if e = a.ValidateRequest(owned("bob"), r); e == nil {
		t.Fatal("foreign regeneration source accepted")
	}
}

func TestEveryNativeOperationState(t *testing.T) {
	for _, tc := range []struct{ kind, taskType string }{{"h3", "generation"}, {"context", "h3_context_ir"}, {"regeneration", "regeneration"}} {
		for _, state := range []string{"queued", "running", "succeeded", "failed", "cancelled"} {
			t.Run(tc.kind+"/"+state, func(t *testing.T) {
				a := fixture(t, func(*http.Request) (*http.Response, error) {
					return response(200, nativeResponse("123", "MiniMax-H3", state, tc.taskType)), nil
				})
				token := a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: tc.kind})
				out, e := a.GetTask(owned("alice"), token)
				if e != nil {
					t.Fatal(e)
				}
				want := map[string]core.ProviderStatus{"queued": core.ProviderQueued, "running": core.ProviderRunning, "succeeded": core.ProviderSucceeded, "failed": core.ProviderFailed, "cancelled": core.ProviderFailed}[state]
				if out.Status != want {
					t.Fatal(out)
				}
				if state == "succeeded" && tc.kind == "context" && (len(out.Artifacts) != 1 || out.Artifacts[0].Text == "") {
					t.Fatal(out)
				}
			})
		}
	}
}
func TestOutputConstraintsCannotMislabelSucceededTasks(t *testing.T) {
	for _, tc := range []struct {
		kind, res string
		duration  int
	}{{"h3", "480P", 5}, {"h3", "768P", 16}, {"regeneration", "768P", 5}, {"regeneration", "2K", 3}} {
		a := fixture(t, func(*http.Request) (*http.Response, error) {
			taskType := "generation"
			if tc.kind == "regeneration" {
				taskType = "regeneration"
			}
			p := nativeResponse("123", "MiniMax-H3", "succeeded", taskType)
			p["task"].(map[string]any)["resolution"] = tc.res
			p["task"].(map[string]any)["duration"] = tc.duration
			return response(200, p), nil
		})
		if _, e := a.GetTask(owned("alice"), a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: tc.kind})); e == nil {
			t.Fatal("invalid final output metadata accepted", tc)
		}
	}
}

func TestContextAliasUsesSameOwnedInputValidation(t *testing.T) {
	ctx := owned("alice")
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/h3_context_ir" {
			t.Fatal(r.URL)
		}
		p := body(t, r)
		content := p["content"].([]any)
		if content[1].(map[string]any)["image_url"].(map[string]any)["url"] != "mm_file://123" {
			t.Fatal(content)
		}
		return response(200, map[string]string{"task_id": "456"}), nil
	})
	file := a.fileView(ctx, File{ID: json.Number("123"), Purpose: "video_generation_input", MediaKind: "image"})
	r := core.MediaRequest{Model: ContextIR, Prompt: "Prepare a scene", Options: core.MediaOptions{ReferenceMode: "first_frame", ImageURLs: []string{file.URI}}}
	if _, e := a.Generate(ctx, r); e != nil {
		t.Fatal(e)
	}
	for _, o := range []core.MediaOptions{{Ratio: "adaptive"}, {ReferenceMode: "references", VideoURLs: []string{"https://media.example/video.mp4"}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{"https://media.example/video.mp4": {Type: "video", Duration: 16}}}, {ReferenceMode: "references", AudioURLs: []string{"https://media.example/audio.mp3"}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{"https://media.example/audio.mp3": {Type: "audio", Duration: 1}}}} {
		if e := a.ValidateRequest(ctx, core.MediaRequest{Model: ContextIR, Prompt: "scene", Options: o}); e == nil {
			t.Fatal("context alias bypassed H3 input protocol", o)
		}
	}
}
func TestContextQuoteRetainsKnownTariffWhileTotalIsUnknown(t *testing.T) {
	a := fixture(t, nil)
	quote, _, e := a.Quote(owned("alice"), core.MediaModel{}, core.MediaRequest{Model: ContextIR, Prompt: "scene"})
	if e != nil || !quote.QuantityUnknown || quote.Status != "quantity_unknown" || len(quote.Pricing.Components) != 2 {
		t.Fatal(quote, e)
	}
	raw, err := json.Marshal(quote)
	if err != nil || strings.Contains(string(raw), `"expected_usd"`) || strings.Contains(string(raw), `"estimated_units"`) {
		t.Fatal("unknown quantity became a numeric quote", err)
	}
	usage, _ := normalizeH3Usage(json.RawMessage(`{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}`), "MiniMax-H3")
	if !quote.MeterUsage(*usage) || *quote.MeteredUSD != 270 {
		t.Fatal(quote)
	}
}

func TestOwnedNativeTaskListAllFilters(t *testing.T) {
	for _, state := range []string{"queued", "running", "succeeded", "failed", "cancelled"} {
		for _, kind := range []string{"generation", "h3_context_ir", "regeneration"} {
			t.Run(state+"/"+kind, func(t *testing.T) {
				a := fixture(t, func(r *http.Request) (*http.Response, error) {
					q := r.URL.Query()
					if r.Method != "GET" || q.Get("filter.status") != state || q.Get("filter.model") != "MiniMax-H3" || q.Get("filter.task_type") != kind || q.Get("page_num") != "1" || q.Get("page_size") != "20" || len(q["filter.task_ids"]) != 1 || q.Get("filter.task_ids") != "123" {
						t.Fatal("documented owner-scoped filters were not preserved")
					}
					return response(200, map[string]any{"total": 1, "items": []any{nativeResponse("123", "MiniMax-H3", state, kind)["task"]}}), nil
				})
				family := map[string]string{"generation": "h3", "h3_context_ir": "context", "regeneration": "regeneration"}[kind]
				token := a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: family})
				items, total, err := a.ListTasksFiltered(owned("alice"), []string{token}, 1, 20, NativeTaskFilter{Status: state, Model: "MiniMax-H3", TaskType: kind})
				if err != nil || total != 1 || len(items) != 1 || items[0].ID != token {
					t.Fatal("filtered list failed or exposed provider ID", err)
				}
			})
		}
	}
	for _, filter := range []NativeTaskFilter{{Status: "unknown"}, {Model: "other"}, {TaskType: "other"}} {
		a := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid filter reached HTTP"); return nil, nil })
		token := a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: "h3"})
		if _, _, err := a.ListTasksFiltered(owned("alice"), []string{token}, 1, 20, filter); err == nil {
			t.Fatal("invalid filter")
		}
		if _, _, err := a.ListTasks(owned("alice"), []string{token, token}, 1, 20); err == nil {
			t.Fatal("duplicate owned handle")
		}
	}
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		return response(200, map[string]any{"total": 1, "items": []any{nativeResponse("123", "MiniMax-H3", "running", "generation")["task"]}}), nil
	})
	token := a.seal(owned("alice"), handle{ID: "123", Model: "MiniMax-H3", Kind: "h3"})
	if _, _, err := a.ListTasksFiltered(owned("alice"), []string{token}, 1, 20, NativeTaskFilter{Status: "queued"}); err == nil {
		t.Fatal("provider ignored filter")
	}
}
