package mediafile

import (
	"encoding/binary"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"math"
	"testing"
)

func TestMP4AverageFPSUsesSampleTiming(t *testing.T) {
	// The reported re-encoded file has 244 constant 512-tick samples and
	// a 125952-tick mdhd header. The extra 1024 header ticks are not frames.
	data := testmedia.MP4(124928, 12288, 244)
	replaceTimingHeader(t, data, 125952)
	info, err := Inspect(data, "video/mp4")
	if err != nil || info.FPS != 24 || info.Duration != 10.25 {
		t.Fatal(info, err)
	}
	// The original file really has a long final sample; its average remains low.
	original := testmedia.MP4(125956, 12288, 244)
	info, err = Inspect(original, "video/mp4")
	if err != nil || math.Abs(info.FPS-23.804122074375176) > 1e-10 {
		t.Fatal(info, err)
	}
	// Strictly below the model minimum must not round to 24.
	ntsc := testmedia.MP4(240*1001, 24000, 240)
	info, err = Inspect(ntsc, "video/mp4")
	if err != nil || info.FPS >= 24 {
		t.Fatal(info, err)
	}
}
func replaceTimingHeader(t *testing.T, data []byte, ticks uint32) {
	t.Helper()
	for i := 0; i+28 < len(data); i++ {
		if string(data[i:i+4]) == "mdhd" {
			binary.BigEndian.PutUint32(data[i+20:i+24], ticks)
			return
		}
	}
	t.Fatal("missing mdhd")
}
func TestMP4RejectsInvalidSampleTiming(t *testing.T) {
	for _, bad := range []uint32{0} {
		data := testmedia.MP4(5000, 1000, 100)
		for i := 0; i+20 <= len(data); i++ {
			if string(data[i:i+4]) == "stts" {
				binary.BigEndian.PutUint32(data[i+16:i+20], bad)
				break
			}
		}
		if _, err := Inspect(data, "video/mp4"); err == nil {
			t.Fatal("zero sample delta accepted")
		}
	}
}

func TestMP4ShortHeaderCannotHideSampleDuration(t *testing.T) {
	data := testmedia.MP4(720*512, 12288, 720)
	replaceTimingHeader(t, data, 122880)
	info, err := Inspect(data, "video/mp4")
	if err != nil || info.FPS != 24 || info.Duration != 30 {
		t.Fatal(info, err)
	}
}
func TestMP4TimingOverflowAndEmptyEntries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		count, delta uint32
	}{{"zero count", 0, 512}, {"zero delta", 244, 0}, {"overflow", ^uint32(0), ^uint32(0)}} {
		t.Run(tc.name, func(t *testing.T) {
			data := testmedia.MP4(125956, 12288, 244)
			for i := 0; i+28 <= len(data); i++ {
				if string(data[i:i+4]) == "stts" {
					binary.BigEndian.PutUint32(data[i+12:i+16], tc.count)
					binary.BigEndian.PutUint32(data[i+16:i+20], tc.delta)
					if tc.name == "overflow" {
						binary.BigEndian.PutUint32(data[i+20:i+24], tc.count)
						binary.BigEndian.PutUint32(data[i+24:i+28], tc.delta)
					}
					break
				}
			}
			if _, err := Inspect(data, "video/mp4"); err == nil {
				t.Fatal("invalid timing accepted")
			}
		})
	}
}
