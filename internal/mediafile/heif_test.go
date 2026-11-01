package mediafile

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// These synthetic containers exercise metadata validation, not pixel decoding.
// A real independently encoded fixture is tested separately when supplied.
type heifTestBits struct{ bits []byte }

func (w *heifTestBits) put(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		w.bits = append(w.bits, byte(v>>uint(i)&1))
	}
}
func (w *heifTestBits) ue(v uint64) {
	v++
	n := 0
	for x := v; x > 1; x >>= 1 {
		n++
	}
	w.put(0, n)
	w.put(v, n+1)
}
func (w *heifTestBits) bytes() []byte {
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
func heifTestConfig(width, height int) []byte {
	w := heifTestBits{}
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
func heifTestFixture(width, height int, rotation byte, idat, wide bool) []byte {
	ftyp := atom("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))
	sample := []byte{0, 0, 0, 4, 0x26, 1, 0xb0, 0x80}
	buildMeta := func(offset uint32) []byte {
		handler := make([]byte, 24)
		copy(handler[8:], "pict")
		infe := []byte{2, 0, 0, 0, 0, 1, 0, 0, 'h', 'v', 'c', '1', 0}
		iinf := append([]byte{0, 0, 0, 0, 0, 1}, atom("infe", infe)...)
		ispe := make([]byte, 12)
		binary.BigEndian.PutUint32(ispe[4:], uint32(width))
		binary.BigEndian.PutUint32(ispe[8:], uint32(height))
		props := append(atom("ispe", ispe), atom("hvcC", heifTestConfig(width, height))...)
		props = append(props, atom("irot", []byte{rotation})...)
		props = append(props, atom("pixi", []byte{0, 0, 0, 0, 3, 8, 8, 8})...)
		ipma := []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 4, 0x81, 0x82, 0x83, 0x84}
		iprp := append(atom("ipco", props), atom("ipma", ipma)...)
		loc := []byte{0, 0, 0, 0, 0x44, 0, 0, 1, 0, 1}
		if idat {
			loc[0] = 1
			loc = append(loc, 0, 1)
		}
		loc = append(loc, 0, 0, 0, 1)
		loc = binary.BigEndian.AppendUint32(loc, offset)
		loc = binary.BigEndian.AppendUint32(loc, uint32(len(sample)))
		meta := append([]byte{0, 0, 0, 0}, atom("hdlr", handler)...)
		meta = append(meta, atom("pitm", []byte{0, 0, 0, 0, 0, 1})...)
		meta = append(meta, atom("iinf", iinf)...)
		meta = append(meta, atom("iprp", iprp)...)
		meta = append(meta, atom("iloc", loc)...)
		if idat {
			meta = append(meta, atom("idat", sample)...)
		}
		return atom("meta", meta)
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
	return append(out, atom("mdat", sample)...)
}
func TestHEIFStillMetadataAndActualDimensions(t *testing.T) {
	for _, tc := range []struct {
		rotation      byte
		idat, wide    bool
		width, height int
	}{{0, false, false, 640, 480}, {1, false, true, 480, 640}, {2, true, false, 640, 480}, {3, true, false, 480, 640}} {
		data := heifTestFixture(640, 480, tc.rotation, tc.idat, tc.wide)
		for _, typ := range []string{"image/heic", "image/heif"} {
			info, err := Inspect(data, typ)
			if err != nil || info.Width != tc.width || info.Height != tc.height || info.Codec != "hevc" || info.ContentType != "image/heic" || info.Size != int64(len(data)) || info.AlphaChannel {
				t.Fatal(tc, info, err)
			}
		}
	}
}
func TestHEIFRejectsUntrustedOrUnsupportedMetadata(t *testing.T) {
	base := heifTestFixture(640, 480, 0, false, false)
	mutate := func(f func([]byte)) []byte { b := bytes.Clone(base); f(b); return b }
	cases := map[string][]byte{
		"brand only":              atom("ftyp", []byte("heic0000mif1heic")),
		"truncated":               base[:len(base)-1],
		"wrong primary type":      mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("hvc1")):], "grid") }),
		"wrong handler":           mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("pict")):], "vide") }),
		"wrong dimensions":        mutate(func(b []byte) { p := bytes.Index(b, []byte("ispe")); binary.BigEndian.PutUint32(b[p+8:], 320) }),
		"zero dimensions":         mutate(func(b []byte) { p := bytes.Index(b, []byte("ispe")); binary.BigEndian.PutUint32(b[p+8:], 0) }),
		"oversized dimensions":    heifTestFixture(100001, 480, 0, false, false),
		"unrecognized transform":  mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("irot")):], "clap") }),
		"auxiliary image":         mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("irot")):], "auxC") }),
		"bad rotation":            mutate(func(b []byte) { b[bytes.Index(b, []byte("irot"))+4] = 4 }),
		"encrypted item":          mutate(func(b []byte) { b[bytes.Index(b, []byte("infe"))+11] = 1 }),
		"external extent":         mutate(func(b []byte) { b[bytes.Index(b, []byte("iloc"))+15] = 1 }),
		"extent outside media":    mutate(func(b []byte) { p := bytes.Index(b, []byte("iloc")); binary.BigEndian.PutUint32(b[p+20:], 0) }),
		"extent length overflow":  mutate(func(b []byte) { p := bytes.Index(b, []byte("iloc")); binary.BigEndian.PutUint32(b[p+24:], 0xffffffff) }),
		"missing codec config":    mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("hvcC")):], "free") }),
		"invalid codec config":    mutate(func(b []byte) { b[bytes.Index(b, []byte("hvcC"))+4] = 0 }),
		"missing parameter sets":  mutate(func(b []byte) { b[bytes.Index(b, []byte("hvcC"))+26] = 0 }),
		"not independent picture": mutate(func(b []byte) { b[len(b)-4] = 0x02 }),
		"truncated NAL":           mutate(func(b []byte) { b[len(b)-5] = 0x7f }),
		"sequence brand":          mutate(func(b []byte) { copy(b[8:], "hevc") }),
		"AVIF brand":              mutate(func(b []byte) { copy(b[8:], "avif") }),
		"box size overflow":       mutate(func(b []byte) { binary.BigEndian.PutUint32(b[:4], 0xffffffff) }),
		"duplicate primary box": func() []byte {
			b := bytes.Clone(base)
			meta := bytes.Index(b, []byte("meta")) - 4
			size := int(binary.BigEndian.Uint32(b[meta:]))
			extra := atom("pitm", []byte{0, 0, 0, 0, 0, 1})
			end := meta + size
			b = append(append(append([]byte{}, b[:end]...), extra...), b[end:]...)
			binary.BigEndian.PutUint32(b[meta:], uint32(size+len(extra)))
			loc := bytes.Index(b, []byte("iloc"))
			off := binary.BigEndian.Uint32(b[loc+20:])
			binary.BigEndian.PutUint32(b[loc+20:], off+uint32(len(extra)))
			return b
		}(),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Inspect(data, "image/heic"); err == nil {
				t.Fatal("accepted malformed or unsupported HEIF")
			}
		})
	}
	tooMany := bytes.Repeat(atom("free", nil), 4097)
	if _, err := heifParts(tooMany, 0); err == nil {
		t.Fatal("box count unbounded")
	}
	if _, err := Inspect(make([]byte, heifMaxBytes+1), "image/heic"); err == nil {
		t.Fatal("oversized HEIF accepted")
	}
	for _, typ := range []string{"image/png", "video/mp4", "audio/mp4", "image/heic-sequence"} {
		if _, err := Inspect(base, typ); err == nil {
			t.Fatal("accepted wrong type", typ)
		}
	}
}
func TestHEIFRealEncodedFixture(t *testing.T) {
	path := os.Getenv("GATEWAY_TEST_HEIC_FIXTURE")
	if path == "" {
		t.Skip("optional locally generated real HEIC fixture; synthetic validation remains unconditional")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(data, "image/heic")
	if err != nil || info.Width < 1 || info.Height < 1 {
		t.Fatal(info, err)
	}
	t.Logf("real HEIC metadata: %dx%d codec=%s bytes=%d", info.Width, info.Height, info.Codec, info.Size)
}
func FuzzHEIFMetadataNeverPanics(f *testing.F) {
	f.Add(heifTestFixture(640, 480, 0, false, false))
	f.Add(heifTestFixture(640, 480, 1, true, false))
	f.Add([]byte("not HEIF"))
	f.Add(heifTestGridFixture(0))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		info, err := Inspect(data, "image/heic")
		if err == nil && (info.Width < 1 || info.Width > 100000 || info.Height < 1 || info.Height > 100000 || info.Size != int64(len(data)) || info.Codec != "hevc") {
			t.Fatal("invalid accepted metadata", info)
		}
	})
}

func heifTestGridFixture(rotation byte) []byte {
	direct := heifTestFixture(640, 480, 0, false, false)
	top, _ := boxes(direct)
	children, _ := boxes(top[1].data[4:])
	find := func(all []box, kind string) []byte {
		for _, b := range all {
			if b.kind == kind {
				return b.data
			}
		}
		return nil
	}
	oldInfo := find(children, "iinf")
	tileInfo := bytes.Clone(oldInfo[14:])
	tileInfo[3] |= 1
	iinf := append([]byte{0, 0, 0, 0, 0, 2}, atom("infe", tileInfo)...)
	iinf = append(iinf, atom("infe", []byte{2, 0, 0, 0, 0, 2, 0, 0, 'g', 'r', 'i', 'd', 0})...)
	properties, _ := boxes(find(children, "iprp"))
	ispe := make([]byte, 12)
	binary.BigEndian.PutUint32(ispe[4:], 1279)
	binary.BigEndian.PutUint32(ispe[8:], 959)
	ipco := append(bytes.Clone(find(properties, "ipco")), atom("ispe", ispe)...)
	ipco = append(ipco, atom("irot", []byte{rotation})...)
	ipma := []byte{0, 0, 0, 0, 0, 0, 0, 2, 0, 1, 4, 0x81, 0x82, 0x83, 0x84, 0, 2, 3, 0x85, 0x86, 0x84}
	iprp := append(atom("ipco", ipco), atom("ipma", ipma)...)
	iref := append([]byte{0, 0, 0, 0}, atom("dimg", []byte{0, 2, 0, 4, 0, 1, 0, 1, 0, 1, 0, 1})...)
	grid := []byte{0, 0, 1, 1, 4, 255, 3, 191}
	sample := top[2].data
	ftyp := atom("ftyp", top[0].data)
	buildMeta := func(offset uint32) []byte {
		loc := []byte{0, 0, 0, 0, 0x44, 0, 0, 2}
		for _, entry := range []struct {
			id             uint16
			offset, length uint32
		}{{1, offset, uint32(len(sample))}, {2, offset + uint32(len(sample)), uint32(len(grid))}} {
			loc = binary.BigEndian.AppendUint16(loc, entry.id)
			loc = append(loc, 0, 0, 0, 1)
			loc = binary.BigEndian.AppendUint32(loc, entry.offset)
			loc = binary.BigEndian.AppendUint32(loc, entry.length)
		}
		meta := append([]byte{0, 0, 0, 0}, atom("hdlr", find(children, "hdlr"))...)
		meta = append(meta, atom("pitm", []byte{0, 0, 0, 0, 0, 2})...)
		meta = append(meta, atom("iinf", iinf)...)
		meta = append(meta, atom("iprp", iprp)...)
		meta = append(meta, atom("iloc", loc)...)
		meta = append(meta, atom("iref", iref)...)
		return atom("meta", meta)
	}
	meta := buildMeta(0)
	meta = buildMeta(uint32(len(ftyp) + len(meta) + 8))
	return append(append(ftyp, meta...), atom("mdat", append(bytes.Clone(sample), grid...))...)
}
func TestHEIFGridMetadataRequiresValidatedTilesAndCoverage(t *testing.T) {
	for rotation := byte(0); rotation < 4; rotation++ {
		info, err := Inspect(heifTestGridFixture(rotation), "image/heif")
		w, h := 1279, 959
		if rotation%2 == 1 {
			w, h = h, w
		}
		if err != nil || info.Width != w || info.Height != h {
			t.Fatal(rotation, info, err)
		}
	}
	base := heifTestGridFixture(0)
	mutate := func(f func([]byte)) []byte { b := bytes.Clone(base); f(b); return b }
	cases := map[string][]byte{
		"missing derivation":                    mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("dimg")):], "thmb") }),
		"nested or cyclic grid":                 mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("hvc1")):], "grid") }),
		"missing tile":                          mutate(func(b []byte) { p := bytes.Index(b, []byte("dimg")); b[p+9] = 99 }),
		"self reference":                        mutate(func(b []byte) { p := bytes.Index(b, []byte("dimg")); b[p+9] = 2 }),
		"wrong tile count":                      mutate(func(b []byte) { p := bytes.Index(b, []byte("dimg")); b[p+7] = 3 }),
		"too many grid cells":                   mutate(func(b []byte) { b[len(b)-6] = 255; b[len(b)-5] = 255 }),
		"grid output disagrees with properties": mutate(func(b []byte) { b[len(b)-1]-- }),
		"uncovered output": mutate(func(b []byte) {
			n := len(b)
			binary.BigEndian.PutUint16(b[n-4:], 1281)
			p := bytes.LastIndex(b, []byte("ispe"))
			binary.BigEndian.PutUint32(b[p+8:], 1281)
		}),
		"empty final column": mutate(func(b []byte) {
			n := len(b)
			binary.BigEndian.PutUint16(b[n-4:], 640)
			p := bytes.LastIndex(b, []byte("ispe"))
			binary.BigEndian.PutUint32(b[p+8:], 640)
		}),
		"invalid tile codec":       mutate(func(b []byte) { b[bytes.Index(b, []byte("hvcC"))+4] = 0 }),
		"invalid tile sample":      mutate(func(b []byte) { b[len(b)-12] = 0x02 }),
		"unknown tile transform":   mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("irot")):], "clap") }),
		"auxiliary relationship":   mutate(func(b []byte) { copy(b[bytes.Index(b, []byte("dimg")):], "auxl") }),
		"unsupported grid version": mutate(func(b []byte) { b[len(b)-8] = 1 }),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Inspect(data, "image/heic"); err == nil {
				t.Fatal("accepted malformed grid")
			}
		})
	}
}

func TestHEIFConfigurationChromaMustMatchSPS(t *testing.T) {
	for _, grid := range []bool{false, true} {
		for _, chroma := range []byte{0, 1, 2, 3} {
			data := heifTestFixture(640, 480, 0, false, false)
			if grid {
				data = heifTestGridFixture(0)
			}
			offset := bytes.Index(data, []byte("hvcC")) + 4 + 16
			data[offset] = (data[offset] & 0xfc) | chroma
			_, err := Inspect(data, "image/heic")
			if (err == nil) != (chroma == 1) {
				t.Fatalf("grid=%v hvcC_chroma=%d SPS_chroma=1: %v", grid, chroma, err)
			}
		}
	}
}
