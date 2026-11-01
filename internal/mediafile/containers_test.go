package mediafile

import (
	"encoding/binary"
	"math"
	"testing"
)

func element(id []byte, data []byte) []byte {
	b := append([]byte{}, id...)
	if len(data) < 127 {
		b = append(b, 0x80|byte(len(data)))
	} else {
		b = append(b, 0x40|byte(len(data)>>8), byte(len(data)))
	}
	return append(b, data...)
}
func webmFixture() []byte {
	duration := make([]byte, 8)
	binary.BigEndian.PutUint64(duration, math.Float64bits(1000))
	info := append(element([]byte{0x2a, 0xd7, 0xb1}, []byte{0x0f, 0x42, 0x40}), element([]byte{0x44, 0x89}, duration)...)
	video := append(element([]byte{0xb0}, []byte{2, 0x80}), element([]byte{0xba}, []byte{1, 0x68})...)
	track := append(element([]byte{0xd7}, []byte{1}), element([]byte{0x83}, []byte{1})...)
	track = append(track, element([]byte{0xe0}, video)...)
	body := append(element([]byte{0x15, 0x49, 0xa9, 0x66}, info), element([]byte{0x16, 0x54, 0xae, 0x6b}, element([]byte{0xae}, track))...)
	cluster := []byte{}
	for i := 0; i < 24; i++ {
		cluster = append(cluster, element([]byte{0xa3}, []byte{0x81, 0, 0, 0, 0})...)
	}
	body = append(body, element([]byte{0x1f, 0x43, 0xb6, 0x75}, cluster)...)
	header := element([]byte{0x1a, 0x45, 0xdf, 0xa3}, element([]byte{0x42, 0x82}, []byte("webm")))
	return append(header, element([]byte{0x18, 0x53, 0x80, 0x67}, body)...)
}
func oggFixture() []byte {
	body := make([]byte, 19)
	copy(body, "OpusHead")
	body[8] = 1
	body[9] = 1
	page := make([]byte, 28)
	copy(page, "OggS")
	page[26] = 1
	page[27] = byte(len(body))
	binary.LittleEndian.PutUint64(page[6:], 48000)
	return append(page, body...)
}
func flacFixture() []byte {
	b := make([]byte, 42)
	copy(b, "fLaC")
	b[4] = 0x80
	b[7] = 34
	binary.BigEndian.PutUint64(b[18:], uint64(48000)<<44|48000)
	return b
}
func TestAdditionalLibraryFormatsHaveReadableMetadata(t *testing.T) {
	for _, tc := range []struct {
		kind string
		data []byte
	}{{"video/webm", webmFixture()}, {"audio/ogg", oggFixture()}, {"audio/flac", flacFixture()}} {
		t.Run(tc.kind, func(t *testing.T) {
			info, err := Inspect(tc.data, tc.kind)
			if err != nil || info.Duration != 1 {
				t.Fatal(info, err)
			}
			if tc.kind == "video/webm" && (info.Width != 640 || info.Height != 360 || info.FPS != 24) {
				t.Fatal(info)
			}
			for _, bad := range [][]byte{nil, tc.data[:8], tc.data[:len(tc.data)-1]} {
				if _, err = Inspect(bad, tc.kind); err == nil {
					t.Fatal("truncated metadata accepted")
				}
			}
		})
	}
}
