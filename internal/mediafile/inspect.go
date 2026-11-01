// Package mediafile inspects bounded, already owner-authorized uploads without
// shelling out, fetching URLs or decoding full-resolution untrusted images.
package mediafile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strings"
)

var ErrInvalid = errors.New("media cannot be inspected: use a valid PNG, JPEG, GIF, WebP, still HEIC/HEIF, MP4/MOV/WebM, MP3, WAV, AAC, M4A, OGG or FLAC reference file")

type Info struct {
	// AudioCodecs follows declared audio track order; an empty entry is unknown.
	AudioCodecs []string

	// FrameCount is the primary video sample count; zero means unavailable.
	FrameCount int64
	// Track counts are nil when container metadata cannot establish them.
	// A non-nil zero means that no stream of that kind was declared.
	AudioStreams  *int
	VideoStreams  *int
	AlphaChannel  bool
	Codec         string
	ContentType   string
	Size          int64
	Width, Height int
	Duration, FPS float64
}

func Inspect(data []byte, kind string) (Info, error) {
	kind = CanonicalContentType(kind)
	result := Info{ContentType: kind, Size: int64(len(data))}
	switch {
	case strings.HasPrefix(kind, "image/"):
		if kind == "image/heic" {
			if err := inspectHEIF(data, &result); err != nil {
				return result, err
			}
		} else if kind == "image/webp" {
			if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
				return result, ErrInvalid
			}
			switch string(data[12:16]) {
			case "VP8X":
				result.AlphaChannel = data[20]&0x10 != 0
				result.Width = 1 + int(data[24]) + int(data[25])<<8 + int(data[26])<<16
				result.Height = 1 + int(data[27]) + int(data[28])<<8 + int(data[29])<<16
			case "VP8 ":
				if !bytes.Equal(data[23:26], []byte{0x9d, 1, 0x2a}) {
					return result, ErrInvalid
				}
				result.Width = int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
				result.Height = int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
			case "VP8L":
				if data[20] != 0x2f {
					return result, ErrInvalid
				}
				bits := binary.LittleEndian.Uint32(data[21:25])
				result.AlphaChannel = bits&(1<<28) != 0
				result.Width = int(bits&0x3fff) + 1
				result.Height = int((bits>>14)&0x3fff) + 1
			default:
				return result, ErrInvalid
			}
		} else {
			config, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return result, ErrInvalid
			}
			result.Width, result.Height = config.Width, config.Height
			if kind == "image/png" {
				if len(data) < 26 {
					return result, ErrInvalid
				}
				result.AlphaChannel = data[25] == 4 || data[25] == 6
				for pos := 8; pos+12 <= len(data); {
					n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
					if n > len(data)-pos-12 {
						return result, ErrInvalid
					}
					if string(data[pos+4:pos+8]) == "tRNS" {
						result.AlphaChannel = true
					}
					pos += n + 12
				}
			}
		}
		if result.Width < 1 || result.Height < 1 || result.Width > 100000 || result.Height > 100000 {
			return result, ErrInvalid
		}
	case kind == "video/mp4" || kind == "video/quicktime":
		if err := mp4(data, &result); err != nil {
			return result, err
		}
		mp4AudioCodecEvidence(data, &result)
	case kind == "video/webm":
		if err := webm(data, &result); err != nil {
			return result, err
		}
	case kind == "audio/aac":
		if err := adtsAAC(data, &result); err != nil {
			return result, err
		}
	case kind == "audio/mp4":
		if err := audioMP4(data, &result); err != nil {
			return result, err
		}
		if top, err := boxes(data); err == nil {
			for _, b := range top {
				if b.kind == "moov" {
					if children, err := boxes(b.data); err == nil {
						mp4StreamCounts(children, &result)
					}
					break
				}
			}
		}
		mp4AudioCodecEvidence(data, &result)
	case kind == "audio/ogg":
		if err := ogg(data, &result); err != nil {
			return result, err
		}
	case kind == "audio/flac":
		if err := flac(data, &result); err != nil {
			return result, err
		}
	case kind == "audio/wave" || kind == "audio/wav" || kind == "audio/x-wav":
		if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
			return result, ErrInvalid
		}
		var rate uint32
		var size int
		for pos := 12; pos+8 <= len(data); {
			n := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
			if n < 0 || n > len(data)-pos-8 {
				return result, ErrInvalid
			}
			body := data[pos+8 : pos+8+n]
			switch string(data[pos : pos+4]) {
			case "fmt ":
				if len(body) < 16 {
					return result, ErrInvalid
				}
				rate = binary.LittleEndian.Uint32(body[8:12])
			case "data":
				size += n
			}
			pos += 8 + n + n%2
		}
		if rate == 0 || size == 0 {
			return result, ErrInvalid
		}
		result.Duration = float64(size) / float64(rate)
	case kind == "audio/mpeg":
		duration, e := mp3(data)
		if e != nil {
			return result, e
		}
		result.Duration = duration
	default:
		return result, ErrInvalid
	}
	if strings.HasPrefix(kind, "audio/") || strings.HasPrefix(kind, "video/") {
		if result.Duration <= 0 || math.IsNaN(result.Duration) || math.IsInf(result.Duration, 0) {
			return result, ErrInvalid
		}
	}
	if strings.HasPrefix(kind, "video/") && (result.FPS <= 0 || math.IsNaN(result.FPS) || math.IsInf(result.FPS, 0)) {
		return result, ErrInvalid
	}
	return result, nil
}

type box struct {
	kind string
	data []byte
}

func boxes(data []byte) ([]box, error) {
	result := []box{}
	for pos := 0; pos < len(data); {
		if len(data)-pos < 8 {
			return nil, ErrInvalid
		}
		size := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
		head := 8
		if size == 1 {
			if len(data)-pos < 16 {
				return nil, ErrInvalid
			}
			size = binary.BigEndian.Uint64(data[pos+8 : pos+16])
			head = 16
		} else if size == 0 {
			size = uint64(len(data) - pos)
		}
		if size < uint64(head) || size > uint64(len(data)-pos) {
			return nil, ErrInvalid
		}
		result = append(result, box{string(data[pos+4 : pos+8]), data[pos+head : pos+int(size)]})
		pos += int(size)
	}
	return result, nil
}
func mp4(data []byte, info *Info) error {
	top, e := boxes(data)
	if e != nil {
		return e
	}
	var moov []byte
	for _, b := range top {
		if b.kind == "moov" {
			moov = b.data
		}
	}
	if moov == nil {
		return ErrInvalid
	}
	children, e := boxes(moov)
	if e != nil {
		return e
	}
	for _, track := range children {
		if track.kind != "trak" {
			continue
		}
		parts, e := boxes(track.data)
		if e != nil {
			return e
		}
		var width, height int
		var duration float64
		var frames, sampleTicks uint64
		var timescale uint32
		video := false
		codec := ""
		for _, part := range parts {
			switch part.kind {
			case "tkhd":
				if len(part.data) < 84 {
					return ErrInvalid
				}
				width = int(binary.BigEndian.Uint32(part.data[len(part.data)-8:]) >> 16)
				height = int(binary.BigEndian.Uint32(part.data[len(part.data)-4:]) >> 16)
			case "mdia":
				mdia, e := boxes(part.data)
				if e != nil {
					return e
				}
				for _, m := range mdia {
					switch m.kind {
					case "hdlr":
						if len(m.data) >= 12 {
							video = string(m.data[8:12]) == "vide"
						}
					case "mdhd":
						offset := 12
						if len(m.data) < 24 {
							return ErrInvalid
						}
						if m.data[0] == 1 {
							offset = 20
						}
						if len(m.data) < offset+8 {
							return ErrInvalid
						}
						scale := binary.BigEndian.Uint32(m.data[offset : offset+4])
						timescale = scale
						value := uint64(binary.BigEndian.Uint32(m.data[offset+4 : offset+8]))
						if m.data[0] == 1 {
							if len(m.data) < offset+12 {
								return ErrInvalid
							}
							value = binary.BigEndian.Uint64(m.data[offset+4 : offset+12])
						}
						if scale == 0 {
							return ErrInvalid
						}
						duration = float64(value) / float64(scale)
					case "minf":
						minf, e := boxes(m.data)
						if e != nil {
							return e
						}
						for _, n := range minf {
							if n.kind != "stbl" {
								continue
							}
							stbl, e := boxes(n.data)
							if e != nil {
								return e
							}
							for _, s := range stbl {
								if s.kind == "stsd" && len(s.data) >= 16 {
									codec = string(s.data[12:16])
								}
								if s.kind != "stts" {
									continue
								}
								if len(s.data) < 8 {
									return ErrInvalid
								}
								count := int(binary.BigEndian.Uint32(s.data[4:8]))
								if count > (len(s.data)-8)/8 {
									return ErrInvalid
								}
								for i := 0; i < count; i++ {
									samples := uint64(binary.BigEndian.Uint32(s.data[8+i*8 : 12+i*8]))
									delta := uint64(binary.BigEndian.Uint32(s.data[12+i*8 : 16+i*8]))
									if samples == 0 || delta == 0 || ^uint64(0)-sampleTicks < samples*delta {
										return ErrInvalid
									}
									if ^uint64(0)-frames < samples {
										return ErrInvalid
									}
									frames += samples
									sampleTicks += samples * delta
								}
							}
						}
					}
				}
			}
		}
		if video {
			if width < 1 || height < 1 || duration <= 0 || frames == 0 || sampleTicks == 0 || timescale == 0 {
				return ErrInvalid
			}
			// mdhd duration can include a container tail not represented by video
			// samples. stts supplies each sample's actual decoding interval; use
			// its weighted duration for average FPS, without rounding a nominal
			// rate. Keep the longer duration for model duration/total limits, so an
			// understated header cannot hide an overlong sample timeline.
			duration = math.Max(duration, float64(sampleTicks)/float64(timescale))
			info.Width, info.Height, info.Duration, info.FPS = width, height, duration, float64(frames)*float64(timescale)/float64(sampleTicks)
			if frames > math.MaxInt64 {
				return ErrInvalid
			}
			info.FrameCount = int64(frames)
			mp4StreamCounts(children, info)
			info.Codec = codec
			return nil
		}
	}
	return ErrInvalid
}

// Count declared stream handlers only when every track has unambiguous
// metadata. An unreadable later track must not be mistaken for proven silence.
// This does not change the existing primary-video acceptance contract.
func mp4StreamCounts(children []box, info *Info) {
	audio, video, tracks := 0, 0, 0
	for _, track := range children {
		if track.kind != "trak" {
			continue
		}
		tracks++
		parts, err := boxes(track.data)
		if err != nil {
			return
		}
		handler := ""
		handlers := 0
		for _, part := range parts {
			if part.kind != "mdia" {
				continue
			}
			media, err := boxes(part.data)
			if err != nil {
				return
			}
			for _, entry := range media {
				if entry.kind != "hdlr" {
					continue
				}
				if len(entry.data) < 12 {
					return
				}
				handlers++
				handler = string(entry.data[8:12])
			}
		}
		if handlers != 1 {
			return
		}
		switch handler {
		case "soun":
			audio++
		case "vide":
			video++
		}
	}
	if tracks == 0 {
		return
	}
	info.AudioStreams = &audio
	info.VideoStreams = &video
}
func mp3(data []byte) (float64, error) {
	pos := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		size := 0
		for _, v := range data[6:10] {
			if v > 127 {
				return 0, ErrInvalid
			}
			size = size<<7 | int(v)
		}
		pos = 10 + size
		if data[5]&0x10 != 0 {
			pos += 10
		}
	}
	frames := 0
	duration := 0.0
	for pos+4 <= len(data) {
		if len(data)-pos == 128 && string(data[pos:pos+3]) == "TAG" {
			break
		}
		header := binary.BigEndian.Uint32(data[pos : pos+4])
		if header>>21 != 0x7ff {
			return 0, ErrInvalid
		}
		version := (header >> 19) & 3
		layer := (header >> 17) & 3
		rateIndex := (header >> 10) & 3
		bitIndex := (header >> 12) & 15
		if version == 1 || layer != 1 || rateIndex == 3 || bitIndex == 0 || bitIndex == 15 {
			return 0, ErrInvalid
		}
		rate := []int{44100, 48000, 32000}[rateIndex]
		bitrates := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
		samples, coefficient := 1152, 144
		if version != 3 {
			rate /= 2
			if version == 0 {
				rate /= 2
			}
			bitrates = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
			samples, coefficient = 576, 72
		}
		size := coefficient*bitrates[bitIndex]*1000/rate + int((header>>9)&1)
		if size < 4 || size > len(data)-pos {
			return 0, ErrInvalid
		}
		duration += float64(samples) / float64(rate)
		pos += size
		frames++
	}
	if frames == 0 {
		return 0, ErrInvalid
	}
	return duration, nil
}
