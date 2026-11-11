package mediafile

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func wavTimingFormat(tag, channels, bits, align uint16, sampleRate, byteRate uint32) []byte {
	fmt := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmt, tag)
	binary.LittleEndian.PutUint16(fmt[2:], channels)
	binary.LittleEndian.PutUint32(fmt[4:], sampleRate)
	binary.LittleEndian.PutUint32(fmt[8:], byteRate)
	binary.LittleEndian.PutUint16(fmt[12:], align)
	binary.LittleEndian.PutUint16(fmt[14:], bits)
	return fmt
}

func wavTimingChunk(kind string, body []byte) []byte {
	chunk := make([]byte, 8+len(body)+len(body)%2)
	copy(chunk, kind)
	binary.LittleEndian.PutUint32(chunk[4:], uint32(len(body)))
	copy(chunk[8:], body)
	return chunk
}

func wavTimingFile(chunks ...[]byte) []byte {
	data := []byte("RIFF\x00\x00\x00\x00WAVE")
	for _, chunk := range chunks {
		data = append(data, chunk...)
	}
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	return data
}

func TestWAVTimingRejectsContradictoryPCMByteRate(t *testing.T) {
	// 480000 mono 16-bit frames at 16000 Hz are 30 seconds, not 10.
	data := wavTimingFile(wavTimingChunk("fmt ", wavTimingFormat(1, 1, 16, 2, 16000, 96000)), wavTimingChunk("data", make([]byte, 960000)))
	info, err := Inspect(data, "audio/wave")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("contradictory PCM timing accepted: duration=%g seconds, err=%v; expected invalid metadata", info.Duration, err)
	}
}

func TestWAVTimingValidPCMControl(t *testing.T) {
	data := wavTimingFile(wavTimingChunk("fmt ", wavTimingFormat(1, 1, 16, 2, 16000, 32000)), wavTimingChunk("data", make([]byte, 960000)))
	info, err := Inspect(data, "audio/wave")
	if err != nil || info.Duration != 30 {
		t.Fatalf("valid 30-second PCM: duration=%g, err=%v", info.Duration, err)
	}
}

func wavTimingExtensible(base []byte, valid uint16, subtype uint32) []byte {
	format := make([]byte, 40)
	copy(format, base)
	binary.LittleEndian.PutUint16(format, 0xfffe)
	binary.LittleEndian.PutUint16(format[16:], 22)
	binary.LittleEndian.PutUint16(format[18:], valid)
	binary.LittleEndian.PutUint32(format[24:], subtype)
	copy(format[28:], []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71})
	return format
}

func TestWAVTimingPreservesSupportedLayoutsAndAliases(t *testing.T) {
	adpcm := append(wavTimingFormat(0x11, 1, 4, 256, 8000, 4055), 2, 0, 249, 1) // 505 samples per IMA block.
	custom := wavTimingExtensible(wavTimingFormat(1, 1, 16, 2, 16000, 96000), 16, 1)
	custom[39] ^= 1 // A different GUID is not a registered PCM subtype.
	for _, tc := range []struct {
		name     string
		format   []byte
		bytes    int
		duration float64
	}{
		{"PCM8 mono", wavTimingFormat(1, 1, 8, 1, 8000, 8000), 8001, 8001.0 / 8000},
		{"PCM16 stereo", wavTimingFormat(1, 2, 16, 4, 48000, 192000), 192000, 1},
		{"PCM24", wavTimingFormat(1, 1, 24, 3, 48000, 144000), 144000, 1},
		{"PCM32", wavTimingFormat(1, 2, 32, 8, 48000, 384000), 384000, 1},
		{"float32", wavTimingFormat(3, 2, 32, 8, 48000, 384000), 384000, 1},
		{"float64", wavTimingFormat(3, 1, 64, 8, 8000, 64000), 64000, 1},
		{"A-law", wavTimingFormat(6, 1, 8, 1, 8000, 8000), 8000, 1},
		{"mu-law stereo", wavTimingFormat(7, 2, 8, 2, 8000, 16000), 16000, 1},
		{"extensible PCM24 in32", wavTimingExtensible(wavTimingFormat(1, 2, 32, 8, 48000, 384000), 24, 1), 384000, 1},
		{"extensible float64", wavTimingExtensible(wavTimingFormat(3, 1, 64, 8, 8000, 64000), 64, 3), 64000, 1},
		{"compressed estimate", adpcm, 8192, 8192.0 / 4055},
		{"extensible compressed estimate", wavTimingExtensible(adpcm, 505, 0x11), 8192, 8192.0 / 4055},
		{"unregistered GUID estimate", custom, 96000, 1},
		{"compressed unspecified bits", wavTimingFormat(0x55, 1, 0, 1, 44100, 16000), 16000, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := wavTimingFile(wavTimingChunk("fmt ", tc.format), wavTimingChunk("data", make([]byte, tc.bytes)))
			for _, alias := range []string{"audio/wave", "audio/wav", "audio/x-wav"} {
				info, err := Inspect(data, alias)
				if err != nil || math.Abs(info.Duration-tc.duration) > 1e-12 || info.ContentType != "audio/wave" {
					t.Fatal(alias, info, err)
				}
			}
		})
	}
	if info, err := Inspect(testmedia.WAV(), "audio/x-wav"); err != nil || info.Duration != 1 {
		t.Fatal("existing shared PCM fixture changed", info, err)
	}
}

func TestWAVTimingChunkAccountingAndLegacyTolerance(t *testing.T) {
	format := wavTimingFormat(1, 1, 16, 2, 16000, 32000)
	data := wavTimingFile(wavTimingChunk("JUNK", []byte{1, 2, 3}), wavTimingChunk("data", make([]byte, 320000)), wavTimingChunk("fmt ", format))
	// Retain physical trailing chunks, even when the declared RIFF length is
	// shorter. Otherwise changing the container boundary could hide 20 seconds.
	data = append(data, wavTimingChunk("data", make([]byte, 640000))...)
	if info, err := Inspect(data, "audio/wave"); err != nil || info.Duration != 30 {
		t.Fatal("not all data chunks counted", info, err)
	}
	odd := wavTimingFile(wavTimingChunk("fmt ", wavTimingFormat(1, 1, 8, 1, 8000, 8000)), wavTimingChunk("data", []byte{0}))
	if info, err := Inspect(odd[:len(odd)-1], "audio/wave"); err != nil || info.Duration != 1.0/8000 {
		t.Fatal("terminal odd-length PCM compatibility", info, err)
	}
	pcmExtra := append(format, 255, 255) // PCM ignores cbSize by convention.
	if _, err := Inspect(wavTimingFile(wavTimingChunk("fmt ", pcmExtra), wavTimingChunk("data", []byte{0, 0})), "audio/wave"); err != nil {
		t.Fatal("PCM extension compatibility", err)
	}
}

func TestWAVTimingRejectsInvalidMetadata(t *testing.T) {
	base := wavTimingFormat(1, 1, 16, 2, 16000, 32000)
	ext := wavTimingExtensible(base, 16, 1)
	for _, tc := range []struct {
		name string
		fmt  []byte
	}{
		{"unknown format zero", wavTimingFormat(0, 1, 16, 2, 16000, 32000)},
		{"zero channels", wavTimingFormat(1, 0, 16, 2, 16000, 32000)},
		{"zero samples", wavTimingFormat(1, 1, 16, 2, 0, 32000)},
		{"zero byte rate", wavTimingFormat(1, 1, 16, 2, 16000, 0)},
		{"zero alignment", wavTimingFormat(1, 1, 16, 0, 16000, 32000)},
		{"wrong alignment", wavTimingFormat(1, 1, 16, 1, 16000, 32000)},
		{"zero PCM bits", wavTimingFormat(1, 1, 0, 2, 16000, 32000)},
		{"fractional container", wavTimingFormat(1, 1, 12, 2, 16000, 32000)},
		{"too low rate", wavTimingFormat(1, 1, 16, 2, 16000, 16000)},
		{"byte rate overflow", wavTimingFormat(1, 1, 16, 2, math.MaxUint32, math.MaxUint32-1)},
		{"alignment overflow", wavTimingFormat(1, 32769, 16, 2, 16000, 32000)},
		{"invalid float size", wavTimingFormat(3, 1, 16, 2, 16000, 32000)},
		{"invalid law size", wavTimingFormat(6, 1, 16, 2, 16000, 32000)},
		{"short fmt", base[:15]},
		{"partial cbSize", append(append([]byte{}, base...), 0)},
		{"truncated extension", append(wavTimingFormat(3, 1, 32, 4, 16000, 64000), 4, 0)},
		{"short extensible", ext[:39]},
		{"short extensible cbSize", func() []byte { b := append([]byte{}, ext...); b[16] = 21; return b }()},
		{"valid bits exceed container", wavTimingExtensible(base, 17, 1)},
		{"float precision differs", wavTimingExtensible(wavTimingFormat(3, 1, 32, 4, 16000, 64000), 24, 3)},
		{"extensible contradictory rate", wavTimingExtensible(wavTimingFormat(1, 1, 16, 2, 16000, 96000), 16, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := wavTimingFile(wavTimingChunk("fmt ", tc.fmt), wavTimingChunk("data", []byte{0, 0}))
			if _, err := Inspect(data, "audio/wave"); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid format accepted", err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"no fmt", wavTimingFile(wavTimingChunk("data", []byte{0, 0}))},
		{"no data", wavTimingFile(wavTimingChunk("fmt ", base))},
		{"duplicate fmt", wavTimingFile(wavTimingChunk("fmt ", base), wavTimingChunk("data", []byte{0, 0}), wavTimingChunk("fmt ", base))},
		{"partial frames cannot cancel out", wavTimingFile(wavTimingChunk("fmt ", base), wavTimingChunk("data", []byte{0}), wavTimingChunk("data", []byte{0}))},
		{"dangling chunk header", append(wavTimingFile(wavTimingChunk("fmt ", base), wavTimingChunk("data", []byte{0, 0})), []byte("data")...)},
		{"truncated chunk", wavTimingFile(wavTimingChunk("fmt ", base), []byte{'d', 'a', 't', 'a', 255, 255, 255, 255})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Inspect(tc.data, "audio/wave"); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid chunk layout accepted", err)
			}
		})
	}
}

func FuzzWAVTimingNeverPanics(f *testing.F) {
	f.Add(testmedia.WAV())
	f.Add(wavTimingFile(wavTimingChunk("fmt ", wavTimingExtensible(wavTimingFormat(1, 2, 32, 8, 48000, 384000), 24, 1)), wavTimingChunk("data", make([]byte, 16))))
	f.Add([]byte("RIFF\x04\x00\x00\x00WAVEfmt "))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = Inspect(data, "audio/wave")
	})
}
