package catalogsync

import (
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"testing"
	"time"
)

func TestDurationSelectorKeysAreDistinctAndAbsentIsCompatible(t *testing.T) {
	r := Rate{Region: "Global", Resolution: "768p", Unit: "request", Per: 1, Price: 280000}
	old := rateKey(r)
	if old != "Global/768p//request/1" {
		t.Fatal("legacy key changed", old)
	}
	six := r
	six.Duration = 6
	ten := r
	ten.Duration = 10
	if rateKey(six) == old || rateKey(ten) == old || rateKey(six) == rateKey(ten) {
		t.Fatal("duration selectors collide")
	}
	b, e := json.Marshal(r)
	if e != nil || string(b) == "" {
		t.Fatal(e)
	}
	var obj map[string]any
	_ = json.Unmarshal(b, &obj)
	if _, ok := obj["duration"]; ok {
		t.Fatal("absent selector not omitted")
	}
}
func TestDurationReconciliationPreservesBothRowsAndFailedRefresh(t *testing.T) {
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	model := core.MediaModel{ID: "duration-fixture", MediaType: "video", MinDuration: 6, MaxDuration: 10, DefaultDuration: 6, DurationStep: 4}
	capture := Capture{Models: []core.MediaModel{model}}
	source := Source{ID: "prices", URL: "https://example.test/prices", Parser: "document"}
	rates := []Rate{{Region: "Global", Resolution: "768p", Duration: 6, Unit: "request", Per: 1, Price: 280000}, {Region: "Global", Resolution: "768p", Duration: 10, Unit: "request", Per: 1, Price: 560000}}
	observation := Observation{Source: source, Hash: "one", Entries: []Entry{{ID: model.ID, Rates: rates}}}
	next, run := Reconcile(State{}, capture, []Observation{observation}, now, func(_ core.MediaModel, r Rate) error {
		if r.Duration != 6 && r.Duration != 10 {
			return errors.New("unsupported duration")
		}
		return nil
	})
	if len(next.Models[model.ID].Rates) != 2 {
		t.Fatal(next.Models, run)
	}
	failed := Observation{Source: source, Hash: "two", Error: "source unavailable"}
	preserved, _ := Reconcile(next, capture, []Observation{failed}, now.Add(time.Hour), func(core.MediaModel, Rate) error { return nil })
	if len(preserved.Models[model.ID].Rates) != 2 || preserved.Models[model.ID].Rates[0].Price != next.Models[model.ID].Rates[0].Price {
		t.Fatal("last-confirmed duration prices lost", preserved.Models)
	}
}
