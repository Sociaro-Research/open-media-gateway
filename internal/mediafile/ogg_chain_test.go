package mediafile

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"
)

// Ogg uses the non-reflected CRC polynomial from RFC 3533, with the checksum
// field zeroed. Fixtures include complete pages rather than signature-only data.
func oggTestChecksum(page []byte) uint32 {
	var crc uint32
	for i, value := range page {
		if i >= 22 && i < 26 {
			value = 0
		}
		crc ^= uint32(value) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func oggTestPage(flags byte, serial, sequence uint32, granule uint64, packets ...[]byte) []byte {
	var lacing, body []byte
	for _, packet := range packets {
		length := len(packet)
		for length >= 255 {
			lacing = append(lacing, 255)
			length -= 255
		}
		lacing = append(lacing, byte(length))
		body = append(body, packet...)
	}
	if len(lacing) > 255 {
		panic("test page exceeds Ogg segment limit")
	}
	page := make([]byte, 27)
	copy(page, "OggS")
	page[5] = flags
	binary.LittleEndian.PutUint64(page[6:], granule)
	binary.LittleEndian.PutUint32(page[14:], serial)
	binary.LittleEndian.PutUint32(page[18:], sequence)
	page[26] = byte(len(lacing))
	page = append(page, lacing...)
	page = append(page, body...)
	binary.LittleEndian.PutUint32(page[22:], oggTestChecksum(page))
	return page
}

func oggTestOpus(serial uint32, skip uint16, inputRate uint32, frames int) []byte {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8], head[9] = 1, 1
	binary.LittleEndian.PutUint16(head[10:], skip)
	binary.LittleEndian.PutUint32(head[12:], inputRate)
	tags := append([]byte("OpusTags"), 0, 0, 0, 0, 0, 0, 0, 0)
	var packets [][]byte
	for i := 0; i < frames; i++ {
		// A mono 20 ms CELT silence packet; each packet represents 960 samples.
		packets = append(packets, []byte{0xf8, 0xff, 0xfe})
	}
	data := oggTestPage(2, serial, 0, 0, head)
	data = append(data, oggTestPage(0, serial, 1, 0, tags)...)
	return append(data, oggTestPage(4, serial, 2, uint64(frames)*960, packets...)...)
}

func oggTestPages(t testing.TB, data []byte) [][]byte {
	t.Helper()
	var pages [][]byte
	for len(data) > 0 {
		if len(data) < 27 || !bytes.Equal(data[:4], []byte("OggS")) || data[4] != 0 {
			t.Fatal("invalid fixture page header")
		}
		header := 27 + int(data[26])
		if header > len(data) {
			t.Fatal("truncated fixture lacing")
		}
		size := header
		for _, n := range data[27:header] {
			size += int(n)
		}
		if size > len(data) {
			t.Fatal("truncated fixture page")
		}
		page := data[:size]
		if binary.LittleEndian.Uint32(page[22:]) != oggTestChecksum(page) {
			t.Fatal("invalid fixture CRC")
		}
		pages = append(pages, page)
		data = data[size:]
	}
	return pages
}

func oggTestValidateLink(t testing.TB, data []byte) {
	t.Helper()
	pages := oggTestPages(t, data)
	if len(pages) < 3 || pages[0][5] != 2 || pages[len(pages)-1][5] != 4 {
		t.Fatal("fixture lacks separate BOS, headers and EOS")
	}
	serial := binary.LittleEndian.Uint32(pages[0][14:])
	for i, page := range pages {
		if binary.LittleEndian.Uint32(page[14:]) != serial || binary.LittleEndian.Uint32(page[18:]) != uint32(i) {
			t.Fatal("fixture serial/sequence mismatch")
		}
		if i > 0 && i < len(pages)-1 && page[5] != 0 {
			t.Fatal("unexpected fixture stream boundary")
		}
	}
}

func oggTestVorbis(t testing.TB, serial, rate uint32) []byte {
	t.Helper()
	packed, err := base64.StdEncoding.DecodeString(oggVorbisSilence)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(reader, 8193))
	_ = reader.Close()
	if err != nil || len(data) > 8192 {
		t.Fatal("invalid compressed fixture", err)
	}
	pages := oggTestPages(t, data)
	for i, page := range pages {
		binary.LittleEndian.PutUint32(page[14:], serial)
		if i == 0 {
			header := 27 + int(page[26])
			binary.LittleEndian.PutUint32(page[header+12:], rate)
		}
		binary.LittleEndian.PutUint32(page[22:], oggTestChecksum(page))
	}
	return data
}

func TestOggSequentialLinksNeverReportFirstLinkOnly(t *testing.T) {
	for _, codec := range []string{"opus", "vorbis"} {
		t.Run(codec, func(t *testing.T) {
			first, second := oggTestOpus(11, 312, 44100, 3), oggTestOpus(29, 120, 16000, 5)
			wantFirst, wantSecond := float64(2880-312)/48000, float64(4800-120)/48000
			if codec == "vorbis" {
				first, second = oggTestVorbis(t, 11, 48000), oggTestVorbis(t, 29, 24000)
				wantFirst, wantSecond = 1, 2
			}
			for i, link := range [][]byte{first, second} {
				oggTestValidateLink(t, link)
				got, err := Inspect(link, "audio/ogg")
				want := []float64{wantFirst, wantSecond}[i]
				if err != nil || got.Codec != codec || math.Abs(got.Duration-want) > 1e-9 {
					t.Fatalf("single link %d: %+v err=%v want=%g", i, got, err, want)
				}
			}
			// This metadata-only inspector does not establish codec-specific
			// initial PCM offsets. An unsupported chain must not report link 1.
			data := append(append([]byte{}, first...), second...)
			got, err := Inspect(data, "audio/ogg")
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("chain returned partial metadata: duration=%g first=%g actual total=%g err=%v", got.Duration, wantFirst, wantFirst+wantSecond, err)
			}
		})
	}
}

func TestOggSingleStreamTimingPreserved(t *testing.T) {
	for _, skip := range []uint16{0, 312, 65535} {
		t.Run("Opus pre-skip "+fmt.Sprint(skip), func(t *testing.T) {
			frames := 120
			link := oggTestOpus(0, skip, 16000, frames)
			oggTestValidateLink(t, link)
			got, err := Inspect(link, "audio/ogg")
			want := float64(frames*960-int(skip)) / 48000
			if err != nil || got.Codec != "opus" || math.Abs(got.Duration-want) > 1e-9 {
				t.Fatal(got, err, want)
			}
		})
	}
	pages := oggTestPages(t, oggTestOpus(19, 312, 44100, 3))
	data := append([]byte{}, pages[0]...)
	data = append(data, pages[1]...)
	data = append(data, oggTestPage(0, 19, 2, 960, []byte{0xf8, 0xff, 0xfe})...)
	data = append(data, oggTestPage(4, 19, 3, 2880, []byte{0xf8, 0xff, 0xfe}, []byte{0xf8, 0xff, 0xfe})...)
	oggTestValidateLink(t, data)
	got, err := Inspect(data, "audio/ogg")
	if err != nil || got.Duration != float64(2880-312)/48000 {
		t.Fatal("ordinary multi-page single stream rejected", got, err)
	}
	// Keep the pre-existing single-stream tolerance when EOS is absent. This
	// change establishes stream boundaries, not full codec/container validity.
	last := oggTestPages(t, data)[3]
	last[5] = 0
	binary.LittleEndian.PutUint32(last[22:], oggTestChecksum(last))
	got, err = Inspect(data, "audio/ogg")
	if err != nil || got.Duration != float64(2880-312)/48000 {
		t.Fatal("single-stream compatibility changed", got, err)
	}
	for _, rate := range []uint32{24000, 48000} {
		got, err = Inspect(oggTestVorbis(t, 7, rate), "audio/ogg")
		if err != nil || got.Codec != "vorbis" || got.Duration != 48000/float64(rate) {
			t.Fatal("Vorbis per-stream sample rate changed", got, err)
		}
	}
}

func TestOggUnsupportedStreamBoundaries(t *testing.T) {
	a := oggTestPages(t, oggTestOpus(11, 312, 48000, 3))
	b := oggTestPages(t, oggTestOpus(29, 120, 48000, 5))
	join := func(pages ...[]byte) []byte { return bytes.Join(pages, nil) }
	for name, data := range map[string][]byte{
		"multiplexed streams": join(a[0], b[0], a[1], b[1], a[2], b[2]),
		"reused serial BOS":   join(a[0], a[1], a[2], a[0], a[1], a[2]),
		"post-EOS same serial without BOS": join(a[0], a[1], a[2],
			oggTestPage(0, 11, 3, 960, []byte{0xf8, 0xff, 0xfe})),
		"post-EOS empty page": join(a[0], a[1], a[2], oggTestPage(0, 11, 3, 0)),
		"different serial without BOS": join(a[0], a[1],
			oggTestPage(0, 29, 2, 4800, []byte{0xf8, 0xff, 0xfe})),
		"later BOS before EOS": join(a[0], a[1], a[0], a[2]),
		"unsupported later codec": join(a[0], a[1], a[2],
			oggTestPage(2, 29, 0, 0, []byte("unsupported codec"))),
	} {
		t.Run(name, func(t *testing.T) {
			if info, err := Inspect(data, "audio/ogg"); !errors.Is(err, ErrInvalid) || info.Duration != 0 {
				t.Fatal("unsupported stream returned partial metadata", info, err)
			}
		})
	}
}

func TestOggPageBoundsAndInvalidTiming(t *testing.T) {
	for _, data := range [][]byte{oggTestOpus(11, 312, 48000, 3), oggTestVorbis(t, 29, 48000)} {
		pages := oggTestPages(t, data)
		last := len(data) - len(pages[len(pages)-1])
		for cut := last + 1; cut < len(data); cut++ {
			if _, err := Inspect(data[:cut], "audio/ogg"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("partial page accepted at cut %d/%d", cut, len(data))
			}
		}
		for _, n := range []int{1, 26, 27} {
			if _, err := Inspect(append(append([]byte{}, data...), make([]byte, n)...), "audio/ogg"); !errors.Is(err, ErrInvalid) {
				t.Fatal("truncated trailing page accepted")
			}
		}
	}
	data := oggTestOpus(11, 312, 48000, 3)
	pages := oggTestPages(t, data)
	last := pages[len(pages)-1]
	binary.LittleEndian.PutUint64(last[6:], 311)
	binary.LittleEndian.PutUint32(last[22:], oggTestChecksum(last))
	if _, err := Inspect(data, "audio/ogg"); !errors.Is(err, ErrInvalid) {
		t.Fatal("granule below pre-skip accepted")
	}
	if _, err := Inspect(oggTestVorbis(t, 29, 0), "audio/ogg"); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero Vorbis sample rate accepted")
	}
}

func FuzzOggStreamBoundaries(f *testing.F) {
	first, second := oggTestOpus(11, 312, 48000, 3), oggTestOpus(29, 120, 16000, 5)
	f.Add(first)
	f.Add(append(append([]byte{}, first...), second...))
	f.Add(oggTestVorbis(f, 11, 48000))
	f.Add(oggFixture())
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		info, err := Inspect(data, "audio/ogg")
		if err == nil && (info.Duration <= 0 || math.IsNaN(info.Duration) || math.IsInf(info.Duration, 0)) {
			t.Fatal("invalid duration returned", info.Duration)
		}
	})
}

// One second of zero-origin stereo silence, encoded locally with FFmpeg 8.1's
// native Vorbis encoder at 48 kHz. Complete codec headers and audio are retained;
// tests need no encoder, process, network or external file. The second link's
// declared sample rate may differ without changing its sample-count granules.
const oggVorbisSilence = "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"
