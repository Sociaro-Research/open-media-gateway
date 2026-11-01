package mediafile

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
)

func atom(kind string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)+8))
	b.WriteString(kind)
	b.Write(data)
	return b.Bytes()
}

func TestPNGAlphaChannelIsPartOfInspectedMetadata(t *testing.T) {
	for _, tc := range []struct {
		image image.Image
		alpha bool
	}{
		{image.NewNRGBA(image.Rect(0, 0, 400, 400)), true},
		{image.NewGray(image.Rect(0, 0, 400, 400)), false},
	} {
		var data bytes.Buffer
		if err := png.Encode(&data, tc.image); err != nil {
			t.Fatal(err)
		}
		info, err := Inspect(data.Bytes(), "image/png")
		if err != nil || info.AlphaChannel != tc.alpha {
			t.Fatal(info, err)
		}
	}
}
func fixtureVideo(duration uint32) []byte {
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[76:], 640<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 360<<16)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], 30000)
	binary.BigEndian.PutUint32(mdhd[16:], duration*30)
	hdlr := make([]byte, 12)
	copy(hdlr[8:], "vide")
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:], 1)
	binary.BigEndian.PutUint32(stts[8:], duration*30/1000)
	binary.BigEndian.PutUint32(stts[12:], 1000)
	mdia := append(atom("mdhd", mdhd), atom("hdlr", hdlr)...)
	mdia = append(mdia, atom("minf", atom("stbl", atom("stts", stts)))...)
	track := append(atom("tkhd", tkhd), atom("mdia", mdia)...)
	return append(atom("ftyp", []byte("isom0000isom")), atom("moov", atom("trak", track))...)
}
func TestInspectActualDimensionsDurationAndCorruption(t *testing.T) {
	var p bytes.Buffer
	_ = png.Encode(&p, image.NewRGBA(image.Rect(0, 0, 320, 256)))
	v, e := Inspect(p.Bytes(), "image/png")
	if e != nil || v.Width != 320 || v.Height != 256 {
		t.Fatal(v, e)
	}
	v, e = Inspect(fixtureVideo(5000), "video/mp4")
	if e != nil || v.Width != 640 || v.Height != 360 || v.Duration != 5 || v.FPS != 30 {
		t.Fatal(v, e)
	}
	for _, data := range [][]byte{nil, []byte("fake video"), fixtureVideo(0), fixtureVideo(5000)[:25], append([]byte{255, 255, 255, 255}, fixtureVideo(5000)[4:]...)} {
		if _, e = Inspect(data, "video/mp4"); e == nil {
			t.Fatal("corrupt video accepted")
		}
	}
	if _, e = Inspect([]byte("<svg><script>1</script></svg>"), "image/png"); e == nil {
		t.Fatal("markup accepted")
	}
}
func FuzzInspectNeverPanics(f *testing.F) {
	f.Add(fixtureVideo(5000), "video/mp4")
	f.Add(webmFixture(), "video/webm")
	f.Add(oggFixture(), "audio/ogg")
	f.Add(flacFixture(), "audio/flac")
	f.Add([]byte("RIFF"), "image/webp")
	f.Fuzz(func(t *testing.T, data []byte, kind string) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = Inspect(data, kind)
	})
}
