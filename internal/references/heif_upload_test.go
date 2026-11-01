package references

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"os"
	"testing"
)

// Synthetic container/configuration fixtures carry no generated image pixels.
type heifUploadBits struct{ bits []byte }

func (w *heifUploadBits) put(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		w.bits = append(w.bits, byte(v>>uint(i)&1))
	}
}
func (w *heifUploadBits) ue(v uint64) {
	v++
	n := 0
	for x := v; x > 1; x >>= 1 {
		n++
	}
	w.put(0, n)
	w.put(v, n+1)
}
func (w *heifUploadBits) bytes() []byte {
	w.put(1, 1)
	for len(w.bits)%8 != 0 {
		w.put(0, 1)
	}
	raw := make([]byte, len(w.bits)/8)
	for i, b := range w.bits {
		raw[i/8] |= b << uint(7-i%8)
	}
	var out []byte
	zeros := 0
	for _, b := range raw {
		if zeros == 2 && b <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}
func heifUploadConfig(width, height int) []byte {
	w := heifUploadBits{}
	w.put(0, 4)
	w.put(0, 3)
	w.put(1, 1)
	w.put(1, 8)
	w.put(0, 64)
	w.put(0, 24)
	w.ue(0)
	w.ue(1)
	w.ue(uint64(width))
	w.ue(uint64(height))
	w.put(0, 1)
	w.ue(0)
	w.ue(0)
	sps := append([]byte{0x42, 1}, w.bytes()...)
	config := make([]byte, 23)
	config[0] = 1
	config[1] = 1
	config[13] = 0xf0
	config[15] = 0xfc
	config[16] = 0xfd
	config[17] = 0xf8
	config[18] = 0xf8
	config[21] = 3
	config[22] = 3
	for _, n := range [][]byte{{0x40, 1, 0x08}, sps, {0x44, 1, 0xe0}} {
		config = append(config, 0x80|n[0]>>1, 0, 1, byte(len(n)>>8), byte(len(n)))
		config = append(config, n...)
	}
	return config
}
func heifUploadFixture(width, height int, rotation byte, idat, wide bool) []byte {
	ftyp := testmedia.Atom("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))
	sample := []byte{0, 0, 0, 4, 0x26, 1, 0xb0, 0x80}
	buildMeta := func(offset uint32) []byte {
		handler := make([]byte, 24)
		copy(handler[8:], "pict")
		infe := []byte{2, 0, 0, 0, 0, 1, 0, 0, 'h', 'v', 'c', '1', 0}
		iinf := append([]byte{0, 0, 0, 0, 0, 1}, testmedia.Atom("infe", infe)...)
		ispe := make([]byte, 12)
		binary.BigEndian.PutUint32(ispe[4:], uint32(width))
		binary.BigEndian.PutUint32(ispe[8:], uint32(height))
		props := append(testmedia.Atom("ispe", ispe), testmedia.Atom("hvcC", heifUploadConfig(width, height))...)
		props = append(props, testmedia.Atom("irot", []byte{rotation})...)
		props = append(props, testmedia.Atom("pixi", []byte{0, 0, 0, 0, 3, 8, 8, 8})...)
		ipma := []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 4, 0x81, 0x82, 0x83, 0x84}
		iprp := append(testmedia.Atom("ipco", props), testmedia.Atom("ipma", ipma)...)
		loc := []byte{0, 0, 0, 0, 0x44, 0, 0, 1, 0, 1}
		if idat {
			loc[0] = 1
			loc = append(loc, 0, 1)
		}
		loc = append(loc, 0, 0, 0, 1)
		loc = binary.BigEndian.AppendUint32(loc, offset)
		loc = binary.BigEndian.AppendUint32(loc, uint32(len(sample)))
		meta := append([]byte{0, 0, 0, 0}, testmedia.Atom("hdlr", handler)...)
		meta = append(meta, testmedia.Atom("pitm", []byte{0, 0, 0, 0, 0, 1})...)
		meta = append(meta, testmedia.Atom("iinf", iinf)...)
		meta = append(meta, testmedia.Atom("iprp", iprp)...)
		meta = append(meta, testmedia.Atom("iloc", loc)...)
		if idat {
			meta = append(meta, testmedia.Atom("idat", sample)...)
		}
		return testmedia.Atom("meta", meta)
	}
	meta := buildMeta(0)
	if idat {
		return append(ftyp, meta...)
	}
	head := 8
	if wide {
		head = 16
	}
	meta = buildMeta(uint32(len(ftyp) + len(meta) + head))
	out := append(ftyp, meta...)
	if wide {
		mdat := []byte{0, 0, 0, 1, 'm', 'd', 'a', 't'}
		mdat = binary.BigEndian.AppendUint64(mdat, uint64(len(sample)+16))
		return append(append(out, mdat...), sample...)
	}
	return append(out, testmedia.Atom("mdat", sample)...)
}

func TestHEIFUploadsRetainInspectedBytesAndReinspect(t *testing.T) {
	data := heifUploadFixture(640, 512, 0, false, true)
	sum := sha256.Sum256(data)
	for _, alias := range []string{"image/heic", "image/heif"} {
		file, err := InspectFile("misleading.mp4", data, alias)
		if err != nil || file.Type != "image" || file.Info.ContentType != "image/heic" || file.Info.Width != 640 || file.Info.Height != 512 {
			t.Fatal(alias, file, err)
		}
		if commonLimit(alias) != 30<<20 {
			t.Fatal("HEIF limit changed", alias, commonLimit(alias))
		}
		plan, err := New(nil, nil).BeginUpload(context.Background(), UploadInput{Name: "synthetic.heif", ContentType: alias, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
		if err != nil || plan.Mode != "local" {
			t.Fatal(plan, err)
		}
		if _, err := New(nil, nil).BeginUpload(context.Background(), UploadInput{Name: "too-large.heif", ContentType: alias, SizeBytes: 30<<20 + 1, SHA256: hex.EncodeToString(sum[:])}); err == nil {
			t.Fatal("oversized HEIF plan accepted")
		}
	}
	repo, store := &aacUploadRepository{}, &aacUploadStore{}
	library := New(repo, store)
	ctx := identity.WithOwnerID(context.Background(), "heif-fixture-owner")
	item, err := library.Upload(ctx, "valid.heif", bytes.NewReader(data))
	if err != nil || item.ContentType != "image/heic" || store.kind != "image/heic" || !bytes.Equal(store.data, data) || item.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal(item, err)
	}
	inspected, err := library.Inspect(ctx, item.ID)
	if err != nil || inspected.Type != "image" || inspected.Info.Width != 640 || inspected.Info.Height != 512 || inspected.SHA256 != item.SHA256 {
		t.Fatal(inspected, err)
	}
	model := core.MediaModel{ReferenceMediaLimits: &core.ReferenceMediaLimits{Image: core.MediaFileLimits{ContentTypes: []string{"image/heif"}, MinDimension: 512, MaxBytes: 20 << 20}}}
	if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{inspected}, false); err != nil {
		t.Fatal("valid HEIF failed model limits", err)
	}
	model.ReferenceMediaLimits.Image.MinDimension = 513
	if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{inspected}, false); err == nil {
		t.Fatal("HEIF bypassed model dimension minimum")
	}
	model.ReferenceMediaLimits.Image.MinDimension = 512
	model.ReferenceMediaLimits.Image.MaxBytes = int64(len(data) - 1)
	if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{inspected}, false); err == nil {
		t.Fatal("HEIF bypassed model byte limit")
	}
}
func TestHEIFUploadsRejectWrongTypeCorruptionAndUnsupportedConstructs(t *testing.T) {
	base := heifUploadFixture(640, 512, 0, false, false)
	for name, data := range map[string][]byte{"fake brand": testmedia.Atom("ftyp", []byte("heic0000mif1heic")), "truncated": base[:len(base)-1], "derived grid": func() []byte { b := bytes.Clone(base); copy(b[bytes.Index(b, []byte("hvc1")):], "grid"); return b }(), "oversized": append(bytes.Clone(base), make([]byte, 30<<20)...)} {
		t.Run(name, func(t *testing.T) {
			repo, store := &aacUploadRepository{}, &aacUploadStore{}
			if _, err := New(repo, store).Upload(context.Background(), "looks-valid.heic", bytes.NewReader(data)); !errors.Is(err, ErrInvalidImage) || len(store.data) != 0 || repo.item.ID != "" {
				t.Fatal("invalid bytes reached storage", err)
			}
		})
	}
	for _, declared := range []string{"image/png", "image/jpeg", "video/mp4", "image/heic-sequence"} {
		if _, err := InspectFile("misleading.heic", base, declared); !errors.Is(err, ErrInvalidImage) {
			t.Fatal("wrong declaration accepted", declared, err)
		}
	}
	if MaxBytes != 200<<20 || commonLimit("image/png") != 30<<20 || commonLimit("video/mp4") != 200<<20 || commonLimit("audio/mp4") != 50<<20 {
		t.Fatal("unrelated size limits changed")
	}
}
func TestHEIFRealUploadFixtureAndModelDimensions(t *testing.T) {
	path := os.Getenv("GATEWAY_TEST_HEIC_FIXTURE")
	if path == "" {
		t.Skip("optional independently encoded local HEIC fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := InspectFile("real.heif", data, "image/heif")
	if err != nil {
		t.Fatal(err)
	}
	model := core.MediaModel{ReferenceMediaLimits: &core.ReferenceMediaLimits{Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"}, MinDimension: 512, MaxBytes: 20 << 20}}}
	if err := referencevalidation.ValidateFiles(model, []referencevalidation.File{file}, false); err != nil {
		t.Fatal(file, err)
	}
	t.Logf("real HEIC passed inspected image constraints: %dx%d bytes=%d", file.Info.Width, file.Info.Height, file.Info.Size)
}
