package api

import (
	"bytes"
	"encoding/binary"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

// Self-contained sample tables prove decoder configuration and actual MP3
// frames through the shared inspector; no claimed provider metadata is used.
func templateCodecDescriptor(tag byte, data []byte) []byte {
	return append([]byte{tag, byte(len(data))}, data...)
}
func templateCodecTrack(codec string, offset uint32) ([]byte, []byte) {
	rate := uint32(48000)
	oti := byte(0x40)
	config := []byte{0x11, 0x90}
	sample := []byte{0x21, 0x10}
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
		decoder = append(decoder, templateCodecDescriptor(5, config)...)
	}
	es := append([]byte{0, 1, 0}, templateCodecDescriptor(4, decoder)...)
	es = append(es, templateCodecDescriptor(6, []byte{2})...)
	entry = append(entry, testmedia.Atom("esds", append(make([]byte, 4), templateCodecDescriptor(3, es)...))...)
	kind := "mp4a"
	if codec == "encrypted" {
		kind = "enca"
	}
	if codec == "opus" {
		kind = "Opus"
	}
	stsd := make([]byte, 8)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	stsd = append(stsd, testmedia.Atom(kind, entry)...)
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
	stbl := append(testmedia.Atom("stsd", stsd), testmedia.Atom("stts", stts)...)
	stbl = append(stbl, testmedia.Atom("stsz", stsz)...)
	stbl = append(stbl, testmedia.Atom("stsc", stsc)...)
	stbl = append(stbl, testmedia.Atom("stco", stco)...)
	dref := make([]byte, 8)
	binary.BigEndian.PutUint32(dref[4:], 1)
	dref = append(dref, testmedia.Atom("url ", []byte{0, 0, 0, 1})...)
	minf := append(testmedia.Atom("dinf", testmedia.Atom("dref", dref)), testmedia.Atom("stbl", stbl)...)
	hdlr := make([]byte, 24)
	copy(hdlr[8:], "soun")
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], rate)
	binary.BigEndian.PutUint32(mdhd[16:], 3072)
	mdia := append(testmedia.Atom("hdlr", hdlr), testmedia.Atom("mdhd", mdhd)...)
	mdia = append(mdia, testmedia.Atom("minf", minf)...)
	track := append(testmedia.Atom("tkhd", make([]byte, 84)), testmedia.Atom("mdia", mdia)...)
	return testmedia.Atom("trak", track), bytes.Repeat(sample, 3)
}

func templateCodecVideo(codec string) []byte {
	silent := templateVideo()
	ftypSize := int(binary.BigEndian.Uint32(silent[:4]))
	ftyp, movie := silent[:ftypSize], bytes.Clone(silent[ftypSize+8:])
	if codec == "silent" {
		return silent
	}
	track, payload := templateCodecTrack(codec, uint32(len(ftyp)+8))
	movie = append(movie, track...)
	data := append(bytes.Clone(ftyp), testmedia.Atom("mdat", payload)...)
	data = append(data, testmedia.Atom("moov", movie)...)
	if codec == "ambiguous" {
		data = append(data, testmedia.Atom("moov", silent[ftypSize+8:])...)
	}
	return data
}

func TestFixedTemplatePreservesInspectedAudioCodecEvidence(t *testing.T) {
	for _, codec := range []string{"silent", "aac", "mp3", "opus", "ambiguous"} {
		t.Run(codec, func(t *testing.T) {
			s, repo, id := metadataFixture(t)
			data := templateCodecVideo(codec)
			inspected, err := mediafile.Inspect(data, "video/mp4")
			if err != nil {
				t.Fatal(err)
			}
			fixed := templateCopy(t, s, id, data)
			record := repo.items[id]
			record.Draft.Slots[2].AssetID = fixed.ID
			published := record.Draft
			record.Published = &published
			repo.items[id] = record
			ctx := gw253Context("alice")
			ref, err := s.references.Upload(ctx, "image.png", bytes.NewReader(referencePNG(t)))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "https://gateway.example.test/v1/templates/"+id+"/generations", nil).WithContext(ctx)
			input := templateGenerationInput{Revision: 1, Provider: "mock", Model: "dance", Inputs: map[string]string{"image:1": ref.ID, "image:2": ref.ID}}
			resolved, err := s.resolveTemplate(request, record, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.Options.VideoURLs) != 1 {
				t.Fatal("fixed video missing")
			}
			proof := resolved.ReferenceEvidence[resolved.Options.VideoURLs[0]]
			if !reflect.DeepEqual(proof.AudioStreams, inspected.AudioStreams) || !reflect.DeepEqual(proof.VideoStreams, inspected.VideoStreams) ||
				!reflect.DeepEqual(proof.AudioCodecs, inspected.AudioCodecs) || proof.FrameCount != inspected.FrameCount || proof.FrameCount != 150 {
				t.Fatal("fixed source inspection was dropped", proof, inspected)
			}
			if codec != "ambiguous" {
				wantStreams := 1
				if codec == "silent" {
					wantStreams = 0
				}
				if proof.AudioStreams == nil || *proof.AudioStreams != wantStreams {
					t.Fatal("actual audio stream count changed", proof)
				}
			}
			accepted := codec == "silent" || codec == "aac" || codec == "mp3"
			if core.AudioCodecsAllowed(proof.AudioStreams, proof.AudioCodecs, []string{"aac", "mp3"}) != accepted {
				t.Fatal("constraint evidence changed", proof)
			}
			if codec == "ambiguous" && (proof.AudioStreams != nil || proof.AudioCodecs != nil) {
				t.Fatal("ambiguous inventory became proven silence", proof)
			}
			service := s.service.(*openAIServiceFixture)
			service.catalog[0].Models[0].ReferenceMediaLimits.Video.AllowedAudioCodecs = []string{"aac", "mp3"}
			_, err = s.resolveTemplate(request, record, input)
			if (err == nil) != accepted || service.calls != 0 {
				t.Fatal("constrained fixed source admission changed or reached provider", codec, err, service.calls)
			}
		})
	}
}
