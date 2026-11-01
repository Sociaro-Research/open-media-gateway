// Package soundoverride replaces audio using local files and verified video stream copy.
package soundoverride

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
)

var ErrProcessing = errors.New("Override Sound could not produce a verified video; check the audio/video format and worker FFmpeg installation")

type packet struct {
	Hash     string `json:"data_hash"`
	PTS      string `json:"pts_time"`
	DTS      string `json:"dts_time"`
	Duration string `json:"duration_time"`
}
type stream struct {
	Codec    string `json:"codec_name"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration string `json:"duration"`
	Start    string `json:"start_time"`
	Rate     string `json:"avg_frame_rate"`
}
type probe struct {
	Packets []packet `json:"packets"`
	Streams []stream `json:"streams"`
	Format  struct {
		Name string `json:"format_name"`
	} `json:"format"`
}

// Tool output is bounded, and never returned to callers (it can contain paths or metadata).
type bounded struct {
	bytes.Buffer
	limit int
}

func (b *bounded) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("tool output limit")
	}
	return b.Buffer.Write(p)
}
func inspect(ctx context.Context, path string) (probe, error) {
	var p probe
	out := &bounded{limit: 32 << 20}
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm", "-select_streams", "v", "-show_packets", "-show_data_hash", "sha256", "-show_entries", "stream=codec_name,width,height,duration,start_time,avg_frame_rate:packet=pts_time,dts_time,duration_time,data_hash:format=format_name", "-of", "json", path)
	cmd.Stdout = out
	if cmd.Run() != nil || json.Unmarshal(out.Bytes(), &p) != nil || len(p.Streams) != 1 || len(p.Packets) == 0 {
		return p, ErrProcessing
	}
	return p, nil
}
func number(s string) float64 {
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return math.NaN()
	}
	return v
}
func equalTime(a, b string) bool {
	if a == b {
		return true
	}
	return math.Abs(number(a)-number(b)) < 0.00001
}

// Replace supports ordinary MP4/MOV and WebM videos with one video stream and a
// zero presentation origin. Unsupported timelines fail explicitly, never re-encode.
// maxBytes also bounds output while FFmpeg runs. Caller owns timeout and files.
func Replace(ctx context.Context, video, audio, output string, maxBytes int64) (string, error) {
	// Preserve the only-audio-change contract by rejecting auxiliary streams
	// that this output contract cannot retain safely.
	extra := &bounded{limit: 1 << 20}
	check := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm", "-show_entries", "stream=codec_type", "-of", "json", video)
	check.Stdout = extra
	var kinds struct {
		Streams []struct {
			Kind string `json:"codec_type"`
		} `json:"streams"`
	}
	if check.Run() != nil || json.Unmarshal(extra.Bytes(), &kinds) != nil {
		return "", ErrProcessing
	}
	for _, stream := range kinds.Streams {
		if stream.Kind != "video" && stream.Kind != "audio" {
			return "", ErrProcessing
		}
	}
	before, err := inspect(ctx, video)
	if err != nil {
		return "", err
	}
	s := before.Streams[0]
	if math.Abs(number(s.Start)) > 0.00001 || math.IsNaN(number(s.Start)) {
		return "", ErrProcessing
	}
	end := 0.0
	for _, p := range before.Packets {
		v := number(p.PTS) + number(p.Duration)
		if math.IsNaN(v) || p.Hash == "" {
			return "", ErrProcessing
		}
		end = math.Max(end, v)
	}
	if end <= 0 || end > 86400 {
		return "", ErrProcessing
	}
	format, codec, mime := "mp4", "aac", "video/mp4"
	switch before.Format.Name {
	case "mov,mp4,m4a,3gp,3g2,mj2":
	case "matroska,webm":
		format, codec, mime = "webm", "libopus", "video/webm"
	default:
		return "", ErrProcessing
	}
	// Opus pre-skip can make a WebM format start negative even when video starts
	// at zero. Preserve input timestamps explicitly; default rebasing shifts video.
	args := []string{"-nostdin", "-v", "error", "-y", "-copyts", "-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm", "-i", video, "-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm,wav,mp3,ogg,aac,flac", "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-map_metadata", "0", "-c:v", "copy", "-c:a", codec, "-af", fmt.Sprintf("asetpts=PTS-STARTPTS,apad,atrim=duration=%.9f", end), "-threads", "1", "-fs", strconv.FormatInt(maxBytes, 10), "-avoid_negative_ts", "disabled", "-f", format}
	if format == "mp4" {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, output)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if cmd.Run() != nil {
		return "", ErrProcessing
	}
	info, statErr := os.Stat(output)
	if statErr != nil || info.Size() > maxBytes {
		return "", ErrProcessing
	}
	after, err := inspect(ctx, output)
	if err != nil {
		return "", err
	}
	t := after.Streams[0]
	if s.Codec != t.Codec || s.Width != t.Width || s.Height != t.Height || s.Rate != t.Rate || len(before.Packets) != len(after.Packets) {
		return "", ErrProcessing
	}
	for i, p := range before.Packets {
		q := after.Packets[i]
		if p.Hash != q.Hash || !equalTime(p.PTS, q.PTS) || !equalTime(p.DTS, q.DTS) || !equalTime(p.Duration, q.Duration) {
			return "", ErrProcessing
		}
	}
	return mime, nil
}
