package mediafile

import (
	"bytes"
	"encoding/binary"
)

// HEIF inspection deliberately supports still HEVC images, including one-level image grids.
// It validates container/item extents and HEVC configuration/dimension metadata;
// it does not decode pixels. Nested/other derived images, external data, encryption and
// unknown essential transforms require a decoder and remain unsupported.
const heifMaxBytes = 30 << 20
const heifMaxItems = 1024

type heifPart struct {
	kind   string
	data   []byte
	offset uint64
}

func heifParts(data []byte, base uint64) ([]heifPart, error) {
	var out []heifPart
	for pos := 0; pos < len(data); {
		if len(out) >= 4096 || len(data)-pos < 8 {
			return nil, ErrInvalid
		}
		n, head := uint64(binary.BigEndian.Uint32(data[pos:])), 8
		if n == 1 {
			if len(data)-pos < 16 {
				return nil, ErrInvalid
			}
			n, head = binary.BigEndian.Uint64(data[pos+8:]), 16
		}
		// Zero-size boxes and ambiguous trailing bytes are outside this subset.
		if n < uint64(head) || n > uint64(len(data)-pos) {
			return nil, ErrInvalid
		}
		out = append(out, heifPart{string(data[pos+4 : pos+8]), data[pos+head : pos+int(n)], base + uint64(pos+head)})
		pos += int(n)
	}
	return out, nil
}

func heifUnique(parts []heifPart, kind string) (heifPart, error) {
	var found heifPart
	for _, p := range parts {
		if p.kind == kind {
			if found.kind != "" {
				return found, ErrInvalid
			}
			found = p
		}
	}
	if found.kind == "" {
		return found, ErrInvalid
	}
	return found, nil
}

func heifFull(data []byte, maxVersion byte, flags uint32) bool {
	return len(data) >= 4 && data[0] <= maxVersion && uint32(data[1])<<16|uint32(data[2])<<8|uint32(data[3]) == flags
}

type heifCursor struct {
	data []byte
	pos  int
	bad  bool
}

func (c *heifCursor) read(n int) uint64 {
	if n < 0 || n > 8 || n > len(c.data)-c.pos {
		c.bad = true
		return 0
	}
	var v uint64
	for _, b := range c.data[c.pos : c.pos+n] {
		v = v<<8 | uint64(b)
	}
	c.pos += n
	return v
}
func (c *heifCursor) done() bool { return !c.bad && c.pos == len(c.data) }

type heifItem struct {
	kind   string
	hidden bool
	data   []byte
	props  []int
}

func inspectHEIF(data []byte, info *Info) error {
	if len(data) < 32 || len(data) > heifMaxBytes {
		return ErrInvalid
	}
	top, err := heifParts(data, 0)
	if err != nil || len(top) == 0 || top[0].kind != "ftyp" {
		return ErrInvalid
	}
	ftyp, err := heifUnique(top, "ftyp")
	if err != nil || len(ftyp.data) < 12 || len(ftyp.data)%4 != 0 {
		return ErrInvalid
	}
	brand := string(ftyp.data[:4])
	if brand != "heic" && brand != "heix" && brand != "mif1" {
		return ErrInvalid
	}
	compatible := false
	for p := 8; p < len(ftyp.data); p += 4 {
		switch string(ftyp.data[p : p+4]) {
		case "heic", "heix", "mif1":
			compatible = true
		case "hevc", "hevx", "msf1", "avis":
			return ErrInvalid
		}
	}
	if !compatible {
		return ErrInvalid
	}
	meta, err := heifUnique(top, "meta")
	if err != nil || !heifFull(meta.data, 0, 0) {
		return ErrInvalid
	}
	for _, p := range top {
		if p.kind == "moov" || p.kind == "moof" {
			return ErrInvalid
		}
	}
	parts, err := heifParts(meta.data[4:], meta.offset+4)
	if err != nil {
		return err
	}
	handler, err := heifUnique(parts, "hdlr")
	if err != nil || len(handler.data) < 24 || !heifFull(handler.data, 0, 0) || string(handler.data[8:12]) != "pict" {
		return ErrInvalid
	}
	for _, p := range parts {
		if p.kind == "ipro" {
			return ErrInvalid
		}
		if p.kind == "dinf" && !heifLocalDataReference(p) {
			return ErrInvalid
		}
	}
	primary, err := heifUnique(parts, "pitm")
	if err != nil || !heifFull(primary.data, 1, 0) {
		return ErrInvalid
	}
	c := heifCursor{data: primary.data, pos: 4}
	idSize := 2
	if primary.data[0] == 1 {
		idSize = 4
	}
	primaryID := uint32(c.read(idSize))
	if !c.done() || primaryID == 0 {
		return ErrInvalid
	}
	items, err := heifItems(parts)
	if err != nil {
		return err
	}
	item := items[primaryID]
	if item == nil || (item.kind != "hvc1" && item.kind != "grid") || item.hidden {
		return ErrInvalid
	}
	if err = heifLocations(parts, top, items, data); err != nil {
		return err
	}
	props, err := heifProperties(parts, items)
	if err != nil {
		return err
	}
	// Alpha/depth/overlay relationships are unsupported; they must not be
	// mistaken for a proven opaque primary. Grids can only reference direct
	// HEVC tiles, never other grids, so cycles/nesting are impossible.
	derived := map[uint32][]uint32{}
	irefs := 0
	for _, p := range parts {
		if p.kind != "iref" {
			continue
		}
		irefs++
		if irefs > 1 || !heifFull(p.data, 1, 0) {
			return ErrInvalid
		}
		refs, e := heifParts(p.data[4:], p.offset+4)
		if e != nil {
			return e
		}
		for _, ref := range refs {
			if ref.kind != "thmb" && ref.kind != "cdsc" && ref.kind != "dimg" {
				return ErrInvalid
			}
			r := heifCursor{data: ref.data}
			n := 2
			if p.data[0] == 1 {
				n = 4
			}
			from := uint32(r.read(n))
			count := r.read(2)
			if items[from] == nil || count == 0 || count > heifMaxItems {
				return ErrInvalid
			}
			if ref.kind == "dimg" && (items[from].kind != "grid" || derived[from] != nil) {
				return ErrInvalid
			}
			for i := uint64(0); i < count; i++ {
				to := uint32(r.read(n))
				if items[to] == nil || to == from {
					return ErrInvalid
				}
				if ref.kind == "dimg" {
					if items[to].kind != "hvc1" {
						return ErrInvalid
					}
					derived[from] = append(derived[from], to)
				}
			}
			if !r.done() {
				return ErrInvalid
			}
		}
	}
	width, height, rotation, config, err := heifImageProperties(item, props)
	if err != nil {
		return err
	}
	if item.kind == "grid" {
		if config != nil || heifValidateGrid(item, derived[primaryID], items, props, width, height) != nil {
			return ErrInvalid
		}
	} else if heifValidateCodedItem(item, config, width, height) != nil {
		return ErrInvalid
	}
	if rotation == 1 || rotation == 3 {
		width, height = height, width
	}
	info.Width, info.Height, info.Codec, info.ContentType = width, height, "hevc", "image/heic"
	return nil
}

func heifImageProperties(item *heifItem, props []heifPart) (int, int, int, []byte, error) {
	width, height, rotation, config := 0, 0, -1, []byte(nil)
	for _, index := range item.props {
		p := props[index-1]
		switch p.kind {
		case "ispe":
			if width != 0 || len(p.data) != 12 || !heifFull(p.data, 0, 0) {
				return 0, 0, 0, nil, ErrInvalid
			}
			width, height = int(binary.BigEndian.Uint32(p.data[4:])), int(binary.BigEndian.Uint32(p.data[8:]))
		case "hvcC":
			if config != nil {
				return 0, 0, 0, nil, ErrInvalid
			}
			config = p.data
		case "irot":
			if rotation >= 0 || len(p.data) != 1 || p.data[0] > 3 {
				return 0, 0, 0, nil, ErrInvalid
			}
			rotation = int(p.data[0])
		case "imir":
			if len(p.data) != 1 || p.data[0] > 1 {
				return 0, 0, 0, nil, ErrInvalid
			}
		case "pixi":
			if len(p.data) < 6 || !heifFull(p.data, 0, 0) || int(p.data[4])+5 != len(p.data) || p.data[4] != 1 && p.data[4] != 3 {
				return 0, 0, 0, nil, ErrInvalid
			}
			for _, depth := range p.data[5:] {
				if depth < 8 || depth > 16 {
					return 0, 0, 0, nil, ErrInvalid
				}
			}
		case "colr", "clli", "mdcv": // Color metadata does not alter dimensions.
		default:
			return 0, 0, 0, nil, ErrInvalid
		}
	}
	if width < 1 || height < 1 || width > 100000 || height > 100000 {
		return 0, 0, 0, nil, ErrInvalid
	}
	return width, height, rotation, config, nil
}

func heifValidateCodedItem(item *heifItem, config []byte, width, height int) error {
	if config == nil {
		return ErrInvalid
	}
	lengthSize, codedWidth, codedHeight, ppsID, err := heifHEVCConfig(config)
	if err != nil || codedWidth != width || codedHeight != height || !heifHEVCSample(item.data, lengthSize, ppsID) {
		return ErrInvalid
	}
	return nil
}

func heifValidateGrid(item *heifItem, refs []uint32, items map[uint32]*heifItem, props []heifPart, width, height int) error {
	d := item.data
	if len(d) < 8 || d[0] != 0 || d[1] > 1 {
		return ErrInvalid
	}
	fieldSize := 2
	if d[1] == 1 {
		fieldSize = 4
	}
	if len(d) != 4+2*fieldSize {
		return ErrInvalid
	}
	rows, columns := int(d[2])+1, int(d[3])+1
	c := heifCursor{data: d, pos: 4}
	w, h := c.read(fieldSize), c.read(fieldSize)
	if w != uint64(width) || h != uint64(height) || rows*columns > heifMaxItems || len(refs) != rows*columns {
		return ErrInvalid
	}
	tileWidth, tileHeight := 0, 0
	var chroma, luma, colour byte
	visited := map[uint32]bool{}
	for _, id := range refs {
		if visited[id] {
			continue
		}
		visited[id] = true
		tile := items[id]
		if tile == nil || tile.kind != "hvc1" {
			return ErrInvalid
		}
		tw, th, rotation, config, err := heifImageProperties(tile, props)
		if err != nil || rotation > 0 || heifValidateCodedItem(tile, config, tw, th) != nil {
			return ErrInvalid
		}
		if tileWidth == 0 {
			tileWidth, tileHeight = tw, th
			chroma, luma, colour = config[16]&3, config[17]&7, config[18]&7
		}
		if tw != tileWidth || th != tileHeight || chroma != config[16]&3 || luma != config[17]&7 || colour != config[18]&7 {
			return ErrInvalid
		}
	}
	// The final row/column may be clipped, but every grid tile must contribute
	// pixels and the complete output must be covered by validated tile data.
	if width <= (columns-1)*tileWidth || width > columns*tileWidth || height <= (rows-1)*tileHeight || height > rows*tileHeight {
		return ErrInvalid
	}
	return nil
}

func heifLocalDataReference(part heifPart) bool {
	parts, err := heifParts(part.data, part.offset)
	if err != nil || len(parts) != 1 || parts[0].kind != "dref" {
		return false
	}
	d := parts[0]
	if len(d.data) < 8 || !heifFull(d.data, 0, 0) || binary.BigEndian.Uint32(d.data[4:]) != 1 {
		return false
	}
	refs, err := heifParts(d.data[8:], d.offset+8)
	return err == nil && len(refs) == 1 && refs[0].kind == "url " && len(refs[0].data) == 4 && heifFull(refs[0].data, 0, 1)
}

func heifItems(parts []heifPart) (map[uint32]*heifItem, error) {
	iinf, err := heifUnique(parts, "iinf")
	if err != nil || !heifFull(iinf.data, 1, 0) {
		return nil, ErrInvalid
	}
	c := heifCursor{data: iinf.data, pos: 4}
	n := 2
	if iinf.data[0] == 1 {
		n = 4
	}
	count := c.read(n)
	if c.bad || count == 0 || count > heifMaxItems {
		return nil, ErrInvalid
	}
	entries, err := heifParts(iinf.data[c.pos:], iinf.offset+uint64(c.pos))
	if err != nil || uint64(len(entries)) != count {
		return nil, ErrInvalid
	}
	items := map[uint32]*heifItem{}
	for _, p := range entries {
		if p.kind != "infe" || len(p.data) < 13 || p.data[0] < 2 || !heifFull(p.data, 3, uint32(p.data[3]&1)) {
			return nil, ErrInvalid
		}
		c := heifCursor{data: p.data, pos: 4}
		n := 2
		if p.data[0] == 3 {
			n = 4
		}
		id, protection := uint32(c.read(n)), c.read(2)
		kind := c.read(4)
		if c.bad || id == 0 || items[id] != nil || protection != 0 || bytes.IndexByte(p.data[c.pos:], 0) < 0 {
			return nil, ErrInvalid
		}
		itemType := string([]byte{byte(kind >> 24), byte(kind >> 16), byte(kind >> 8), byte(kind)})
		if itemType != "hvc1" && itemType != "grid" && itemType != "Exif" && itemType != "mime" {
			return nil, ErrInvalid
		}
		items[id] = &heifItem{kind: itemType, hidden: p.data[3]&1 != 0}
	}
	return items, nil
}

func heifLocations(parts, top []heifPart, items map[uint32]*heifItem, data []byte) error {
	loc, err := heifUnique(parts, "iloc")
	if err != nil || !heifFull(loc.data, 2, 0) || len(loc.data) < 8 {
		return ErrInvalid
	}
	version := loc.data[0]
	offsetSize, lengthSize, baseSize, indexSize := int(loc.data[4]>>4), int(loc.data[4]&15), int(loc.data[5]>>4), int(loc.data[5]&15)
	if (offsetSize != 0 && offsetSize != 4 && offsetSize != 8) || (lengthSize != 4 && lengthSize != 8) || (baseSize != 0 && baseSize != 4 && baseSize != 8) || indexSize != 0 {
		return ErrInvalid
	}
	c := heifCursor{data: loc.data, pos: 6}
	n := 2
	if version == 2 {
		n = 4
	}
	count := c.read(n)
	if count != uint64(len(items)) {
		return ErrInvalid
	}
	seen := map[uint32]bool{}
	for i := uint64(0); i < count; i++ {
		id := uint32(c.read(n))
		item := items[id]
		method := uint64(0)
		if version > 0 {
			method = c.read(2)
		}
		ref, base := c.read(2), c.read(baseSize)
		extents := c.read(2)
		if c.bad || item == nil || seen[id] || method > 1 || ref != 0 || extents != 1 {
			return ErrInvalid
		}
		seen[id] = true
		offset, length := c.read(offsetSize), c.read(lengthSize)
		if c.bad || length == 0 || base > uint64(len(data)) || offset > uint64(len(data))-base {
			return ErrInvalid
		}
		start := base + offset
		if method == 1 {
			idat, e := heifUnique(parts, "idat")
			if e != nil || start > uint64(len(idat.data)) || length > uint64(len(idat.data))-start {
				return ErrInvalid
			}
			item.data = idat.data[int(start):int(start+length)]
		} else {
			if start > uint64(len(data)) || length > uint64(len(data))-start {
				return ErrInvalid
			}
			contained := false
			for _, m := range top {
				if m.kind == "mdat" && start >= m.offset && start-m.offset <= uint64(len(m.data)) && length <= uint64(len(m.data))-(start-m.offset) {
					contained = true
				}
			}
			if !contained {
				return ErrInvalid
			}
			item.data = data[int(start):int(start+length)]
		}
	}
	if !c.done() {
		return ErrInvalid
	}
	return nil
}

func heifProperties(parts []heifPart, items map[uint32]*heifItem) ([]heifPart, error) {
	iprp, err := heifUnique(parts, "iprp")
	if err != nil {
		return nil, err
	}
	children, err := heifParts(iprp.data, iprp.offset)
	if err != nil {
		return nil, err
	}
	ipco, err := heifUnique(children, "ipco")
	if err != nil {
		return nil, err
	}
	props, err := heifParts(ipco.data, ipco.offset)
	if err != nil || len(props) == 0 || len(props) > 256 {
		return nil, ErrInvalid
	}
	ipma, err := heifUnique(children, "ipma")
	if err != nil || len(ipma.data) < 8 || ipma.data[0] > 1 || ipma.data[1] != 0 || ipma.data[2] != 0 || ipma.data[3] > 1 {
		return nil, ErrInvalid
	}
	c := heifCursor{data: ipma.data, pos: 4}
	count := c.read(4)
	if count == 0 || count > uint64(len(items)) {
		return nil, ErrInvalid
	}
	seen := map[uint32]bool{}
	for i := uint64(0); i < count; i++ {
		n := 2
		if ipma.data[0] == 1 {
			n = 4
		}
		id := uint32(c.read(n))
		item := items[id]
		associations := c.read(1)
		if c.bad || item == nil || seen[id] || associations == 0 {
			return nil, ErrInvalid
		}
		seen[id] = true
		indexes := map[int]bool{}
		for j := uint64(0); j < associations; j++ {
			n = 1
			mask := uint64(0x7f)
			if ipma.data[3] == 1 {
				n = 2
				mask = 0x7fff
			}
			a := c.read(n)
			index := int(a & mask)
			if c.bad || index < 1 || index > len(props) || indexes[index] {
				return nil, ErrInvalid
			}
			indexes[index] = true
			// Every associated property must be understood, even if nonessential.
			// Otherwise a geometry-affecting transform could be ignored.
			item.props = append(item.props, index)
		}
	}
	if !c.done() {
		return nil, ErrInvalid
	}
	return props, nil
}

type heifBits struct {
	data []byte
	pos  int
	bad  bool
}

func (b *heifBits) read(n int) uint64 {
	if n < 0 || n > 64 || n > len(b.data)*8-b.pos {
		b.bad = true
		return 0
	}
	var v uint64
	for i := 0; i < n; i++ {
		v = v<<1 | uint64((b.data[b.pos/8]>>uint(7-b.pos%8))&1)
		b.pos++
	}
	return v
}
func (b *heifBits) ue() uint64 {
	zeros := 0
	for !b.bad && b.read(1) == 0 {
		zeros++
		if zeros > 31 {
			b.bad = true
			return 0
		}
	}
	if b.bad {
		return 0
	}
	return (uint64(1) << uint(zeros)) - 1 + b.read(zeros)
}
func heifNAL(nal []byte, kind byte) bool {
	return len(nal) >= 3 && nal[0]&0x80 == 0 && (nal[0]>>1)&63 == kind && nal[0]&1 == 0 && nal[1]>>3 == 0 && nal[1]&7 != 0
}
func heifRBSP(nal []byte) ([]byte, error) {
	if len(nal) < 3 {
		return nil, ErrInvalid
	}
	rbsp := make([]byte, 0, len(nal)-2)
	zeros := 0
	for i := 2; i < len(nal); i++ {
		v := nal[i]
		if zeros >= 2 && v == 3 {
			if i+1 >= len(nal) || nal[i+1] > 3 {
				return nil, ErrInvalid
			}
			zeros = 0
			continue
		}
		rbsp = append(rbsp, v)
		if v == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return rbsp, nil
}

type heifParameterLinks struct{ vpsID, spsID, chroma uint64 }

func heifSPS(nal []byte, links *heifParameterLinks) (int, int, int, int, error) {
	if !heifNAL(nal, 33) {
		return 0, 0, 0, 0, ErrInvalid
	}
	rbsp, err := heifRBSP(nal)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	b := heifBits{data: rbsp}
	links.vpsID = b.read(4)
	layers := int(b.read(3))
	b.read(1)
	if layers > 6 {
		return 0, 0, 0, 0, ErrInvalid
	}
	b.read(64)
	b.read(32)
	var profile, level [7]bool
	for i := 0; i < layers; i++ {
		profile[i] = b.read(1) != 0
		level[i] = b.read(1) != 0
	}
	if layers > 0 {
		for i := layers; i < 8; i++ {
			if b.read(2) != 0 {
				return 0, 0, 0, 0, ErrInvalid
			}
		}
	}
	for i := 0; i < layers; i++ {
		if profile[i] {
			b.read(64)
			b.read(24)
		}
		if level[i] {
			b.read(8)
		}
	}
	links.spsID = b.ue()
	if links.spsID > 15 {
		return 0, 0, 0, 0, ErrInvalid
	}
	chroma := b.ue()
	links.chroma = chroma
	if chroma > 3 {
		return 0, 0, 0, 0, ErrInvalid
	}
	separate := false
	if chroma == 3 {
		separate = b.read(1) != 0
	}
	width, height := b.ue(), b.ue()
	if width == 0 || height == 0 || width > 100000 || height > 100000 {
		return 0, 0, 0, 0, ErrInvalid
	}
	if b.read(1) != 0 {
		left, right, top, bottom := b.ue(), b.ue(), b.ue(), b.ue()
		x, y := uint64(1), uint64(1)
		if !separate && (chroma == 1 || chroma == 2) {
			x = 2
		}
		if !separate && chroma == 1 {
			y = 2
		}
		if left+right >= width || top+bottom >= height || (left+right)*x >= width || (top+bottom)*y >= height {
			return 0, 0, 0, 0, ErrInvalid
		}
		width -= (left + right) * x
		height -= (top + bottom) * y
	}
	luma, colour := b.ue()+8, b.ue()+8
	if b.bad || luma > 16 || colour > 16 {
		return 0, 0, 0, 0, ErrInvalid
	}
	return int(width), int(height), int(luma), int(colour), nil
}
func heifHEVCConfig(data []byte) (int, int, int, uint64, error) {
	if len(data) < 23 || data[0] != 1 || data[13]&0xf0 != 0xf0 || data[15]&0xfc != 0xfc || data[16]&0xfc != 0xfc || data[17]&0xf8 != 0xf8 || data[18]&0xf8 != 0xf8 {
		return 0, 0, 0, 0, ErrInvalid
	}
	lengthSize := int(data[21]&3) + 1
	c := heifCursor{data: data, pos: 23}
	count := int(data[22])
	found := map[byte]bool{}
	width, height := 0, 0
	var vpsID, ppsID, ppsSPS uint64
	var links heifParameterLinks
	if count < 3 || count > 16 {
		return 0, 0, 0, 0, ErrInvalid
	}
	for i := 0; i < count; i++ {
		header := byte(c.read(1))
		kind := header & 63
		units := c.read(2)
		if c.bad || header&0x40 != 0 || kind != 32 && kind != 33 && kind != 34 && kind != 39 && kind != 40 || units == 0 || units > 16 || kind <= 34 && units != 1 || found[kind] {
			return 0, 0, 0, 0, ErrInvalid
		}
		found[kind] = true
		for j := uint64(0); j < units; j++ {
			n := int(c.read(2))
			if c.bad || n < 3 || n > len(c.data)-c.pos {
				return 0, 0, 0, 0, ErrInvalid
			}
			nal := c.data[c.pos : c.pos+n]
			c.pos += n
			if !heifNAL(nal, kind) {
				return 0, 0, 0, 0, ErrInvalid
			}
			if kind == 32 {
				vpsID = uint64(nal[2] >> 4)
			}
			if kind == 34 {
				rbsp, e := heifRBSP(nal)
				if e != nil {
					return 0, 0, 0, 0, ErrInvalid
				}
				b := heifBits{data: rbsp}
				ppsID, ppsSPS = b.ue(), b.ue()
				if b.bad || ppsID > 63 || ppsSPS > 15 {
					return 0, 0, 0, 0, ErrInvalid
				}
			}
			if kind == 33 {
				w, h, luma, colour, e := heifSPS(nal, &links)
				if e != nil || width != 0 && (w != width || h != height) || links.chroma != uint64(data[16]&3) || luma != int(data[17]&7)+8 || colour != int(data[18]&7)+8 {
					return 0, 0, 0, 0, ErrInvalid
				}
				width, height = w, h
			}
		}
	}
	if !c.done() || !found[32] || !found[33] || !found[34] || vpsID != links.vpsID || ppsSPS != links.spsID {
		return 0, 0, 0, 0, ErrInvalid
	}
	return lengthSize, width, height, ppsID, nil
}
func heifHEVCSample(data []byte, lengthSize int, ppsID uint64) bool {
	c := heifCursor{data: data}
	slices := 0
	count := 0
	for c.pos < len(data) && !c.bad {
		count++
		if count > 4096 {
			return false
		}
		n := c.read(lengthSize)
		if c.bad || n < 3 || n > uint64(len(data)-c.pos) {
			return false
		}
		nal := data[c.pos : c.pos+int(n)]
		c.pos += int(n)
		kind := (nal[0] >> 1) & 63
		if !heifNAL(nal, kind) {
			return false
		}
		if kind <= 31 {
			if kind < 16 || kind > 21 {
				return false
			} // Independently decodable still picture.
			if slices == 0 && nal[2]&0x80 == 0 || slices > 0 && nal[2]&0x80 != 0 {
				return false
			}
			// A valid PPS ID is at most 63, so this prefix fits before any
			// emulation-prevention sequence. Avoid copying the image payload.
			b := heifBits{data: nal[2:]}
			b.read(1)
			b.read(1)
			if b.ue() != ppsID || b.bad {
				return false
			}
			slices++
		} else if kind != 39 && kind != 40 && kind != 35 {
			return false
		}
	}
	return c.done() && slices > 0
}
