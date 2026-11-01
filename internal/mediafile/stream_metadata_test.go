package mediafile

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func metadataMP4(t *testing.T, handlers ...string) []byte {
	t.Helper()
	top, err := boxes(fixtureVideo(5000))
	if err != nil {
		t.Fatal(err)
	}
	moov := append([]byte(nil), top[1].data...)
	for _, handler := range handlers {
		if handler == "video" {
			moov = append(moov, top[1].data...)
			continue
		}
		if handler == "incomplete" {
			moov = append(moov, atom("trak", []byte("bad"))...)
			continue
		}
		data := make([]byte, 12)
		copy(data[8:], handler)
		moov = append(moov, atom("trak", atom("mdia", atom("hdlr", data)))...)
	}
	return append(atom("ftyp", top[0].data), atom("moov", moov)...)
}
func TestMP4InspectedSampleAndStreamCounts(t *testing.T) {
	for _, tc := range []struct {
		name         string
		handlers     []string
		audio, video int
		known        bool
	}{
		{"video without audio", nil, 0, 1, true}, {"audio follows primary video", []string{"soun"}, 1, 1, true}, {"multiple audio streams", []string{"soun", "soun"}, 2, 1, true}, {"additional video", []string{"video", "soun"}, 1, 2, true}, {"non audio track", []string{"text"}, 0, 1, true}, {"incomplete later metadata", []string{"incomplete"}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Inspect(metadataMP4(t, tc.handlers...), "video/mp4")
			if err != nil || got.FrameCount != 150 || got.FPS != 30 || got.Duration != 5 {
				t.Fatal(got, err)
			}
			if tc.known {
				if got.AudioStreams == nil || got.VideoStreams == nil || *got.AudioStreams != tc.audio || *got.VideoStreams != tc.video {
					t.Fatal(got)
				}
			} else if got.AudioStreams != nil || got.VideoStreams != nil {
				t.Fatal("incomplete track list became proven absence", got)
			}
		})
	}
	// A long container tail is not additional video frames.
	data := fixtureVideo(5000)
	replaceTimingHeader(t, data, 300000)
	got, err := Inspect(data, "video/mp4")
	if err != nil || got.Duration != 10 || got.FrameCount != 150 {
		t.Fatal(got, err)
	}
	audio, err := Inspect(aacTestM4A(0, false, false), "audio/mp4")
	if err != nil || audio.FrameCount != 0 || audio.AudioStreams == nil || *audio.AudioStreams != 1 || audio.VideoStreams == nil || *audio.VideoStreams != 0 {
		t.Fatal("audio samples were mislabeled as video frames", audio, err)
	}
}
func metadataWebM(audioTracks int, blocks [][]byte, defaultCadence bool, incomplete bool) []byte {
	duration := make([]byte, 8)
	binary.BigEndian.PutUint64(duration, math.Float64bits(1000))
	info := element([]byte{0x44, 0x89}, duration)
	video := append(element([]byte{0xb0}, []byte{2, 0x80}), element([]byte{0xba}, []byte{1, 0x68})...)
	track := append(element([]byte{0xd7}, []byte{1}), element([]byte{0x83}, []byte{1})...)
	track = append(track, element([]byte{0xe0}, video)...)
	if defaultCadence {
		track = append(track, element([]byte{0x23, 0xe3, 0x83}, []byte{2, 0x7b, 0x25, 0xa0})...)
	} // 41,666,656 ns, not a frame count.
	tracks := element([]byte{0xae}, track)
	for i := 0; i < audioTracks; i++ {
		entry := append(element([]byte{0xd7}, []byte{byte(i + 2)}), element([]byte{0x83}, []byte{2})...)
		tracks = append(tracks, element([]byte{0xae}, entry)...)
	}
	if incomplete {
		tracks = append(tracks, element([]byte{0xae}, element([]byte{0xd7}, []byte{9}))...)
	}
	body := append(element([]byte{0x15, 0x49, 0xa9, 0x66}, info), element([]byte{0x16, 0x54, 0xae, 0x6b}, tracks)...)
	cluster := []byte{}
	for _, block := range blocks {
		cluster = append(cluster, element([]byte{0xa3}, block)...)
	}
	if len(cluster) > 0 {
		body = append(body, element([]byte{0x1f, 0x43, 0xb6, 0x75}, cluster)...)
	}
	return element([]byte{0x18, 0x53, 0x80, 0x67}, body)
}
func TestWebMInspectedSamplesAndTrackMetadata(t *testing.T) {
	plain := []byte{0x81, 0, 0, 0, 0}
	fixed := []byte{0x81, 0, 0, 4, 2, 1, 2, 3}
	xiph := []byte{0x81, 0, 0, 2, 2, 1, 1, 1, 2, 3}
	ebml := []byte{0x81, 0, 0, 6, 2, 0x81, 0xbf, 1, 2, 3}
	for _, tc := range []struct {
		name   string
		blocks [][]byte
		frames int64
		tracks int
	}{
		{"plain", [][]byte{plain, plain}, 2, 0}, {"fixed lacing", [][]byte{fixed}, 3, 1}, {"Xiph lacing", [][]byte{xiph}, 3, 2}, {"EBML lacing", [][]byte{ebml}, 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Inspect(metadataWebM(tc.tracks, tc.blocks, false, false), "video/webm")
			if err != nil || got.FrameCount != tc.frames || got.AudioStreams == nil || *got.AudioStreams != tc.tracks || got.VideoStreams == nil || *got.VideoStreams != 1 {
				t.Fatal(got, err)
			}
		})
	}
	cadenceOnly, err := Inspect(metadataWebM(0, nil, true, false), "video/webm")
	if err != nil || cadenceOnly.FPS <= 0 || cadenceOnly.FrameCount != 0 || cadenceOnly.AudioStreams == nil || *cadenceOnly.AudioStreams != 0 {
		t.Fatal("nominal cadence invented video frames", cadenceOnly, err)
	}
	incomplete, err := Inspect(metadataWebM(0, [][]byte{plain}, false, true), "video/webm")
	if err != nil || incomplete.FrameCount != 1 || incomplete.AudioStreams != nil || incomplete.VideoStreams != nil {
		t.Fatal("incomplete track metadata became known silence", incomplete, err)
	}
	invalidLacing, err := Inspect(metadataWebM(0, [][]byte{{0x81, 0, 0, 4, 255, 0}}, false, false), "video/webm")
	if err != nil || invalidLacing.FrameCount != 0 {
		t.Fatal("unverifiable lacing published frame-count evidence or changed legacy acceptance", invalidLacing, err)
	}
}

func TestWebMDuplicateTrackIdentityLeavesCountsUnknown(t *testing.T) {
	data := metadataWebM(1, [][]byte{{0x81, 0, 0, 0, 0}}, false, false)
	data = bytes.Replace(data, []byte{0xd7, 0x81, 2, 0x83, 0x81, 2}, []byte{0xd7, 0x81, 1, 0x83, 0x81, 2}, 1)
	got, err := Inspect(data, "video/webm")
	if err != nil || got.FrameCount != 0 || got.AudioStreams != nil || got.VideoStreams != nil {
		t.Fatal("ambiguous video/audio identity became trusted metadata", got, err)
	}
}
