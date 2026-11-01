package mediafile

import (
	"encoding/binary"
	"math"
)

var aacSampleRates = [...]uint32{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

// adtsAAC inspects a complete AAC-LC ADTS sequence, not a sync-word prefix.
// CRC-protected, multi-block, PCE-channel and non-LC streams are deliberately
// unsupported; no decoder or unverifiable CRC/payload claim is made here.
func adtsAAC(data []byte, info *Info) error {
	var frames uint64
	var rate uint32
	var configuration byte
	for pos := 0; pos < len(data); {
		if len(data)-pos < 8 {
			return ErrInvalid
		}
		h := data[pos : pos+7]
		index := (h[2] >> 2) & 15
		channels := (h[2]&1)<<2 | h[3]>>6
		if h[0] != 0xff || h[1]&0xf6 != 0xf0 || h[1]&1 == 0 || h[2]>>6 != 1 || int(index) >= len(aacSampleRates) || channels == 0 || h[6]&3 != 0 {
			return ErrInvalid
		}
		current := aacSampleRates[index]
		config := (h[1] & 8) | channels
		if frames > 0 && (rate != current || configuration != config) {
			return ErrInvalid
		}
		size := int(h[3]&3)<<11 | int(h[4])<<3 | int(h[5]>>5)
		if size <= 7 || size > len(data)-pos {
			return ErrInvalid
		}
		frames++
		rate = current
		configuration = config
		pos += size
	}
	if frames == 0 {
		return ErrInvalid
	}
	info.Duration = float64(frames) * 1024 / float64(rate)
	info.Codec = "aac"
	return nil
}

type aacAtom struct {
	kind   string
	data   []byte
	offset uint64
}

// A shared budget bounds metadata fan-out; recursion below follows a fixed
// container path rather than caller-controlled nesting. Payload bytes are views.
func aacAtoms(data []byte, budget *int) ([]aacAtom, error) {
	var result []aacAtom
	for pos := 0; pos < len(data); {
		if *budget <= 0 || len(data)-pos < 8 {
			return nil, ErrInvalid
		}
		*budget--
		size := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
		head := 8
		if size == 1 {
			if len(data)-pos < 16 {
				return nil, ErrInvalid
			}
			head = 16
			size = binary.BigEndian.Uint64(data[pos+8 : pos+16])
		} else if size == 0 {
			size = uint64(len(data) - pos)
		}
		if size < uint64(head) || size > uint64(len(data)-pos) {
			return nil, ErrInvalid
		}
		result = append(result, aacAtom{kind: string(data[pos+4 : pos+8]), data: data[pos+head : pos+int(size)], offset: uint64(pos + head)})
		pos += int(size)
	}
	return result, nil
}
func aacOne(atoms []aacAtom, kind string) ([]byte, error) {
	var result []byte
	for _, a := range atoms {
		if a.kind == kind {
			if result != nil {
				return nil, ErrInvalid
			}
			result = a.data
		}
	}
	if result == nil {
		return nil, ErrInvalid
	}
	return result, nil
}
func aacFullBox(data []byte) bool { return len(data) >= 4 && binary.BigEndian.Uint32(data[:4]) == 0 }

// audioMP4 accepts non-fragmented, self-contained AAC-LC MP4 audio. It requires
// one soun track, decoder configuration, real sample tables and chunk extents
// inside mdat. An ftyp brand, filename, or audio handler alone is insufficient.
// The video parser remains independent, including all dimension/FPS checks.
func audioMP4(data []byte, info *Info) error {
	budget := 4096
	top, err := aacAtoms(data, &budget)
	if err != nil {
		return err
	}
	ftyp, err := aacOne(top, "ftyp")
	if err != nil || len(ftyp) < 8 || len(ftyp)%4 != 0 {
		return ErrInvalid
	}
	moov, err := aacOne(top, "moov")
	if err != nil {
		return err
	}
	type extent struct{ start, end uint64 }
	var media []extent
	var mediaBytes uint64
	for _, a := range top {
		if a.kind == "moof" {
			return ErrInvalid
		}
		if a.kind == "mdat" && len(a.data) > 0 {
			media = append(media, extent{a.offset, a.offset + uint64(len(a.data))})
			mediaBytes += uint64(len(a.data))
		}
	}
	if len(media) == 0 {
		return ErrInvalid
	}
	movie, err := aacAtoms(moov, &budget)
	if err != nil {
		return err
	}
	track, err := aacOne(movie, "trak")
	if err != nil {
		return err
	}
	for _, a := range movie {
		if a.kind == "mvex" {
			return ErrInvalid
		}
	}
	parts, err := aacAtoms(track, &budget)
	if err != nil {
		return err
	}
	mdia, err := aacOne(parts, "mdia")
	if err != nil {
		return err
	}
	metadata, err := aacAtoms(mdia, &budget)
	if err != nil {
		return err
	}
	handler, err := aacOne(metadata, "hdlr")
	if err != nil || len(handler) < 24 || !aacFullBox(handler) || string(handler[8:12]) != "soun" {
		return ErrInvalid
	}
	header, err := aacOne(metadata, "mdhd")
	if err != nil {
		return err
	}
	scale, ticks, err := aacMediaTime(header)
	if err != nil {
		return err
	}
	minf, err := aacOne(metadata, "minf")
	if err != nil {
		return err
	}
	mediaInfo, err := aacAtoms(minf, &budget)
	if err != nil {
		return err
	}
	dinf, err := aacOne(mediaInfo, "dinf")
	if err != nil {
		return err
	}
	if err = aacSelfContained(dinf, &budget); err != nil {
		return err
	}
	stbl, err := aacOne(mediaInfo, "stbl")
	if err != nil {
		return err
	}
	tables, err := aacAtoms(stbl, &budget)
	if err != nil {
		return err
	}
	description, err := aacOne(tables, "stsd")
	if err != nil {
		return err
	}
	var sampleRate uint32
	if err = aacDescription(description, &budget, &sampleRate); err != nil {
		return err
	}
	timing, err := aacOne(tables, "stts")
	if err != nil {
		return err
	}
	samples, sampleTicks, err := aacTiming(timing)
	if samples > mediaBytes {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	sizes, err := aacOne(tables, "stsz")
	if err != nil {
		return err
	}
	sampleSizes, err := aacSampleSizes(sizes, samples)
	if err != nil {
		return err
	}
	chunks, err := aacOne(tables, "stsc")
	if err != nil {
		return err
	}
	var offsets []byte
	wide := false
	for _, a := range tables {
		if a.kind == "stco" || a.kind == "co64" {
			if offsets != nil {
				return ErrInvalid
			}
			offsets = a.data
			wide = a.kind == "co64"
		}
	}
	if offsets == nil || !aacFullBox(offsets) || len(offsets) < 8 {
		return ErrInvalid
	}
	count := uint64(binary.BigEndian.Uint32(offsets[4:8]))
	width := uint64(4)
	if wide {
		width = 8
	}
	if count == 0 || count > uint64((len(offsets)-8))/width || uint64(len(offsets)-8) != count*width {
		return ErrInvalid
	}
	if !aacFullBox(chunks) || len(chunks) < 8 {
		return ErrInvalid
	}
	mappings := uint64(binary.BigEndian.Uint32(chunks[4:8]))
	if mappings == 0 || mappings > uint64((len(chunks)-8)/12) || uint64(len(chunks)-8) != mappings*12 {
		return ErrInvalid
	}
	for i := uint64(0); i < mappings; i++ {
		entry := chunks[8+i*12 : 20+i*12]
		first := uint64(binary.BigEndian.Uint32(entry[:4]))
		perChunk := binary.BigEndian.Uint32(entry[4:8])
		if first == 0 || first > count || perChunk == 0 || binary.BigEndian.Uint32(entry[8:12]) != 1 || i == 0 && first != 1 {
			return ErrInvalid
		}
		if i > 0 && first <= uint64(binary.BigEndian.Uint32(chunks[8+(i-1)*12:12+(i-1)*12])) {
			return ErrInvalid
		}
	}
	sample, entryIndex, previousEnd := uint64(0), uint64(0), uint64(0)
	mediaIndex := 0
	for chunk := uint64(0); chunk < count; chunk++ {
		if entryIndex+1 < mappings && chunk+1 == uint64(binary.BigEndian.Uint32(chunks[8+(entryIndex+1)*12:12+(entryIndex+1)*12])) {
			entryIndex++
		}
		perChunk := uint64(binary.BigEndian.Uint32(chunks[12+entryIndex*12 : 16+entryIndex*12]))
		if perChunk > samples-sample {
			return ErrInvalid
		}
		var size uint64
		for i := uint64(0); i < perChunk; i++ {
			n := sampleSizes.at(sample + i)
			if n == 0 || size > math.MaxUint64-uint64(n) {
				return ErrInvalid
			}
			size += uint64(n)
		}
		sample += perChunk
		start := uint64(binary.BigEndian.Uint32(offsets[8+chunk*width : 12+chunk*width]))
		if wide {
			start = binary.BigEndian.Uint64(offsets[8+chunk*width : 16+chunk*width])
		}
		if start < previousEnd || size > math.MaxUint64-start {
			return ErrInvalid
		}
		end := start + size
		for mediaIndex < len(media) && start >= media[mediaIndex].end {
			mediaIndex++
		}
		if mediaIndex >= len(media) || start < media[mediaIndex].start || end > media[mediaIndex].end {
			return ErrInvalid
		}
		previousEnd = end
	}
	if sample != samples {
		return ErrInvalid
	}
	// A short declared timeline must not hide complete AAC-LC packets. Each
	// supported packet decodes 1024 samples, including encoder padding.
	info.Duration = math.Max(float64(samples)*1024/float64(sampleRate), math.Max(float64(ticks)/float64(scale), float64(sampleTicks)/float64(scale)))
	info.Codec = "aac"
	return nil
}

func aacMediaTime(data []byte) (uint32, uint64, error) {
	if len(data) < 4 || data[1] != 0 || data[2] != 0 || data[3] != 0 {
		return 0, 0, ErrInvalid
	}
	var scale uint32
	var ticks uint64
	switch data[0] {
	case 0:
		if len(data) != 24 {
			return 0, 0, ErrInvalid
		}
		scale = binary.BigEndian.Uint32(data[12:16])
		ticks = uint64(binary.BigEndian.Uint32(data[16:20]))
		if ticks == math.MaxUint32 {
			return 0, 0, ErrInvalid
		}
	case 1:
		if len(data) != 36 {
			return 0, 0, ErrInvalid
		}
		scale = binary.BigEndian.Uint32(data[20:24])
		ticks = binary.BigEndian.Uint64(data[24:32])
		if ticks == math.MaxUint64 {
			return 0, 0, ErrInvalid
		}
	default:
		return 0, 0, ErrInvalid
	}
	if scale == 0 || ticks == 0 {
		return 0, 0, ErrInvalid
	}
	return scale, ticks, nil
}
func aacSelfContained(data []byte, budget *int) error {
	atoms, err := aacAtoms(data, budget)
	if err != nil {
		return err
	}
	dref, err := aacOne(atoms, "dref")
	if err != nil || !aacFullBox(dref) || len(dref) < 8 || binary.BigEndian.Uint32(dref[4:8]) != 1 {
		return ErrInvalid
	}
	refs, err := aacAtoms(dref[8:], budget)
	if err != nil || len(refs) != 1 || refs[0].kind != "url " || len(refs[0].data) != 4 || binary.BigEndian.Uint32(refs[0].data) != 1 {
		return ErrInvalid
	}
	return nil
}
func aacDescription(data []byte, budget *int, sampleRate *uint32) error {
	if !aacFullBox(data) || len(data) < 8 || binary.BigEndian.Uint32(data[4:8]) != 1 {
		return ErrInvalid
	}
	entries, err := aacAtoms(data[8:], budget)
	if err != nil || len(entries) != 1 || entries[0].kind != "mp4a" {
		return ErrInvalid
	}
	entry := entries[0].data
	if len(entry) < 28 || binary.BigEndian.Uint16(entry[6:8]) != 1 || binary.BigEndian.Uint16(entry[8:10]) != 0 {
		return ErrInvalid
	}
	extensions, err := aacAtoms(entry[28:], budget)
	if err != nil {
		return err
	}
	esds, err := aacOne(extensions, "esds")
	if err != nil || !aacFullBox(esds) {
		return ErrInvalid
	}
	es, tail, err := aacDescriptor(esds[4:], 3)
	if err != nil || len(tail) != 0 || len(es) < 3 || es[2]&0xe0 != 0 {
		return ErrInvalid
	}
	decoder, sl, err := aacDescriptor(es[3:], 4)
	if err != nil || len(decoder) < 13 || decoder[0] != 0x40 || decoder[1] != 0x15 {
		return ErrInvalid
	}
	config, tail, err := aacDescriptor(decoder[13:], 5)
	if err != nil || len(tail) != 0 {
		return ErrInvalid
	}
	predefined, tail, err := aacDescriptor(sl, 6)
	if err != nil || len(tail) != 0 || len(predefined) != 1 || predefined[0] != 2 {
		return ErrInvalid
	}
	rate, channels, err := aacConfig(config)
	if err != nil {
		return err
	}
	if rate > 65535 || binary.BigEndian.Uint32(entry[24:28]) != rate<<16 || uint32(binary.BigEndian.Uint16(entry[16:18])) != channels {
		return ErrInvalid
	}
	*sampleRate = rate
	return nil
}
func aacDescriptor(data []byte, tag byte) ([]byte, []byte, error) {
	if len(data) < 2 || data[0] != tag {
		return nil, nil, ErrInvalid
	}
	size, pos := uint64(0), 1
	for i := 0; i < 4; i++ {
		if pos >= len(data) {
			return nil, nil, ErrInvalid
		}
		v := data[pos]
		pos++
		size = size<<7 | uint64(v&0x7f)
		if v&0x80 == 0 {
			if size > uint64(len(data)-pos) {
				return nil, nil, ErrInvalid
			}
			end := pos + int(size)
			return data[pos:end], data[end:], nil
		}
	}
	return nil, nil, ErrInvalid
}
func aacConfig(data []byte) (uint32, uint32, error) {
	if len(data) < 2 || data[0]>>3 != 2 {
		return 0, 0, ErrInvalid
	}
	index := (data[0]&7)<<1 | data[1]>>7
	channels := (data[1] >> 3) & 15
	if int(index) >= len(aacSampleRates) || channels == 0 || channels > 7 || data[1]&7 != 0 {
		return 0, 0, ErrInvalid
	}
	// AAC-LC may append an explicitly disabled SBR sync extension. Active SBR,
	// PS, 960-sample frames, core dependencies and other extensions are unsupported.
	if len(data) != 2 && (len(data) != 5 || data[2] != 0x56 || data[3] != 0xe5 || data[4] != 0) {
		return 0, 0, ErrInvalid
	}
	if channels == 7 {
		channels = 8
	}
	return aacSampleRates[index], uint32(channels), nil
}
func aacTiming(data []byte) (uint64, uint64, error) {
	if !aacFullBox(data) || len(data) < 8 {
		return 0, 0, ErrInvalid
	}
	count := uint64(binary.BigEndian.Uint32(data[4:8]))
	if count == 0 || count > uint64((len(data)-8)/8) || uint64(len(data)-8) != count*8 {
		return 0, 0, ErrInvalid
	}
	var samples, ticks uint64
	for i := uint64(0); i < count; i++ {
		n := uint64(binary.BigEndian.Uint32(data[8+i*8 : 12+i*8]))
		delta := uint64(binary.BigEndian.Uint32(data[12+i*8 : 16+i*8]))
		if n == 0 || delta == 0 || samples > math.MaxUint64-n || ticks > math.MaxUint64-n*delta {
			return 0, 0, ErrInvalid
		}
		samples += n
		ticks += n * delta
	}
	return samples, ticks, nil
}

type aacSizes struct {
	constant uint32
	data     []byte
}

func (s aacSizes) at(index uint64) uint32 {
	if s.constant != 0 {
		return s.constant
	}
	return binary.BigEndian.Uint32(s.data[index*4 : index*4+4])
}
func aacSampleSizes(data []byte, expected uint64) (aacSizes, error) {
	if !aacFullBox(data) || len(data) < 12 || uint64(binary.BigEndian.Uint32(data[8:12])) != expected {
		return aacSizes{}, ErrInvalid
	}
	constant := binary.BigEndian.Uint32(data[4:8])
	if constant != 0 {
		if len(data) != 12 {
			return aacSizes{}, ErrInvalid
		}
		return aacSizes{constant: constant}, nil
	}
	if expected > uint64((len(data)-12)/4) || uint64(len(data)-12) != expected*4 {
		return aacSizes{}, ErrInvalid
	}
	return aacSizes{data: data[12:]}, nil
}
