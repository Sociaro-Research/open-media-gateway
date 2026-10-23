package api

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

func TestEquivalentVideoSecondsPreserveAdmissionAndIdempotency(t *testing.T) {
	contract, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct{ Pattern string }
			}
		}
	}
	if err = json.Unmarshal(contract, &spec); err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(spec.Components.Schemas["GenerationRequest"].Properties["seconds"].Pattern)
	for _, path := range []string{"/v1/videos", "/v1/generations", "/v1/generations/estimate"} {
		t.Run(path, func(t *testing.T) {
			repo := &admissionRepository{}
			svc := core.NewTaskServiceWithProviders(repo, &admissionQueue{}, map[string]core.MediaProvider{"mock": mock.New(time.Millisecond, "")}, map[string]string{"mock": "mock-video-v1"}, "mock")
			svc.EnableAccounting()
			h := New(svc, healthStub{}, "secret")
			for _, seconds := range []string{"5", "+5", "05", "+05", "0005"} {
				if !pattern.MatchString(seconds) {
					t.Errorf("schema rejects supported equivalent seconds %q", seconds)
				}
				w := openAIRequest(h, path, `{"model":"mock/mock-video-v1","prompt":"test","seconds":"`+seconds+`"}`)
				want := 202
				if path == "/v1/generations/estimate" {
					want = 200
				}
				if w.Code != want {
					t.Fatalf("%q: %d %s", seconds, w.Code, w.Body)
				}
				if want == 202 && (repo.creates != 1 || repo.task.Options.Duration != 5) {
					t.Fatal("equivalent duration changed admission identity", repo.creates, repo.task)
				}
			}
			for _, seconds := range []string{"0", "+0", "000", "-5", "5.0", "5e0"} {
				if pattern.MatchString(seconds) {
					t.Errorf("schema accepts invalid duration %q", seconds)
				}
				w := openAIRequest(h, path, `{"model":"mock/mock-video-v1","prompt":"test","seconds":"`+seconds+`"}`)
				if w.Code != 400 {
					t.Fatalf("invalid duration %q: %d %s", seconds, w.Code, w.Body)
				}
			}
		})
	}
}
