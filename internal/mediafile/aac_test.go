package mediafile

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func aacTestADTS(frames int, index byte) []byte {
	var data []byte
	for i := 0; i < frames; i++ {
		length := 11
		data = append(data, 0xff, 0xf1, 0x40|index<<2, 0x80|byte(length>>11), byte(length>>3), byte(length&7)<<5|0x1f, 0xfc, 0x21, 0x10, 0x04, 0x60)
	}
	return data
}
func aacTestDescriptor(tag byte, data []byte) []byte {
	return append([]byte{tag, byte(len(data))}, data...)
}
func aacTestM4A(version byte, wide, variable bool) []byte {
	ftyp := atom("ftyp", []byte{'M', '4', 'A', ' ', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'})
	payload := []byte{0x21, 0x10, 0x21, 0x10, 0x21, 0x10}
	mdat := atom("mdat", payload)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], 48000)
	binary.BigEndian.PutUint32(mdhd[16:], 3072)
	if version == 1 {
		mdhd = make([]byte, 36)
		mdhd[0] = 1
		binary.BigEndian.PutUint32(mdhd[20:], 48000)
		binary.BigEndian.PutUint64(mdhd[24:], 3072)
	}
	hdlr := make([]byte, 24)
	copy(hdlr[8:], "soun")
	entry := make([]byte, 28)
	binary.BigEndian.PutUint16(entry[6:], 1)
	binary.BigEndian.PutUint16(entry[16:], 2)
	binary.BigEndian.PutUint16(entry[18:], 16)
	binary.BigEndian.PutUint32(entry[24:], 48000<<16)
	decoder := make([]byte, 13)
	decoder[0], decoder[1] = 0x40, 0x15
	decoder = append(decoder, aacTestDescriptor(5, []byte{0x11, 0x90, 0x56, 0xe5, 0})...)
	es := append([]byte{0, 1, 0}, aacTestDescriptor(4, decoder)...)
	es = append(es, aacTestDescriptor(6, []byte{2})...)
	esds := append(make([]byte, 4), aacTestDescriptor(3, es)...)
	entry = append(entry, atom("esds", esds)...)
	stsd := make([]byte, 8)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	stsd = append(stsd, atom("mp4a", entry)...)
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:], 1)
	binary.BigEndian.PutUint32(stts[8:], 3)
	binary.BigEndian.PutUint32(stts[12:], 1024)
	stsz := make([]byte, 12)
	binary.BigEndian.PutUint32(stsz[4:], 2)
	binary.BigEndian.PutUint32(stsz[8:], 3)
	if variable {
		binary.BigEndian.PutUint32(stsz[4:], 0)
		for i := 0; i < 3; i++ {
			stsz = append(stsz, 0, 0, 0, 2)
		}
	}
	stsc := make([]byte, 20)
	binary.BigEndian.PutUint32(stsc[4:], 1)
	binary.BigEndian.PutUint32(stsc[8:], 1)
	binary.BigEndian.PutUint32(stsc[12:], 3)
	binary.BigEndian.PutUint32(stsc[16:], 1)
	offsets := make([]byte, 12)
	binary.BigEndian.PutUint32(offsets[4:], 1)
	binary.BigEndian.PutUint32(offsets[8:], uint32(len(ftyp)+8))
	offsetKind := "stco"
	if wide {
		offsets = make([]byte, 16)
		binary.BigEndian.PutUint32(offsets[4:], 1)
		binary.BigEndian.PutUint64(offsets[8:], uint64(len(ftyp)+8))
		offsetKind = "co64"
	}
	stbl := append(atom("stsd", stsd), atom("stts", stts)...)
	stbl = append(stbl, atom("stsz", stsz)...)
	stbl = append(stbl, atom("stsc", stsc)...)
	stbl = append(stbl, atom(offsetKind, offsets)...)
	dref := make([]byte, 8)
	binary.BigEndian.PutUint32(dref[4:], 1)
	dref = append(dref, atom("url ", []byte{0, 0, 0, 1})...)
	minf := append(atom("smhd", make([]byte, 8)), atom("dinf", atom("dref", dref))...)
	minf = append(minf, atom("stbl", stbl)...)
	mdia := append(atom("mdhd", mdhd), atom("hdlr", hdlr)...)
	mdia = append(mdia, atom("minf", minf)...)
	trak := append(atom("tkhd", make([]byte, 84)), atom("mdia", mdia)...)
	return append(append(ftyp, mdat...), atom("moov", atom("trak", trak))...)
}
func aacTestField(t *testing.T, data []byte, kind string, offset int) []byte {
	t.Helper()
	i := bytes.Index(data, []byte(kind))
	if i < 4 || i+4+offset > len(data) {
		t.Fatalf("missing fixture atom %s", kind)
	}
	return data[i+4+offset:]
}

func TestAACADTSCompleteFramesAndDuration(t *testing.T) {
	for _, index := range []byte{3, 4, 8, 12} {
		data := aacTestADTS(100, index)
		info, err := Inspect(data, "audio/aac")
		want := float64(100*1024) / float64(aacSampleRates[index])
		if err != nil || info.Codec != "aac" || math.Abs(info.Duration-want) > 1e-12 || info.ContentType != "audio/aac" || info.Size != int64(len(data)) {
			t.Fatal(info, err)
		}
	}
	data := aacTestADTS(2, 3)
	data[1] |= 8
	data[12] |= 8
	if _, err := Inspect(data, "audio/aac"); err != nil {
		t.Fatal("consistent MPEG-2 AAC-LC rejected", err)
	}
}
func TestAACADTSRejectsMalformedAndUnsupportedSequences(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"truncated header", func(b []byte) []byte { return b[:6] }},
		{"truncated second frame", func(b []byte) []byte { return b[:len(b)-1] }},
		{"trailing bytes", func(b []byte) []byte { return append(b, 0) }},
		{"broken sync", func(b []byte) []byte { b[11] = 0xfe; return b }},
		{"invalid layer", func(b []byte) []byte { b[1] |= 2; return b }},
		{"CRC protected unsupported", func(b []byte) []byte { b[1] &= 0xfe; return b }},
		{"non LC", func(b []byte) []byte { b[2] &= 0x3f; return b }},
		{"reserved frequency", func(b []byte) []byte { b[2] = 0x7c; return b }},
		{"changed frequency", func(b []byte) []byte { b[13] = 0x50; return b }},
		{"changed channels", func(b []byte) []byte { b[14] = 0x40; return b }},
		{"changed MPEG ID", func(b []byte) []byte { b[12] |= 8; return b }},
		{"PCE channels unsupported", func(b []byte) []byte { b[3] &= 0x3f; return b }},
		{"multiple raw data blocks unsupported", func(b []byte) []byte { b[6] |= 1; return b }},
		{"header without payload", func(b []byte) []byte { b[4] = 0; b[5] = (7 << 5) | 0x1f; return b }},
		{"frame length beyond file", func(b []byte) []byte { b[3] |= 3; b[4] = 255; b[5] = 255; return b }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Inspect(tc.mutate(aacTestADTS(2, 3)), "audio/aac"); err == nil {
				t.Fatal("accepted corrupt AAC")
			}
		})
	}
}

func TestAACM4AInspectsAudioTrackAndSamples(t *testing.T) {
	for _, version := range []byte{0, 1} {
		for _, wide := range []bool{false, true} {
			for _, variable := range []bool{false, true} {
				data := aacTestM4A(version, wide, variable)
				for _, kind := range []string{"audio/mp4", "audio/x-m4a", "audio/m4a"} {
					info, err := Inspect(data, kind)
					if err != nil || info.Codec != "aac" || info.Duration != 0.064 || info.FPS != 0 || info.Width != 0 || info.ContentType != "audio/mp4" {
						t.Fatal(version, wide, variable, kind, info, err)
					}
				}
			}
		}
	}
	data := aacTestM4A(0, false, false)
	binary.BigEndian.PutUint32(aacTestField(t, data, "mdhd", 16), 1024)
	if info, err := Inspect(data, "audio/mp4"); err != nil || info.Duration != 0.064 {
		t.Fatal("short header hid sample duration", info, err)
	}
	data = aacTestM4A(0, false, false)
	binary.BigEndian.PutUint32(aacTestField(t, data, "mdhd", 16), 3)
	binary.BigEndian.PutUint32(aacTestField(t, data, "stts", 12), 1)
	if info, err := Inspect(data, "audio/mp4"); err != nil || info.Duration != 0.064 {
		t.Fatal("short timeline hid AAC packets", info, err)
	}
	data = aacTestM4A(0, false, false)
	binary.BigEndian.PutUint32(aacTestField(t, data, "mdhd", 16), 9600)
	if info, err := Inspect(data, "audio/mp4"); err != nil || info.Duration != 0.2 {
		t.Fatal("long header ignored", info, err)
	}
}
func TestAACM4ARejectsMalformedAndUnsupportedContainers(t *testing.T) {
	tests := []struct {
		name string
		edit func(*testing.T, []byte) []byte
	}{
		{"file truncated", func(_ *testing.T, b []byte) []byte { return b[:len(b)-1] }},
		{"unbounded box", func(t *testing.T, b []byte) []byte {
			i := bytes.Index(b, []byte("moov"))
			binary.BigEndian.PutUint32(b[i-4:i], math.MaxUint32)
			return b
		}},
		{"video handler", func(t *testing.T, b []byte) []byte { copy(aacTestField(t, b, "hdlr", 8), "vide"); return b }},
		{"no sound handler", func(t *testing.T, b []byte) []byte { copy(aacTestField(t, b, "hdlr", 8), "meta"); return b }},
		{"unsupported codec", func(_ *testing.T, b []byte) []byte {
			i := bytes.Index(b, []byte("mp4a"))
			copy(b[i:], "alac")
			return b
		}},
		{"zero duration", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "mdhd", 16), 0)
			return b
		}},
		{"zero scale", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "mdhd", 12), 0)
			return b
		}},
		{"unknown time version", func(t *testing.T, b []byte) []byte { aacTestField(t, b, "mdhd", 0)[0] = 2; return b }},
		{"missing sample timing", func(t *testing.T, b []byte) []byte {
			i := bytes.Index(b, []byte("stts"))
			copy(b[i:], "free")
			return b
		}},
		{"truncated sample timing", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stts", 4), 2)
			return b
		}},
		{"zero sample delta", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stts", 12), 0)
			return b
		}},
		{"sample count mismatch", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stsz", 8), 4)
			return b
		}},
		{"oversized sample", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stsz", 4), 200)
			return b
		}},
		{"unbounded sample count", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stts", 8), math.MaxUint32)
			binary.BigEndian.PutUint32(aacTestField(t, b, "stsz", 8), math.MaxUint32)
			binary.BigEndian.PutUint32(aacTestField(t, b, "stsc", 12), math.MaxUint32)
			return b
		}},
		{"external data reference", func(t *testing.T, b []byte) []byte { aacTestField(t, b, "url ", 3)[0] = 0; return b }},
		{"wrong sample data reference", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint16(aacTestField(t, b, "mp4a", 6), 2)
			return b
		}},
		{"unsupported sound entry version", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint16(aacTestField(t, b, "mp4a", 8), 1)
			return b
		}},
		{"wrong sample rate", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "mp4a", 24), 44100<<16)
			return b
		}},
		{"wrong channels", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint16(aacTestField(t, b, "mp4a", 16), 1)
			return b
		}},
		{"bad decoder descriptors", func(t *testing.T, b []byte) []byte { aacTestField(t, b, "esds", 5)[0] = 127; return b }},
		{"chunk outside media", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stco", 8), uint32(len(b)-1))
			return b
		}},
		{"chunk points at header", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stco", 8), 0)
			return b
		}},
		{"chunk too many samples", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stsc", 12), 4)
			return b
		}},
		{"chunk missing samples", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stsc", 12), 2)
			return b
		}},
		{"chunk description not one", func(t *testing.T, b []byte) []byte {
			binary.BigEndian.PutUint32(aacTestField(t, b, "stsc", 16), 2)
			return b
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Inspect(tc.edit(t, aacTestM4A(0, false, false)), "audio/mp4"); err == nil {
				t.Fatal("accepted malformed M4A")
			}
		})
	}
	data := aacTestM4A(0, false, false)
	for i := 0; i < len(data); i++ {
		if _, err := Inspect(data[:i], "audio/mp4"); err == nil {
			t.Fatalf("accepted truncated prefix %d", i)
		}
	}
	for _, extra := range [][]byte{atom("moof", nil), bytes.Repeat(atom("free", nil), 4097)} {
		if _, err := Inspect(append(data, extra...), "audio/mp4"); err == nil {
			t.Fatal("accepted fragmented or over-budget container")
		}
	}
}
func TestAACM4ACannotBypassVideoInspection(t *testing.T) {
	audio := aacTestM4A(0, false, false)
	if _, err := Inspect(audio, "video/mp4"); err == nil {
		t.Fatal("audio-only file passed video dimension/FPS inspection")
	}
	video := fixtureVideo(5000)
	if _, err := Inspect(video, "audio/mp4"); err == nil {
		t.Fatal("video passed audio-only inspection")
	}
	if info, err := Inspect(video, "video/mp4"); err != nil || info.Width != 640 || info.Height != 360 || info.FPS != 30 {
		t.Fatal("video behavior changed", info, err)
	}
}
func TestAACConfigUnsupportedVariants(t *testing.T) {
	for _, data := range [][]byte{nil, {0x11}, {0x29, 0x90}, {0x11, 0x94}, {0x11, 0x92}, {0x11, 0x91}, {0x17, 0x90}, {0x11, 0x80}, {0x11, 0x90, 0x56, 0xe5, 0x80}, {0x11, 0x90, 0}} {
		if _, _, err := aacConfig(data); err == nil {
			t.Fatalf("accepted unsupported ASC %x", data)
		}
	}
	for _, data := range [][]byte{{3, 0x80}, {3, 0x80, 0x80, 0x80, 0x80, 0}, {3, 127, 0}} {
		if _, _, err := aacDescriptor(data, 3); err == nil {
			t.Fatalf("accepted invalid descriptor %x", data)
		}
	}
}
func FuzzAACInspectionNeverPanics(f *testing.F) {
	f.Add(aacTestADTS(2, 3), false)
	f.Add(aacTestM4A(0, false, false), true)
	f.Add([]byte{0xff, 0xf1}, false)
	f.Fuzz(func(t *testing.T, data []byte, mp4 bool) {
		if len(data) > 1<<20 {
			return
		}
		kind := "audio/aac"
		if mp4 {
			kind = "audio/mp4"
		}
		_, _ = Inspect(data, kind)
	})
}
