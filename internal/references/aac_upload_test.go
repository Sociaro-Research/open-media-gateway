package references

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func aacUploadDescriptor(tag byte, data []byte) []byte {
	return append([]byte{tag, byte(len(data))}, data...)
}
func aacUploadM4A(version byte, wide, variable bool) []byte {
	ftyp := testmedia.Atom("ftyp", []byte{'M', '4', 'A', ' ', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'})
	payload := []byte{0x21, 0x10, 0x21, 0x10, 0x21, 0x10}
	mdat := testmedia.Atom("mdat", payload)
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
	decoder = append(decoder, aacUploadDescriptor(5, []byte{0x11, 0x90, 0x56, 0xe5, 0})...)
	es := append([]byte{0, 1, 0}, aacUploadDescriptor(4, decoder)...)
	es = append(es, aacUploadDescriptor(6, []byte{2})...)
	esds := append(make([]byte, 4), aacUploadDescriptor(3, es)...)
	entry = append(entry, testmedia.Atom("esds", esds)...)
	stsd := make([]byte, 8)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	stsd = append(stsd, testmedia.Atom("mp4a", entry)...)
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
	stbl := append(testmedia.Atom("stsd", stsd), testmedia.Atom("stts", stts)...)
	stbl = append(stbl, testmedia.Atom("stsz", stsz)...)
	stbl = append(stbl, testmedia.Atom("stsc", stsc)...)
	stbl = append(stbl, testmedia.Atom(offsetKind, offsets)...)
	dref := make([]byte, 8)
	binary.BigEndian.PutUint32(dref[4:], 1)
	dref = append(dref, testmedia.Atom("url ", []byte{0, 0, 0, 1})...)
	minf := append(testmedia.Atom("smhd", make([]byte, 8)), testmedia.Atom("dinf", testmedia.Atom("dref", dref))...)
	minf = append(minf, testmedia.Atom("stbl", stbl)...)
	mdia := append(testmedia.Atom("mdhd", mdhd), testmedia.Atom("hdlr", hdlr)...)
	mdia = append(mdia, testmedia.Atom("minf", minf)...)
	trak := append(testmedia.Atom("tkhd", make([]byte, 84)), testmedia.Atom("mdia", mdia)...)
	return append(append(ftyp, mdat...), testmedia.Atom("moov", testmedia.Atom("trak", trak))...)
}

func aacUploadADTS() []byte {
	return bytes.Repeat([]byte{0xff, 0xf1, 0x4c, 0x80, 0x01, 0x7f, 0xfc, 0x21, 0x10, 0x04, 0x60}, 100)
}

type aacUploadRepository struct{ item Image }

func (r *aacUploadRepository) List(context.Context, int, int) ([]Image, error) { return nil, nil }
func (r *aacUploadRepository) Get(_ context.Context, id string) (Image, error) {
	if id != r.item.ID {
		return Image{}, storage.ErrObjectNotFound
	}
	return r.item, nil
}
func (r *aacUploadRepository) Insert(_ context.Context, item Image) error { r.item = item; return nil }
func (r *aacUploadRepository) Delete(context.Context, string) error       { return nil }
func (r *aacUploadRepository) FindByHash(_ context.Context, hash string) (Image, error) {
	if r.item.SHA256 == hash {
		return r.item, nil
	}
	return Image{}, storage.ErrObjectNotFound
}
func (r *aacUploadRepository) UnhashedBySize(context.Context, int64) ([]Image, error) {
	return nil, nil
}
func (r *aacUploadRepository) SetHash(context.Context, string, string) error { return nil }

type aacUploadReadCloser struct{ *bytes.Reader }

func (r aacUploadReadCloser) Close() error { return nil }

type aacUploadStore struct {
	data []byte
	kind string
}

func (s *aacUploadStore) StoreReference(_ context.Context, id string, data []byte, kind string) (core.StoredMedia, error) {
	s.data = append([]byte{}, data...)
	s.kind = kind
	return core.StoredMedia{StorageKey: "fixture/" + id, ContentType: kind, SizeBytes: int64(len(data))}, nil
}
func (s *aacUploadStore) DeleteReference(context.Context, string) error { return nil }
func (s *aacUploadStore) Open(context.Context, string) (storage.Object, error) {
	return storage.Object{Reader: aacUploadReadCloser{bytes.NewReader(s.data)}, SizeBytes: int64(len(s.data)), ContentType: s.kind}, nil
}

func TestAACUploadsInspectActualBytesAndMIMEAliases(t *testing.T) {
	for _, tc := range []struct {
		name, canonical string
		data            []byte
		aliases         []string
	}{
		{"synthetic.aac", "audio/aac", aacUploadADTS(), []string{"audio/aac", "audio/x-aac"}},
		{"synthetic.m4a", "audio/mp4", aacUploadM4A(0, false, false), []string{"audio/mp4", "audio/x-m4a", "audio/m4a"}},
		{"synthetic-isom.m4a", "audio/mp4", func() []byte { b := aacUploadM4A(1, true, true); copy(b[8:12], "isom"); return b }(), []string{"audio/mp4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, alias := range tc.aliases {
				file, err := InspectFile("misleading.jpg", tc.data, alias)
				if err != nil || file.Type != "audio" || file.Info.ContentType != tc.canonical || file.Info.Codec != "aac" || file.Info.Duration <= 0 {
					t.Fatal(alias, file, err)
				}
				sum := sha256.Sum256(tc.data)
				plan, err := New(nil, nil).BeginUpload(context.Background(), UploadInput{Name: tc.name, ContentType: alias, SizeBytes: int64(len(tc.data)), SHA256: hex.EncodeToString(sum[:])})
				if err != nil || plan.Mode != "local" {
					t.Fatal(alias, plan, err)
				}
			}
			repo, store := &aacUploadRepository{}, &aacUploadStore{}
			library := New(repo, store)
			ctx := identity.WithOwnerID(context.Background(), "aac-fixture-owner")
			item, err := library.Upload(ctx, tc.name, bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			if item.ContentType != tc.canonical || store.kind != tc.canonical || !bytes.Equal(store.data, tc.data) || repo.item.ID != item.ID {
				t.Fatal(item, store.kind)
			}
			inspected, err := library.Inspect(ctx, item.ID)
			if err != nil || inspected.Info.ContentType != tc.canonical || inspected.Type != "audio" {
				t.Fatal(inspected, err)
			}
		})
	}
}

func TestAACUploadsRejectCorruptionAndWrongDeclaredTypes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     []byte
		declared string
	}{
		{"truncated ADTS", aacUploadADTS()[:1099], "audio/aac"},
		{"ADTS sync prefix only", []byte{0xff, 0xf1, 0x4c, 0x80, 0x01, 0x7f, 0xfc}, "audio/aac"},
		{"truncated M4A", func() []byte { b := aacUploadM4A(0, false, false); return b[:len(b)-1] }(), "audio/mp4"},
		{"audio-only claimed video", aacUploadM4A(0, false, false), "video/mp4"},
		{"video claimed audio", testmedia.MP4(5000, 1000, 150), "audio/mp4"},
		{"AAC claimed MP3", aacUploadADTS(), "audio/mpeg"},
		{"brand only M4A", testmedia.Atom("ftyp", []byte("M4A 0000isom")), "audio/mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := InspectFile(tc.name, tc.data, tc.declared); !errors.Is(err, ErrInvalidImage) {
				t.Fatal("accepted invalid upload", err)
			}
		})
	}
	repo, store := &aacUploadRepository{}, &aacUploadStore{}
	if _, err := New(repo, store).Upload(context.Background(), "corrupt.aac", bytes.NewReader(aacUploadADTS()[:1099])); err == nil || len(store.data) > 0 || repo.item.ID != "" {
		t.Fatal("corrupt bytes reached storage", err)
	}
	for _, kind := range []string{"audio/mp4", "audio/aac"} {
		if commonLimit(kind) != 50<<20 {
			t.Fatal("audio limit changed", kind, commonLimit(kind))
		}
		if _, err := New(nil, nil).BeginUpload(context.Background(), UploadInput{Name: "large", ContentType: kind, SizeBytes: 50<<20 + 1, SHA256: strings.Repeat("a", 64)}); err == nil {
			t.Fatal("oversized upload accepted")
		}
	}
	if MaxBytes != 200<<20 || commonLimit("video/mp4") != 200<<20 || commonLimit("image/png") != 30<<20 {
		t.Fatal("unrelated upload limits changed")
	}
}
