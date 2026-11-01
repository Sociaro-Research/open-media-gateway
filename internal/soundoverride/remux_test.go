package soundoverride

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func run(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %s: %v", out, e)
	}
}
func TestReplacePreservesVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture dependency unavailable")
	}
	for _, container := range []string{"mp4", "webm"} {
		for _, tracks := range []string{"none", "one", "multiple"} {
			for _, seconds := range []string{"0.2", "1", "2"} {
				t.Run(container+"/"+tracks+"/"+seconds, func(t *testing.T) {
					dir := t.TempDir()
					video, audio, out := filepath.Join(dir, "source."+container), filepath.Join(dir, "replacement.wav"), filepath.Join(dir, "out."+container)
					args := []string{"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10:duration=1"}
					if tracks != "none" {
						args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-map", "0:v", "-map", "1:a")
						if tracks == "multiple" {
							args = append(args, "-map", "1:a")
						}
					}
					if container == "mp4" {
						args = append(args, "-c:v", "libx264", "-c:a", "aac")
					} else {
						args = append(args, "-c:v", "libvpx-vp9", "-c:a", "libopus")
					}
					run(t, append(args, video)...)
					run(t, "-f", "lavfi", "-i", "sine=frequency=880:duration="+seconds, audio)
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					if _, err := Replace(ctx, video, audio, out, 10<<20); err != nil {
						t.Fatal(err)
					}
					probe := exec.Command("ffprobe", "-v", "error", "-select_streams", "a", "-show_entries", "stream=index", "-of", "csv=p=0", out)
					raw, e := probe.Output()
					if e != nil || string(raw) != "1\n" {
						t.Fatalf("expected only replacement audio: %q %v", raw, e)
					}
					// The production verifier compares every packet hash and PTS/DTS/duration.
					// Decode both sources for an independent PCM check: first tone is 880 Hz.
					pcm, e := exec.Command("ffmpeg", "-v", "error", "-i", out, "-map", "0:a:0", "-t", "0.1", "-ac", "1", "-ar", "8000", "-f", "s16le", "-").Output()
					if e != nil || len(pcm) < 100 {
						t.Fatal("audio is not playable", e)
					}
					changes := 0
					for i := 2; i+1 < len(pcm); i += 2 {
						a := int16(uint16(pcm[i-2]) | uint16(pcm[i-1])<<8)
						b := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
						if a < 0 && b >= 0 {
							changes++
						}
					}
					if changes < 75 || changes > 95 {
						t.Fatalf("expected replacement 880 Hz, got %d positive crossings", changes)
					}
					if seconds == "0.2" {
						silence, e := exec.Command("ffmpeg", "-v", "error", "-i", out, "-ss", "0.6", "-t", "0.2", "-map", "0:a:0", "-ac", "1", "-ar", "8000", "-f", "s16le", "-").Output()
						if e != nil {
							t.Fatal(e)
						}
						for i := 0; i+1 < len(silence); i += 2 {
							v := int16(uint16(silence[i]) | uint16(silence[i+1])<<8)
							if v > 5 || v < -5 {
								t.Fatal("short audio did not leave silence")
							}
						}
					}
				})
			}
		}
	}
}
func TestReplaceFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture dependency unavailable")
	}
	dir := t.TempDir()
	v, a, out := filepath.Join(dir, "v.mp4"), filepath.Join(dir, "a.wav"), filepath.Join(dir, "out.mp4")
	run(t, "-f", "lavfi", "-i", "color=size=160x90:rate=10:duration=1", "-c:v", "libx264", v)
	if err := os.WriteFile(a, []byte("not audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Replace(context.Background(), v, a, out, 10<<20); err == nil {
		t.Fatal("invalid audio accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Replace(ctx, v, a, out, 10<<20); err == nil {
		t.Fatal("cancelled process accepted")
	}
	run(t, "-f", "lavfi", "-i", "sine=duration=1", a)
	if _, err := Replace(context.Background(), v, a, out, 500); err == nil {
		t.Fatal("truncated output accepted")
	}
	if _, err := Replace(context.Background(), v, a, out, 10<<20); err != nil {
		t.Fatal("retry failed", err)
	}
}

func TestReplaceRejectsAuxiliaryStreams(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg fixture dependency unavailable")
	}
	dir := t.TempDir()
	video, audio, sub, out := filepath.Join(dir, "v.mp4"), filepath.Join(dir, "a.wav"), filepath.Join(dir, "text.srt"), filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(sub, []byte("1\n00:00:00,000 --> 00:00:00,900\nKeep this subtitle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(t, "-f", "lavfi", "-i", "color=size=160x90:rate=10:duration=1", "-i", sub, "-c:v", "libx264", "-c:s", "mov_text", video)
	run(t, "-f", "lavfi", "-i", "sine=duration=1", audio)
	if _, err := Replace(context.Background(), video, audio, out, 10<<20); err == nil {
		t.Fatal("subtitle would be silently discarded")
	}
}
