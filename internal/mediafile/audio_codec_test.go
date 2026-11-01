package mediafile

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Synthetic self-contained sample tables exercise actual parser paths. The AAC
// sample payload is not presented as decoded speech; AAC identity is proved by
// the decoder configuration. MPEG audio additionally requires LayerIII frames.
func codecAudioTrack(codec string, offset uint32) ([]byte, []byte) {
	rate := uint32(48000)
	oti := byte(0x40)
	config := []byte{0x11, 0x90}
	sample := []byte{0x21, 0x10}
	if codec == "he-aac" {
		rate = 44100
		config = []byte{0x2b, 0x92, 0x08, 0x00}
	}
	if codec == "he-aac-v2" {
		rate = 44100
		config = []byte{0xeb, 0x8a, 0x08, 0x00}
	}
	if codec == "he-aac-sync" {
		rate = 44100
		config = []byte{0x13, 0x90, 0x56, 0xe5, 0xa0}
	}
	if codec == "he-aac-v2-sync" {
		rate = 44100
		config = []byte{0x13, 0x88, 0x56, 0xe5, 0xa5, 0x48, 0x80}
	}
	if codec == "mp3" || codec == "layer2" {
		rate = 44100
		oti = 0x6b
		config = nil
		sample = make([]byte, 417)
		copy(sample, []byte{0xff, 0xfb, 0x90, 0})
		if codec == "layer2" {
			sample[1] = 0xfd
		}
	}
	entry := make([]byte, 28)
	binary.BigEndian.PutUint16(entry[6:], 1)
	binary.BigEndian.PutUint16(entry[16:], 2)
	binary.BigEndian.PutUint16(entry[18:], 16)
	binary.BigEndian.PutUint32(entry[24:], rate<<16)
	decoder := make([]byte, 13)
	decoder[0], decoder[1] = oti, 0x15
	if codec == "missing-asc" {
		config = nil
	}
	if len(config) > 0 {
		decoder = append(decoder, aacTestDescriptor(5, config)...)
	}
	es := append([]byte{0, 1, 0}, aacTestDescriptor(4, decoder)...)
	es = append(es, aacTestDescriptor(6, []byte{2})...)
	entry = append(entry, atom("esds", append(make([]byte, 4), aacTestDescriptor(3, es)...))...)
	kind := "mp4a"
	if codec == "encrypted" {
		kind = "enca"
	}
	if codec == "opus" {
		kind = "Opus"
	}
	stsd := make([]byte, 8)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	stsd = append(stsd, atom(kind, entry)...)
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:], 1)
	binary.BigEndian.PutUint32(stts[8:], 3)
	binary.BigEndian.PutUint32(stts[12:], 1024)
	stsz := make([]byte, 12)
	binary.BigEndian.PutUint32(stsz[4:], uint32(len(sample)))
	binary.BigEndian.PutUint32(stsz[8:], 3)
	stsc := make([]byte, 20)
	binary.BigEndian.PutUint32(stsc[4:], 1)
	binary.BigEndian.PutUint32(stsc[8:], 1)
	binary.BigEndian.PutUint32(stsc[12:], 3)
	binary.BigEndian.PutUint32(stsc[16:], 1)
	stco := make([]byte, 12)
	binary.BigEndian.PutUint32(stco[4:], 1)
	binary.BigEndian.PutUint32(stco[8:], offset)
	stbl := append(atom("stsd", stsd), atom("stts", stts)...)
	stbl = append(stbl, atom("stsz", stsz)...)
	stbl = append(stbl, atom("stsc", stsc)...)
	stbl = append(stbl, atom("stco", stco)...)
	dref := make([]byte, 8)
	binary.BigEndian.PutUint32(dref[4:], 1)
	dref = append(dref, atom("url ", []byte{0, 0, 0, 1})...)
	minf := append(atom("dinf", atom("dref", dref)), atom("stbl", stbl)...)
	hdlr := make([]byte, 24)
	copy(hdlr[8:], "soun")
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], rate)
	binary.BigEndian.PutUint32(mdhd[16:], 3072)
	mdia := append(atom("hdlr", hdlr), atom("mdhd", mdhd)...)
	mdia = append(mdia, atom("minf", minf)...)
	track := append(atom("tkhd", make([]byte, 84)), atom("mdia", mdia)...)
	return atom("trak", track), bytes.Repeat(sample, 3)
}
func codecVideo(t interface {
	Helper()
	Fatal(...any)
}, codecs ...string) []byte {
	t.Helper()
	top, err := boxes(fixtureVideo(5000))
	if err != nil {
		t.Fatal(err)
	}
	ftyp := atom("ftyp", top[0].data)
	movie := bytes.Clone(top[1].data)
	out := bytes.Clone(ftyp)
	for _, codec := range codecs {
		track, payload := codecAudioTrack(codec, uint32(len(out)+8))
		out = append(out, atom("mdat", payload)...)
		movie = append(movie, track...)
	}
	return append(out, atom("moov", movie)...)
}
func TestEmbeddedAudioCodecsRequirePerStreamDecoderAndSampleProof(t *testing.T) {
	for _, tc := range []struct{ names, want []string }{{nil, nil}, {[]string{"aac"}, []string{"aac"}}, {[]string{"mp3"}, []string{"mp3"}}, {[]string{"aac", "mp3"}, []string{"aac", "mp3"}}, {[]string{"mp3", "aac"}, []string{"mp3", "aac"}}, {[]string{"missing-asc"}, []string{""}}, {[]string{"layer2"}, []string{""}}, {[]string{"encrypted"}, []string{""}}, {[]string{"opus"}, []string{""}}} {
		data := codecVideo(t, tc.names...)
		got, err := Inspect(data, "video/mp4")
		if err != nil || got.AudioStreams == nil || *got.AudioStreams != len(tc.names) || !reflect.DeepEqual(got.AudioCodecs, tc.want) || got.Codec != "" || got.FPS != 30 {
			t.Fatal(tc.names, got, err)
		}
	}
}
func TestEmbeddedAudioCodecCorruptionRemainsUnknownWithoutChangingVideoAcceptance(t *testing.T) {
	cases := []struct {
		name   string
		change func([]byte)
	}{
		{"generic mp4a has no codec guarantee", func(b []byte) { i := bytes.Index(b, []byte{0x05, 0x02, 0x11, 0x90}); b[i+2] = 0x19 }},
		{"non audio object type", func(b []byte) { i := bytes.Index(b, []byte{0x40, 0x15}); b[i] = 0x20 }},
		{"wrong stream type", func(b []byte) { i := bytes.Index(b, []byte{0x40, 0x15}); b[i+1] = 0x11 }},
		{"external data reference", func(b []byte) { i := bytes.Index(b, []byte("url ")); b[i+7] = 0 }},
		{"audio chunk points outside mdat", func(b []byte) {
			i := bytes.Index(b, []byte("stco"))
			binary.BigEndian.PutUint32(b[i+12:], uint32(len(b)-8))
		}},
		{"sample size exceeds bytes", func(b []byte) { i := bytes.Index(b, []byte("stsz")); binary.BigEndian.PutUint32(b[i+8:], ^uint32(0)) }},
		{"sample description index does not match", func(b []byte) { i := bytes.Index(b, []byte("stsc")); binary.BigEndian.PutUint32(b[i+20:], 2) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := codecVideo(t, "aac")
			tc.change(data)
			got, err := Inspect(data, "video/mp4")
			if err != nil || got.AudioStreams == nil || *got.AudioStreams != 1 || len(got.AudioCodecs) != 1 || got.AudioCodecs[0] != "" {
				t.Fatal(got, err)
			}
		})
	}
	data := codecVideo(t, "mp3")
	i := bytes.Index(data, []byte{0xff, 0xfb, 0x90, 0})
	data[i+417] = 0
	got, err := Inspect(data, "video/mp4")
	if err != nil || len(got.AudioCodecs) != 1 || got.AudioCodecs[0] != "" {
		t.Fatal("a later invalid MPEG audio frame was ignored", got, err)
	}
}
func TestEmbeddedAudioCodecsLocalEncoderFixtures(t *testing.T) {
	dir := os.Getenv("GATEWAY_AUDIO_CODEC_FIXTURES")
	if dir == "" {
		t.Skip("optional locally encoded fixtures not supplied")
	}
	for _, codec := range []string{"aac", "mp3", "he-aac", "he-aac-v2", "aac-5-1"} {
		data, err := os.ReadFile(filepath.Join(dir, codec+".mp4"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Inspect(data, "video/mp4")
		if err != nil || !reflect.DeepEqual(got.AudioCodecs, []string{func() string {
			if codec == "mp3" {
				return "mp3"
			}
			return "aac"
		}()}) {
			t.Fatal(codec, got, err)
		}
		t.Logf("inspected actual local encoder fixture %s bytes=%d audio=%v", codec, len(data), got.AudioCodecs)
	}
}
func FuzzEmbeddedAudioCodecInspection(f *testing.F) {
	f.Add(codecVideo(f, "aac"))
	f.Add(codecVideo(f, "mp3"))
	f.Add(codecVideo(f, "he-aac"))
	f.Add(codecVideo(f, "he-aac-v2"))
	f.Add(codecVideo(f, "he-aac-sync"))
	f.Add(codecVideo(f, "he-aac-v2-sync"))
	silent, err := boxes(fixtureVideo(5000))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(codecVideo(f, "opus"), atom("moov", silent[1].data)...))
	f.Add(append(codecVideo(f), atom("moof", nil)...))
	movie := append(bytes.Clone(silent[1].data), atom("mvex", nil)...)
	f.Add(append(atom("ftyp", silent[0].data), atom("moov", movie)...))
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = Inspect(data, "video/mp4") })
}

func TestEmbeddedHEAACExplicitASCAndMalformedNestedConfiguration(t *testing.T) {
	for _, name := range []string{"he-aac", "he-aac-v2"} {
		data := codecVideo(t, name)
		info, err := Inspect(data, "video/mp4")
		if err != nil || !reflect.DeepEqual(info.AudioCodecs, []string{"aac"}) {
			t.Fatal(name, info, err)
		}
		for _, mode := range []string{"nonAACnestedObject", "unsupportedGAextension", "nonzeroPadding", "contradictoryRate", "invalidPSchannels"} {
			if mode == "invalidPSchannels" && name != "he-aac-v2" {
				continue
			}
			bad := bytes.Clone(data)
			i := bytes.Index(bad, []byte{0x05, 0x04})
			if i < 0 {
				t.Fatal("fixture ASC missing")
			}
			switch mode {
			case "nonAACnestedObject":
				bad[i+4] &= 0x83
			case "unsupportedGAextension":
				bad[i+5] |= 0x80
			case "nonzeroPadding":
				bad[i+5] |= 1
			case "contradictoryRate":
				entry := bytes.Index(bad, []byte("mp4a"))
				binary.BigEndian.PutUint32(bad[entry+28:], 48000<<16)
			case "invalidPSchannels":
				bad[i+3] = 0x92
			}
			info, err = Inspect(bad, "video/mp4")
			if err != nil || len(info.AudioCodecs) != 1 || info.AudioCodecs[0] != "" {
				t.Fatal(name, mode, info, err)
			}
		}
	}
}

func TestEmbeddedHEAACSyncExtensionsAreRecognizedFromActualConfiguration(t *testing.T) {
	for _, name := range []string{"he-aac-sync", "he-aac-v2-sync"} {
		data := codecVideo(t, name)
		got, err := Inspect(data, "video/mp4")
		if err != nil || !reflect.DeepEqual(got.AudioCodecs, []string{"aac"}) {
			t.Fatal(name, got, err)
		}
	}
}

func TestEmbeddedAACChannelConfigurationsPreserveMultichannelEvidence(t *testing.T) {
	for configuration := byte(1); configuration <= 7; configuration++ {
		data := codecVideo(t, "aac")
		asc := bytes.Index(data, []byte{0x05, 0x02, 0x11, 0x90})
		entry := bytes.Index(data, []byte("mp4a"))
		if asc < 0 || entry < 0 {
			t.Fatal("fixture configuration missing")
		}
		data[asc+3] = data[asc+3]&0x87 | configuration<<3
		channels := uint16(configuration)
		if configuration == 7 {
			channels = 8
		}
		binary.BigEndian.PutUint16(data[entry+20:], channels)
		got, err := Inspect(data, "video/mp4")
		if err != nil || !reflect.DeepEqual(got.AudioCodecs, []string{"aac"}) {
			t.Fatal("valid multichannel AAC configuration rejected", configuration, got, err)
		}
	}
	data := codecVideo(t, "aac")
	entry := bytes.Index(data, []byte("mp4a"))
	binary.BigEndian.PutUint16(data[entry+20:], 6)
	got, err := Inspect(data, "video/mp4")
	if err != nil || len(got.AudioCodecs) != 1 || got.AudioCodecs[0] != "" {
		t.Fatal("contradictory ASC and sample-entry channels accepted", got, err)
	}
}

func TestEmbeddedAudioAmbiguousInventoryCannotProveSilence(t *testing.T) {
	for _, variant := range []string{"duplicate-moov", "duplicate-mdia", "duplicate-hdlr", "truncated-hdlr", "missing-hdlr", "unknown-hdlr", "unsupported-hdlr-version", "mvex", "moof", "malformed-later-track"} {
		t.Run(variant, func(t *testing.T) {
			top, err := boxes(fixtureVideo(5000))
			if err != nil {
				t.Fatal(err)
			}
			movie := bytes.Clone(top[1].data)
			switch variant {
			case "duplicate-moov":
				data := append(codecVideo(t, "opus"), atom("moov", movie)...)
				got, err := Inspect(data, "video/mp4")
				if err != nil || got.AudioStreams != nil || got.AudioCodecs != nil {
					t.Fatal("ambiguous movies proved a stream inventory", got, err)
				}
				return
			case "duplicate-mdia":
				children, err := boxes(movie)
				if err != nil {
					t.Fatal(err)
				}
				movie = nil
				for _, child := range children {
					body := child.data
					if child.kind == "trak" {
						body = append(bytes.Clone(body), atom("mdia", nil)...)
					}
					movie = append(movie, atom(child.kind, body)...)
				}
			case "mvex":
				movie = append(movie, atom("mvex", nil)...)
			case "duplicate-hdlr", "truncated-hdlr", "missing-hdlr", "unknown-hdlr", "unsupported-hdlr-version":
				h := make([]byte, 12)
				copy(h[8:], "soun")
				metadata := atom("hdlr", h)
				if variant == "duplicate-hdlr" {
					metadata = append(metadata, atom("hdlr", h)...)
				}
				if variant == "truncated-hdlr" {
					metadata = atom("hdlr", []byte{0})
				}
				if variant == "missing-hdlr" {
					metadata = nil
				}
				if variant == "unknown-hdlr" {
					copy(h[8:], "????")
					metadata = atom("hdlr", h)
				}
				if variant == "unsupported-hdlr-version" {
					h[0] = 1
					metadata = atom("hdlr", h)
				}
				movie = append(movie, atom("trak", atom("mdia", metadata))...)
			case "malformed-later-track":
				movie = append(movie, atom("trak", []byte("bad"))...)
			}
			data := append(atom("ftyp", top[0].data), atom("moov", movie)...)
			if variant == "moof" {
				data = append(data, atom("moof", nil)...)
			}
			got, err := Inspect(data, "video/mp4")
			if err != nil || got.FPS != 30 || got.Duration != 5 {
				t.Fatal("generic primary-video acceptance changed", got, err)
			}
			if got.AudioStreams != nil || got.AudioCodecs != nil {
				t.Fatal("ambiguous track inventory became known", got)
			}
		})
	}
}
