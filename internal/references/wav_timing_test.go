package references

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func referenceTimingWAV(seconds int, byteRate uint32) []byte {
	data := append([]byte{}, testmedia.WAV()[:44]...)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], byteRate)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	size := seconds * 16000 * 2
	binary.LittleEndian.PutUint32(data[40:], uint32(size))
	data = append(data, make([]byte, size)...)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	return data
}

func TestWAVTimingMalformedUploadAndStoredReinspection(t *testing.T) {
	data := referenceTimingWAV(30, 96000)
	for _, alias := range []string{"audio/wave", "audio/wav", "audio/x-wav"} {
		if file, err := InspectFile("contradictory.wav", data, alias); !errors.Is(err, ErrInvalidImage) || !errors.Is(err, referencevalidation.ErrInvalid) {
			t.Fatal("malformed PCM was accepted through MIME alias", alias, file.Info.Duration, err)
		}
	}
	repo, store := &aacUploadRepository{}, &aacUploadStore{}
	library := New(repo, store)
	ctx := identity.WithOwnerID(context.Background(), "wav-fixture-owner")
	calls := 0
	_, err := library.UploadWithValidation(ctx, "contradictory.wav", bytes.NewReader(data), &referencevalidation.Target{Model: "fixture-15s"}, func(context.Context, *referencevalidation.Target, []referencevalidation.File, bool) error {
		calls++
		return nil
	})
	if !errors.Is(err, ErrInvalidImage) || calls != 0 || len(store.data) != 0 || repo.item.ID != "" {
		t.Fatal("invalid WAV reached validation or storage", err, calls, len(store.data), repo.item.ID)
	}
	// Simulate a historical row rather than bypassing upload validation in runtime.
	sum := sha256.Sum256(data)
	repo.item = Image{ID: uuid.NewString(), Name: "legacy.wav", ContentType: "audio/wave", SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), StorageKey: "wav"}
	store.data, store.kind = data, "audio/wave"
	if _, err := library.Inspect(ctx, repo.item.ID); !errors.Is(err, ErrInvalidImage) {
		t.Fatal("stored WAV trusted old timing metadata", err)
	}
}

func TestWAVTimingMeasuredDurationControlsUploadAndAggregateLimits(t *testing.T) {
	ctx := identity.WithOwnerID(context.Background(), "wav-fixture-owner")
	for _, limit := range []float64{15, 30} {
		repo, store := &aacUploadRepository{}, &aacUploadStore{}
		library := New(repo, store)
		model := core.MediaModel{ID: "fixture-audio", ReferenceMediaLimits: &core.ReferenceMediaLimits{Audio: core.MediaFileLimits{MaxDuration: limit}}}
		validate := func(_ context.Context, _ *referencevalidation.Target, files []referencevalidation.File, complete bool) error {
			return referencevalidation.ValidateFiles(model, files, complete)
		}
		item, err := library.UploadWithValidation(ctx, "thirty.wav", bytes.NewReader(referenceTimingWAV(30, 32000)), &referencevalidation.Target{Model: model.ID}, validate)
		if limit == 15 {
			if !errors.Is(err, referencevalidation.ErrInvalid) || !strings.Contains(err.Error(), "duration 30 seconds") || !strings.Contains(err.Error(), "at most 15 seconds") || len(store.data) != 0 || repo.item.ID != "" {
				t.Fatal("30-second file bypassed 15-second maximum or lost actionable bounds", err)
			}
			if _, err := library.UploadWithValidation(ctx, "fifteen.wav", bytes.NewReader(referenceTimingWAV(15, 32000)), &referencevalidation.Target{Model: model.ID}, validate); err != nil {
				t.Fatal("valid exact 15-second boundary rejected", err)
			}
			continue
		}
		if err != nil {
			t.Fatal("valid exact 30-second boundary rejected", err)
		}
		stored, err := library.Inspect(ctx, item.ID)
		if err != nil || stored.Info.Duration != 30 || !bytes.Equal(store.data, referenceTimingWAV(30, 32000)) {
			t.Fatal("stored duration/bytes changed", stored.Info, err)
		}
		model.ReferenceMediaLimits.Audio.MaxDuration = 15
		if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{stored}, true); !errors.Is(err, referencevalidation.ErrInvalid) {
			t.Fatal("stored reference bypassed tighter model maximum", err)
		}
	}
	file, err := InspectFile("eight.wav", referenceTimingWAV(8, 32000), "audio/x-wav")
	if err != nil {
		t.Fatal(err)
	}
	model := core.MediaModel{ID: "fixture-aggregate", ReferenceMediaLimits: &core.ReferenceMediaLimits{Audio: core.MediaFileLimits{MaxDuration: 15, MaxTotalDuration: 15}}}
	if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{file}, true); err != nil {
		t.Fatal("one valid reference rejected", err)
	}
	if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{file, file}, true); !errors.Is(err, referencevalidation.ErrInvalid) || !strings.Contains(err.Error(), "combined duration 16 seconds") {
		t.Fatal("aggregate duration bypassed", err)
	}
}
