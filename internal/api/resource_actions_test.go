package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func sessionAPIBinding(f *resourceAPIFixture, actor string) providerresources.Binding {
	b := f.binding(actor, "editing_session")
	b.Subtype = "selection"
	duration := 4.0
	b.Sources = []core.ResourceSourceEvidence{{ReferenceID: b.ID, MediaType: "video", SHA256: strings.Repeat("a", 64), Duration: &duration}}
	b.Session = &core.MediaSessionEvidence{ExpiresAt: time.Now().UTC().Add(time.Hour), SourceVideoDuration: 4, SourceSHA256: strings.Repeat("a", 64), FrameCount: 120, FPS: 30}
	f.resources.bindings[b.ID] = b
	return b
}
func TestResourceSessionActionAuthenticatedAdmissionEstimateAndReplay(t *testing.T) {
	f := newResourceAPIFixtureWithPrices(t, map[string]string{"selection_add_price": "0.01"})
	b := sessionAPIBinding(f, "alice")
	path := "/v1/provider-resources/" + b.ID + "/actions/add"
	point := map[string]any{"x": .123456789012345, "y": .5}
	body := map[string]any{"workflow_model": kling.MultiElementsEditing, "expected_revision": 0, "frame": map[string]any{"frame_index": 0, "points": []any{point}}}
	estimate := resourceAPIRequest(f.h, "POST", path+"/estimate", f.alice, "", body)
	resourceAPIAssertStatus(t, estimate, 200)
	if f.tasks.creates != 0 || len(f.queue.ids) != 0 {
		t.Fatal("estimate admitted remote work")
	}
	w := resourceAPIRequest(f.h, "POST", path, f.alice, "add-once", body)
	resourceAPIAssertStatus(t, w, 202)
	if f.tasks.creates != 1 || len(f.queue.ids) != 1 {
		t.Fatal("action not admitted exactly once")
	}
	accepted := f.tasks.tasks[f.queue.ids[0]]
	if accepted.Model != "kling-selection-add" || accepted.Options.SessionAction == nil || accepted.Options.SessionAction.Frame.Points[0].X != .123456789012345 || len(accepted.Options.ResourceSnapshots) != 1 || accepted.Options.ResourceSnapshots[0].RemoteID != b.RemoteID || accepted.Cost == nil || !accepted.Cost.Pricing.AccountSpecific {
		t.Fatal("typed intent, scope or tariff changed", accepted)
	}
	resourceAPIAssertPrivate(t, w.Body.String(), b.RemoteID, resourceAPIRevision, b.Session.SourceSHA256, "_reference_resources", "SourceReference")
	var first map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &first)
	// A replay can outlive both the provider session and connection availability.
	b.Status = providerresources.Deleted
	f.resources.bindings[b.ID] = b
	again := resourceAPIRequest(f.h, "POST", path, f.alice, "add-once", body)
	resourceAPIAssertStatus(t, again, 202)
	var replay map[string]any
	_ = json.Unmarshal(again.Body.Bytes(), &replay)
	if replay["id"] != first["id"] || f.tasks.creates != 1 {
		t.Fatal("accepted replay revalidated deleted session")
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "new-after-delete", body), 404)
	body["expected_revision"] = 1
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "add-once", body), 409)
}
func TestResourceSessionActionRejectsSpoofingForeignScopesAndUnknownPrice(t *testing.T) {
	f := newResourceAPIFixture(t)
	b := sessionAPIBinding(f, "alice")
	path := "/v1/provider-resources/" + b.ID + "/actions/clear"
	body := map[string]any{"workflow_model": kling.MultiElementsEditing, "expected_revision": 0}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "unknown-price", body), 422)
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.bob, "foreign", body), 404)
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, "", "missing-auth", body), 401)
	for _, key := range []string{"remote_id", "session_id", "source_sha256", "duration", "_reference_resources", "source_reference"} {
		forged := map[string]any{"workflow_model": kling.MultiElementsEditing, "expected_revision": 0, key: "client-forged"}
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, key, forged), 400)
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "missing-revision", map[string]any{}), 400)
	b.Revision = "another-connection-revision"
	f.resources.bindings[b.ID] = b
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "stale-connection", body), 404)
	if f.tasks.creates != 0 || len(f.queue.ids) != 0 {
		t.Fatal("rejected request admitted work")
	}
}
func TestResourceSessionActionCapsBudgetRecoveryAndExactDeletion(t *testing.T) {
	f := newResourceAPIFixtureWithPrices(t, map[string]string{"selection_clear_price": "0.1", "selection_delete_price": "0.1"})
	b := sessionAPIBinding(f, "alice")
	path := "/v1/provider-resources/" + b.ID + "/actions/clear"
	body := map[string]any{"workflow_model": kling.MultiElementsEditing, "expected_revision": 0, "max_expected_usd": .01}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "cap", body), 409)
	delete(body, "max_expected_usd")
	f.tasks.budget = true
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "budget", body), 402)
	f.tasks.budget = false
	b.Status = providerresources.Failed
	f.resources.bindings[b.ID] = b
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "no-recovery", body), 404)
	body["recover"] = true
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "explicit-recovery", body), 202)
	b.Status = providerresources.Succeeded
	b.Session.SelectionRevision = 1
	b.Session.HasSelection = true
	b.Session.Selections = []core.FrameSelection{{FrameIndex: 0, Points: []core.SelectionPoint{{X: .123456789012345, Y: .5}}}}
	f.resources.bindings[b.ID] = b
	bad := map[string]any{"workflow_model": kling.MultiElementsEditing, "expected_revision": 1, "frame": map[string]any{"frame_index": 0, "points": []any{map[string]any{"x": .123457, "y": .5}}}}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", strings.TrimSuffix(path, "clear")+"delete", f.alice, "rounded-deletion", bad), 400)
	if f.tasks.creates != 1 {
		t.Fatal("rejected request persisted", f.tasks.creates)
	}
}
func TestResourceActionPathsUseGenerationPolicy(t *testing.T) {
	for _, p := range []string{"/v1/provider-resources/local/actions/add", "/v1/provider-resources/local/actions/clear/estimate"} {
		if !isGenerationPath(p) {
			t.Fatal("missing model/rate/budget/company policy", p)
		}
	}
	for _, p := range []string{"/v1/provider-resources/local", "/v1/provider-resources/local/actions/add/extra", "/v1/provider-resource-jobs/local"} {
		if isResourceActionPath(p) {
			t.Fatal("unrelated path accepted", p)
		}
	}
}
