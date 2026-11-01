package mediafile

import "encoding/binary"

// Codec evidence follows self-contained MP4 audio sample descriptions. A generic
// mp4a entry is not an AAC claim: MPEG-4 audio OTI0x40 needs an AAC ASC, and MPEG
// audio OTI0x69/0x6b needs actual LayerIII frame headers. See MP4RA Object Types:
// https://mp4ra.org/registered-types/object-types . This is bounded structural
// inspection, not audio decoding. Unsupported constructions remain unknown and
// do not change the existing unconstrained primary-video acceptance contract.
type audioCodecExtent struct{ start, end uint64 }
type audioCodecWork struct{ samples, bytes uint64 }

func mp4AudioCodecEvidence(data []byte, info *Info) {
	// The primary-video reader deliberately retains legacy acceptance and may
	// select one movie. That selection cannot prove a complete stream inventory:
	// validate unique container/track metadata before retaining even zero audio.
	info.AudioStreams, info.VideoStreams, info.AudioCodecs = nil, nil, nil
	budget := 4096
	top, err := aacAtoms(data, &budget)
	if err != nil {
		return
	}
	movie, err := aacOne(top, "moov")
	if err != nil {
		return
	}
	entries, err := aacAtoms(movie, &budget)
	if err != nil {
		return
	}
	var media []audioCodecExtent
	for _, a := range top {
		if a.kind == "moof" {
			return
		}
		if a.kind == "mdat" && len(a.data) > 0 {
			media = append(media, audioCodecExtent{a.offset, a.offset + uint64(len(a.data))})
		}
	}
	audio, video, tracks := 0, 0, 0
	var audioTracks [][]aacAtom
	for _, track := range entries {
		if track.kind == "mvex" {
			return
		}
		if track.kind != "trak" {
			continue
		}
		tracks++
		parts, err := aacAtoms(track.data, &budget)
		if err != nil {
			return
		}
		mdia, err := aacOne(parts, "mdia")
		if err != nil {
			return
		}
		metadata, err := aacAtoms(mdia, &budget)
		if err != nil {
			return
		}
		handler, err := aacOne(metadata, "hdlr")
		if err != nil || len(handler) < 12 || !aacFullBox(handler) {
			return
		}
		switch string(handler[8:12]) {
		case "soun":
			audio++
			if audio <= 256 {
				audioTracks = append(audioTracks, metadata)
			}
		case "vide":
			video++
		case "text", "sbtl", "subt", "clcp", "tmcd", "meta", "mdta", "mdir", "hint", "pict":
			// Recognized non-audio handlers contribute no audio stream. An unknown
			// handler cannot establish silence for a codec-constrained input.
		default:
			return
		}
	}
	if tracks == 0 {
		return
	}
	info.AudioStreams, info.VideoStreams = &audio, &video
	if audio == 0 || audio > 256 {
		return
	}
	info.AudioCodecs = make([]string, audio)
	if len(media) == 0 {
		return
	}
	work := audioCodecWork{samples: 1 << 20, bytes: uint64(len(data))}
	for index, metadata := range audioTracks {
		info.AudioCodecs[index] = mp4AudioTrackCodec(data, metadata, media, &budget, &work)
	}
}
func mp4AudioTrackCodec(data []byte, metadata []aacAtom, media []audioCodecExtent, budget *int, work *audioCodecWork) string {
	minf, err := aacOne(metadata, "minf")
	if err != nil {
		return ""
	}
	parts, err := aacAtoms(minf, budget)
	if err != nil {
		return ""
	}
	dinf, err := aacOne(parts, "dinf")
	if err != nil || aacSelfContained(dinf, budget) != nil {
		return ""
	}
	stbl, err := aacOne(parts, "stbl")
	if err != nil {
		return ""
	}
	tables, err := aacAtoms(stbl, budget)
	if err != nil {
		return ""
	}
	description, err := aacOne(tables, "stsd")
	if err != nil {
		return ""
	}
	codec, rate, channels, oti := mp4AudioDescription(description, budget)
	if codec == "" {
		return ""
	}
	if !mp4AudioSampleProof(data, tables, media, codec, rate, channels, oti, work) {
		return ""
	}
	return codec
}
func mp4AudioDescription(data []byte, budget *int) (string, uint32, uint16, byte) {
	unknown := func() (string, uint32, uint16, byte) { return "", 0, 0, 0 }
	if !aacFullBox(data) || len(data) < 8 || binary.BigEndian.Uint32(data[4:8]) != 1 {
		return unknown()
	}
	entries, err := aacAtoms(data[8:], budget)
	if err != nil || len(entries) != 1 || entries[0].kind != "mp4a" {
		return unknown()
	}
	entry := entries[0].data
	if len(entry) < 28 || binary.BigEndian.Uint16(entry[6:8]) != 1 || binary.BigEndian.Uint16(entry[8:10]) != 0 {
		return unknown()
	}
	extensions, err := aacAtoms(entry[28:], budget)
	if err != nil {
		return unknown()
	}
	esds, err := aacOne(extensions, "esds")
	if err != nil || !aacFullBox(esds) {
		return unknown()
	}
	es, tail, err := aacDescriptor(esds[4:], 3)
	if err != nil || len(tail) != 0 || len(es) < 3 || es[2]&0xe0 != 0 {
		return unknown()
	}
	decoder, sl, err := aacDescriptor(es[3:], 4)
	if err != nil || len(decoder) < 13 || decoder[1] != 0x15 {
		return unknown()
	}
	predefined, tail, err := aacDescriptor(sl, 6)
	if err != nil || len(tail) != 0 || len(predefined) != 1 || predefined[0] != 2 {
		return unknown()
	}
	if decoder[0] == 0x40 {
		config, tail, err := aacDescriptor(decoder[13:], 5)
		if err != nil || len(tail) != 0 {
			return unknown()
		}
		parsed, ok := aacCodecASC(config)
		if !ok {
			return unknown()
		}
		headerRate := binary.BigEndian.Uint32(entry[24:28])
		headerChannels := uint32(binary.BigEndian.Uint16(entry[16:18]))
		if headerRate&0xffff != 0 || (headerRate>>16 != parsed.coreRate && headerRate>>16 != parsed.outputRate) ||
			(headerChannels != parsed.channels && !(parsed.ps && headerChannels == 2)) {
			return unknown()
		}
		return "aac", headerRate >> 16, uint16(headerChannels), 0x40
	}
	if (decoder[0] != 0x69 && decoder[0] != 0x6b) || len(decoder) != 13 {
		return unknown()
	}
	sampleRate := binary.BigEndian.Uint32(entry[24:28])
	channels := binary.BigEndian.Uint16(entry[16:18])
	if sampleRate&0xffff != 0 || sampleRate>>16 == 0 || (channels != 1 && channels != 2) {
		return unknown()
	}
	return "mp3", sampleRate >> 16, channels, decoder[0]
}

// Every audio sample must lie inside this file's mdat. The fixed traversal and
// table/sample budgets prevent unbounded metadata or frame-header work.
func mp4AudioSampleProof(data []byte, tables []aacAtom, media []audioCodecExtent, codec string, rate uint32, channels uint16, oti byte, work *audioCodecWork) bool {
	timing, err := aacOne(tables, "stts")
	if err != nil {
		return false
	}
	count, _, err := aacTiming(timing)
	if err != nil || count == 0 || count > work.samples || count > uint64(len(data)) {
		return false
	}
	work.samples -= count
	sizes, err := aacOne(tables, "stsz")
	if err != nil {
		return false
	}
	samples, err := aacSampleSizes(sizes, count)
	if err != nil {
		return false
	}
	chunks, err := aacOne(tables, "stsc")
	if err != nil || !aacFullBox(chunks) || len(chunks) < 8 {
		return false
	}
	mappings := uint64(binary.BigEndian.Uint32(chunks[4:8]))
	if mappings == 0 || mappings > 4096 || uint64(len(chunks)-8) != mappings*12 {
		return false
	}
	var offsets []byte
	var width uint64
	for _, table := range tables {
		if table.kind == "stco" || table.kind == "co64" {
			if offsets != nil {
				return false
			}
			offsets = table.data
			width = 4
			if table.kind == "co64" {
				width = 8
			}
		}
	}
	if !aacFullBox(offsets) || len(offsets) < 8 {
		return false
	}
	totalChunks := uint64(binary.BigEndian.Uint32(offsets[4:8]))
	if totalChunks == 0 || totalChunks > 1<<20 || uint64(len(offsets)-8) != totalChunks*width {
		return false
	}
	for i := uint64(0); i < mappings; i++ {
		row := chunks[8+i*12 : 20+i*12]
		first := uint64(binary.BigEndian.Uint32(row[:4]))
		if first == 0 || first > totalChunks || binary.BigEndian.Uint32(row[4:8]) == 0 || binary.BigEndian.Uint32(row[8:12]) != 1 || i == 0 && first != 1 {
			return false
		}
		if i > 0 && first <= uint64(binary.BigEndian.Uint32(chunks[8+(i-1)*12:12+(i-1)*12])) {
			return false
		}
	}
	sample, mapping, previousEnd := uint64(0), uint64(0), uint64(0)
	extentIndex := 0
	for chunk := uint64(0); chunk < totalChunks; chunk++ {
		if mapping+1 < mappings && chunk+1 == uint64(binary.BigEndian.Uint32(chunks[8+(mapping+1)*12:12+(mapping+1)*12])) {
			mapping++
		}
		n := uint64(binary.BigEndian.Uint32(chunks[12+mapping*12 : 16+mapping*12]))
		if n > count-sample {
			return false
		}
		start := uint64(binary.BigEndian.Uint32(offsets[8+chunk*width : 12+chunk*width]))
		if width == 8 {
			start = binary.BigEndian.Uint64(offsets[8+chunk*width : 16+chunk*width])
		}
		if start < previousEnd {
			return false
		}
		for i := uint64(0); i < n; i++ {
			size := uint64(samples.at(sample))
			sample++
			if size == 0 || size > work.bytes || start > uint64(len(data)) || size > uint64(len(data))-start {
				return false
			}
			work.bytes -= size
			end := start + size
			for extentIndex < len(media) && start >= media[extentIndex].end {
				extentIndex++
			}
			if extentIndex >= len(media) || start < media[extentIndex].start || end > media[extentIndex].end {
				return false
			}
			if codec == "mp3" && !mp3AudioPacket(data[int(start):int(end)], rate, channels, oti) {
				return false
			}
			start = end
		}
		previousEnd = start
	}
	return sample == count
}
func mp3AudioPacket(data []byte, rate uint32, channels uint16, oti byte) bool {
	if len(data) < 4 {
		return false
	}
	for pos := 0; pos < len(data); {
		if len(data)-pos < 4 {
			return false
		}
		h := binary.BigEndian.Uint32(data[pos : pos+4])
		version, layer := (h>>19)&3, (h>>17)&3
		if h>>21 != 0x7ff || layer != 1 || (oti == 0x6b && version != 3) || (oti == 0x69 && version != 2) {
			return false
		}
		frequency, bitrate := (h>>10)&3, (h>>12)&15
		if frequency == 3 || bitrate == 0 || bitrate == 15 {
			return false
		}
		currentRate := []uint32{44100, 48000, 32000}[frequency]
		rates := []uint32{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
		coefficient := uint32(144)
		if version == 2 {
			currentRate /= 2
			rates = []uint32{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
			coefficient = 72
		}
		currentChannels := uint16(2)
		if (h>>6)&3 == 3 {
			currentChannels = 1
		}
		if currentRate != rate || currentChannels != channels {
			return false
		}
		size := int(coefficient*rates[bitrate]*1000/currentRate + ((h >> 9) & 1))
		if size < 4 || size > len(data)-pos {
			return false
		}
		pos += size
	}
	return true
}

// ASC classification uses the MPEG-4 audio configuration hierarchy: initial
// AOT2, or explicit SBR5/PS29 wrapping AAC-LC2, followed by GA flags and optional
// sync extensions. RFC6416 sections3 and7.4.1.5-7 clarify core/output rates and PS
// mono-core/stereo output: https://www.rfc-editor.org/rfc/rfc6416.html .
// This identifies the codec from bounded actual configuration, not a container
// name or provider claim. PCE/dependent cores, other AOTs/extensions stay unknown.
type aacCodecConfig struct {
	coreRate, outputRate, channels uint32
	ps                             bool
}
type aacCodecBits struct {
	data []byte
	pos  int
	ok   bool
}

func (b *aacCodecBits) take(n int) uint32 {
	if !b.ok || n < 1 || n > 24 || n > len(b.data)*8-b.pos {
		b.ok = false
		return 0
	}
	var value uint32
	for i := 0; i < n; i++ {
		value = value<<1 | uint32((b.data[b.pos/8]>>uint(7-b.pos%8))&1)
		b.pos++
	}
	return value
}
func (b *aacCodecBits) rate() uint32 {
	index := b.take(4)
	if index == 15 {
		rate := b.take(24)
		if rate == 0 || rate > 384000 {
			b.ok = false
		}
		return rate
	}
	if int(index) >= len(aacSampleRates) {
		b.ok = false
		return 0
	}
	return aacSampleRates[index]
}
func aacCodecASC(data []byte) (aacCodecConfig, bool) {
	var c aacCodecConfig
	if len(data) < 2 || len(data) > 64 {
		return c, false
	}
	bits := aacCodecBits{data: data, ok: true}
	outer := bits.take(5)
	if outer != 2 && outer != 5 && outer != 29 {
		return c, false
	}
	c.coreRate = bits.rate()
	c.outputRate = c.coreRate
	c.channels = bits.take(4)
	if c.channels == 0 || c.channels > 7 {
		return c, false
	}
	if outer == 5 || outer == 29 {
		c.outputRate = bits.rate()
		if bits.take(5) != 2 {
			return c, false
		}
		c.ps = outer == 29
	}
	// frameLengthFlag may select1024 or960; dependency/PCE and extensionFlag
	// constructions need more grammar than this bounded classification supports.
	if bits.take(3)&3 != 0 {
		return c, false
	}
	if outer == 2 && len(data)*8-bits.pos > 7 {
		if bits.take(11) != 0x2b7 || bits.take(5) != 5 {
			return c, false
		}
		sbr := bits.take(1)
		if sbr != 0 {
			c.outputRate = bits.rate()
			if len(data)*8-bits.pos > 7 {
				if bits.take(11) != 0x548 {
					return c, false
				}
				c.ps = bits.take(1) != 0
			}
		}
	}
	if !bits.ok || c.coreRate == 0 || (c.outputRate != c.coreRate && c.outputRate != 2*c.coreRate) || c.ps && c.channels != 1 || len(data)*8-bits.pos > 7 {
		return c, false
	}
	for bits.pos < len(data)*8 {
		if bits.take(1) != 0 {
			return c, false
		}
	}
	if !bits.ok {
		return c, false
	}
	if c.channels == 7 {
		c.channels = 8
	}
	return c, true
}
