package catalogsync

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
)

// Optional acceptance against retained normalized evidence, never a live fetch.
func TestBytePlusGroupedHeadersSavedReportReplay(t *testing.T) {
	dir := os.Getenv("GATEWAY_BYTEPLUS_HEADER_REVIEW_DIR")
	if dir == "" {
		t.Skip("no retained BytePlus report evidence")
	}
	read := func(name string, target any) {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(body, target); err != nil {
			t.Fatal(err)
		}
	}
	var envelope struct {
		Runs []struct {
			ID      string `json:"id"`
			Payload Run    `json:"payload"`
		} `json:"runs"`
	}
	read("saved-evidence.json", &envelope)
	var saved Run
	for _, r := range envelope.Runs {
		if r.ID == "a9982346-1209-425d-8f42-eafeec3d24d2" {
			saved = r.Payload
		}
	}
	if saved.ID == "" || len(saved.OpenWarnings) != 102 {
		t.Fatal("target report missing or changed")
	}
	var previous State
	read("bytedance-state.json", &previous)
	observations := append([]Observation(nil), saved.Sources...)
	repaired := 0
	for i, ob := range observations {
		if ob.Source.ID != "models" {
			continue
		}
		if ob.Hash != "9e4493d9a2399310a5bce24b65b657b907e549b2432823cb24b94cf0b0c8a2fe" {
			t.Fatal("unexpected model source hash")
		}
		body, err := os.ReadFile(filepath.Join(dir, ob.Hash+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprintf("%x", sha256.Sum256(body)) != ob.Hash {
			t.Fatal("saved source bytes changed")
		}
		parsed, text, err := Parse(ob.Source, body)
		if err != nil {
			t.Fatal(err)
		}
		var expected []Entry
		if err = json.Unmarshal(encoded(ob.Entries), &expected); err != nil {
			t.Fatal(err)
		}
		for j := range expected {
			facts := expected[j].Metadata.Facts
			if len(facts) == 3 && facts[0].Label == "Provider: Default" && facts[1].Label == "Provider: " && facts[2].Label == "Provider: Flex" {
				facts[0].Label = "Provider: Default / Max RPM"
				facts[1].Label = "Provider: Default / Max concurrency"
				repaired++
			}
		}
		if repaired != 6 || !reflect.DeepEqual(parsed, expected) {
			t.Fatal("changed source entries beyond the six grouped headers", repaired)
		}
		observations[i].Entries = parsed
		observations[i].Text = text
	}
	if repaired != 6 {
		t.Fatal("no grouped tables repaired")
	}
	capture := Capture{Inactive: modelcatalog.Inactive("bytedance", saved.CompletedAt)}
	for _, entry := range previous.Models {
		if entry.Contract != nil && !entry.Inactive {
			model := *entry.Contract
			model = modelcatalog.Enrich("bytedance", model)
			capture.Models = append(capture.Models, model)
		}
	}
	for _, ob := range observations {
		if ob.Source.ID == "_api" {
			read(ob.Hash+".txt", &capture.IDs)
		}
	}
	// Existing saved tariffs are unchanged inputs; acceptance does not make fresh
	// provider calls or test their external availability.
	next, run := Reconcile(previous, capture, observations, saved.CompletedAt, accepted)
	if len(run.Changes) != 0 || next.Version != previous.Version || len(run.OpenWarnings) != 102 || len(run.NewWarnings) != 12 || len(run.ResolvedWarnings) != 12 {
		t.Fatalf("unexpected reconciliation changes/warnings: changes=%d open=%d new=%d resolved=%d", len(run.Changes), len(run.OpenWarnings), len(run.NewWarnings), len(run.ResolvedWarnings))
	}
	for id, before := range previous.Models {
		after := next.Models[id]
		if !reflect.DeepEqual(before.Rates, after.Rates) || !reflect.DeepEqual(before.Contract, after.Contract) || before.Inactive != after.Inactive {
			t.Fatal("header review changed a price or contract", id)
		}
	}
	byKey := map[string]Warning{}
	for _, w := range run.OpenWarnings {
		var fact core.ModelFact
		if err := json.Unmarshal(w.Evidence, &fact); err != nil {
			t.Fatal(err)
		}
		if fact.Label == "Provider: " {
			t.Fatal("empty label remains")
		}
		byKey[w.Key] = w
	}
	preserved := 0
	workflow := false
	for _, old := range saved.OpenWarnings {
		var oldFact core.ModelFact
		if err := json.Unmarshal(old.Evidence, &oldFact); err != nil {
			t.Fatal(err)
		}
		if oldFact.Label == "Provider: " || oldFact.Label == "Provider: Default" {
			continue
		}
		current, ok := byKey[old.Key]
		if !ok {
			t.Fatal("unrelated capability warning disappeared", old.Key)
		}
		var fact core.ModelFact
		if err := json.Unmarshal(current.Evidence, &fact); err != nil {
			t.Fatal(err)
		}
		if fact != oldFact {
			t.Fatal("unrelated capability evidence changed", old.Key)
		}
		preserved++
		workflow = workflow || current.Model == "seedance-1-5-pro-251215" && fact.Label == "Provider: reference workflows" && fact.Value == "R2V FLF I2V I2V T2V"
	}
	if preserved != 90 || !workflow {
		t.Fatal("semantic review warnings were suppressed", preserved, workflow)
	}
	_, repeat := Reconcile(next, capture, observations, saved.CompletedAt.Add(time.Hour), accepted)
	if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || len(repeat.ResolvedWarnings) != 0 {
		t.Fatal("repeat is not quiet")
	}
	t.Log("exact saved run: six empty labels replaced by scoped headers; all 102 review warnings retained (12 relabeled, 90 unchanged); prices/contracts unchanged; R2V still requires review; repeat quiet")
}
