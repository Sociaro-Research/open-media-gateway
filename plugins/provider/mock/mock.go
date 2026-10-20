package mock

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

type Adapter struct {
	delay         time.Duration
	resultBaseURL string
	now           func() time.Time
}

const embeddedVideoDataURL = "data:video/mp4;base64,AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDEAAAPEbW9vdgAAAGxtdmhkAAAAAAAAAAAAAAAAAAAD6AAAA+gAAQAAAQAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAu50cmFrAAAAXHRraGQAAAADAAAAAAAAAAAAAAABAAAAAAAAA+gAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAUAAAAC0AAAAAAAkZWR0cwAAABxlbHN0AAAAAAAAAAEAAAPoAAAIAAABAAAAAAJmbWRpYQAAACBtZGhkAAAAAAAAAAAAAAAAAAAwAAAAMABVxAAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAAAAAAAAAABWaWRlb0hhbmRsZXIAAAACEW1pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRkaW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAAdFzdGJsAAAAwXN0c2QAAAAAAAAAAQAAALFhdmMxAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAAUAAtABIAAAASAAAAAAAAAABFUxhdmM2Mi4yOC4xMDAgbGlieDI2NAAAAAAAAAAAAAAAGP//AAAAN2F2Y0MBZAAM/+EAGmdkAAys2UFBn58BEAAAAwAQAAADAYDxQplgAQAGaOvjyyLA/fj4AAAAABBwYXNwAAAAAQAAAAEAAAAUYnRydAAAAAAAAKgoAAAAAAAAABhzdHRzAAAAAAAAAAEAAAAMAAAEAAAAABRzdHNzAAAAAAAAAAEAAAABAAAAaGN0dHMAAAAAAAAACwAAAAEAAAgAAAAAAQAAFAAAAAABAAAIAAAAAAEAAAAAAAAAAQAABAAAAAABAAAUAAAAAAEAAAgAAAAAAQAAAAAAAAABAAAEAAAAAAEAABAAAAAAAgAABAAAAAAcc3RzYwAAAAAAAAABAAAAAQAAAAwAAAABAAAARHN0c3oAAAAAAAAAAAAAAAwAAA14AAACXAAAAEsAAAAkAAAAJgAAAjcAAABOAAAAJwAAACMAAAFRAAAASwAAADEAAAAUc3RjbwAAAAAAAAABAAAD9AAAAGJ1ZHRhAAAAWm1ldGEAAAAAAAAAIWhkbHIAAAAAAAAAAG1kaXJhcHBsAAAAAAAAAAAAAAAALWlsc3QAAAAlqXRvbwAAAB1kYXRhAAAAAQAAAABMYXZmNjIuMTIuMTAwAAAACGZyZWUAABUNbWRhdAAAAq4GBf//qtxF6b3m2Ui3lizYINkj7u94MjY0IC0gY29yZSAxNjUgcjMyMjIgYjM1NjA1YSAtIEguMjY0L01QRUctNCBBVkMgY29kZWMgLSBDb3B5bGVmdCAyMDAzLTIwMjUgLSBodHRwOi8vd3d3LnZpZGVvbGFuLm9yZy94MjY0Lmh0bWwgLSBvcHRpb25zOiBjYWJhYz0xIHJlZj0zIGRlYmxvY2s9MTowOjAgYW5hbHlzZT0weDM6MHgxMTMgbWU9aGV4IHN1Ym1lPTcgcHN5PTEgcHN5X3JkPTEuMDA6MC4wMCBtaXhlZF9yZWY9MSBtZV9yYW5nZT0xNiBjaHJvbWFfbWU9MSB0cmVsbGlzPTEgOHg4ZGN0PTEgY3FtPTAgZGVhZHpvbmU9MjEsMTEgZmFzdF9wc2tpcD0xIGNocm9tYV9xcF9vZmZzZXQ9LTIgdGhyZWFkcz02IGxvb2thaGVhZF90aHJlYWRzPTEgc2xpY2VkX3RocmVhZHM9MCBucj0wIGRlY2ltYXRlPTEgaW50ZXJsYWNlZD0wIGJsdXJheV9jb21wYXQ9MCBjb25zdHJhaW5lZF9pbnRyYT0wIGJmcmFtZXM9MyBiX3B5cmFtaWQ9MiBiX2FkYXB0PTEgYl9iaWFzPTAgZGlyZWN0PTEgd2VpZ2h0Yj0xIG9wZW5fZ29wPTAgd2VpZ2h0cD0yIGtleWludD0yNTAga2V5aW50X21pbj0xMiBzY2VuZWN1dD00MCBpbnRyYV9yZWZyZXNoPTAgcmNfbG9va2FoZWFkPTQwIHJjPWNyZiBtYnRyZWU9MSBjcmY9MjMuMCBxY29tcD0wLjYwIHFwbWluPTAgcXBtYXg9NjkgcXBzdGVwPTQgaXBfcmF0aW89MS40MCBhcT0xOjEuMDAAgAAACsJliIQAN//+9vD+BTZWBFCUwSASZgmTZk719WCNOdlDFe/gfNCouvlFK8/yAVitahbshaOjecoGeyjhMBBC1UcBoeSp4GpcomTUr1+hEFDYJRkuRDBPZxiGo6f310kI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"

const embeddedImageDataURL = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSI2NDAiIGhlaWdodD0iMzYwIj48cmVjdCB3aWR0aD0iNjQwIiBoZWlnaHQ9IjM2MCIgZmlsbD0iIzEwMTQxYyIvPjx0ZXh0IHg9IjMyMCIgeT0iMTgwIiBmaWxsPSIjYjdmMzRiIiBmb250LXNpemU9IjQ4IiB0ZXh0LWFuY2hvcj0ibWlkZGxlIj5Nb2NrIEltYWdlPC90ZXh0Pjwvc3ZnPg=="
const embeddedAudioDataURL = "data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA="

func New(delay time.Duration, resultBaseURL string) *Adapter {
	return &Adapter{delay: delay, resultBaseURL: strings.TrimRight(resultBaseURL, "/"), now: time.Now}
}

func (a *Adapter) Name() string { return "mock" }

func (a *Adapter) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{{
		ID: "mock-video-v1", DisplayName: "Mock Video", MediaType: "video",
		Resolutions: []string{"480p", "720p", "1080p"},
		Ratios:      []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"},
		MinDuration: 2, MaxDuration: 30, DefaultDuration: 5,
		SupportsAudio: true, SupportsCameraFixed: true, SupportsImageInput: true,
		Pricing: core.MediaModelPricing{Known: true, ReferenceDescription: "Free test"},
	}, {
		ID: "mock-image-v1", DisplayName: "Mock Image", MediaType: "image",
		SupportsImageInput: true,
		Resolutions:        []string{"1024*1024"}, Pricing: core.MediaModelPricing{Known: true, ReferenceDescription: "Free test"},
	}, {
		ID: "mock-speech-v1", DisplayName: "Mock Speech", MediaType: "speech", SupportsVoice: true,
		Voices: []string{"Mock"}, Formats: []string{"wav"}, Pricing: core.MediaModelPricing{Known: true, ReferenceDescription: "Free test"},
	}, {
		ID: "mock-music-v1", DisplayName: "Mock Music", MediaType: "music", SupportsLyrics: true,
		Formats: []string{"wav"}, Pricing: core.MediaModelPricing{Known: true, ReferenceDescription: "Free test"},
	}}, nil
}

func (a *Adapter) Generate(_ context.Context, request core.MediaRequest) (core.ProviderTask, error) {
	mediaType := "video"
	if strings.Contains(request.Model, "image") {
		mediaType = "image"
	} else if strings.Contains(request.Model, "speech") {
		mediaType = "speech"
	} else if strings.Contains(request.Model, "music") {
		mediaType = "music"
	}
	return core.ProviderTask{ID: fmt.Sprintf("mock-%d-%s-%s", a.now().UnixMilli(), mediaType, request.TaskID)}, nil
}

func (a *Adapter) GetTask(_ context.Context, providerTaskID string) (core.ProviderResult, error) {
	parts := strings.SplitN(providerTaskID, "-", 4)
	if len(parts) < 3 || parts[0] != "mock" {
		return core.ProviderResult{}, fmt.Errorf("invalid mock task id")
	}
	createdMillis, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return core.ProviderResult{}, fmt.Errorf("invalid mock task timestamp: %w", err)
	}
	if a.now().Before(time.UnixMilli(createdMillis).Add(a.delay)) {
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	}
	mediaType := "video"
	taskID := parts[2]
	if len(parts) == 4 {
		mediaType = parts[2]
		taskID = parts[3]
	}
	resultURL := embeddedVideoDataURL
	if mediaType == "image" {
		resultURL = embeddedImageDataURL
	} else if mediaType == "speech" || mediaType == "music" {
		resultURL = embeddedAudioDataURL
	}
	if a.resultBaseURL != "" && a.resultBaseURL != "embedded" {
		extension := ".mp4"
		if mediaType == "image" {
			extension = ".png"
		} else if mediaType == "speech" || mediaType == "music" || mediaType == "audio" {
			extension = ".wav"
		}
		resultURL = a.resultBaseURL + "/" + url.PathEscape(taskID) + extension
	}
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: resultURL}, nil
}
