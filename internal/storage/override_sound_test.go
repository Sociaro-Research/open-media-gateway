package storage

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestOverrideSoundManagerS3AndLocalDestinations(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture dependency unavailable")
	}
	dir := t.TempDir()
	video, audio := filepath.Join(dir, "v.mp4"), filepath.Join(dir, "a.wav")
	for _, args := range [][]string{{"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=1", "-c:v", "libx264", video}, {"-f", "lavfi", "-i", "sine=duration=0.5", audio}} {
		if out, err := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("fixture %s %v", out, err)
		}
	}
	v, _ := os.ReadFile(video)
	a, _ := os.ReadFile(audio)
	actor := identity.WithOwnerID(context.Background(), uuid.NewString())
	shared := identity.WithResourceOwners(identity.WithDataOwnerID(actor, templates.LibraryOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
	manager := newTestManager(t, newSettingsMemory(), newMemoryS3())
	manager.maxBytes = 10 << 20
	manager.local.maxBytes = 10 << 20
	if _, err := manager.Update(shared, s3Update()); err != nil {
		t.Fatal(err)
	}
	source, err := manager.StoreTemplateMedia(shared, uuid.NewString(), a, "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	sound := core.SoundOverride{ReferenceID: uuid.NewString(), OwnerID: templates.LibraryOwnerID, DataOwnerID: templates.LibraryOwnerID, StorageOwnerID: identity.SharedOwnerID, StorageKey: source.StorageKey, SHA256: source.SHA256, SizeBytes: source.SizeBytes}
	for _, driver := range []string{"s3", "local"} {
		t.Run(driver, func(t *testing.T) {
			targetCtx := identity.WithResourceOwners(actor, identity.SharedOwnerID, identity.SharedOwnerID)
			if driver == "local" {
				if _, err = manager.Update(targetCtx, Update{Driver: "local"}); err != nil {
					t.Fatal(err)
				}
			}
			target, err := manager.ForArchive(targetCtx)
			if err != nil {
				t.Fatal(err)
			}
			result, err := manager.ArchiveOverride(targetCtx, target, uuid.NewString(), "data:video/mp4;base64,"+base64.StdEncoding.EncodeToString(v), sound)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(result.StorageKey, "s3:") != (driver == "s3") {
				t.Fatal("wrong destination", result.StorageKey)
			}
			object, err := manager.Open(targetCtx, result.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(object.Reader)
			object.Reader.Close()
			if err != nil || len(data) != int(result.SizeBytes) || result.SHA256 == "" {
				t.Fatal("result readback failed", err)
			}
			if _, err = manager.Open(targetCtx, source.StorageKey); err == nil {
				t.Fatal("private result owner read shared source directly")
			}
		})
	}
}
