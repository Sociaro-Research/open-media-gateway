package mediafile

import (
	"bytes"
	"encoding/binary"
	"math"
)

func flac(data []byte, info *Info) error {
	if len(data) < 42 || string(data[:4]) != "fLaC" || data[4]&127 != 0 || int(data[5])<<16|int(data[6])<<8|int(data[7]) != 34 {
		return ErrInvalid
	}
	packed := binary.BigEndian.Uint64(data[18:26])
	rate := packed >> 44
	samples := packed & ((1 << 36) - 1)
	if rate == 0 || samples == 0 {
		return ErrInvalid
	}
	info.Duration = float64(samples) / float64(rate)
	info.Codec = "flac"
	return nil
}
func ogg(data []byte, info *Info) error {
	var serial uint32
	var granule uint64
	var rate uint64
	var skip uint16
	found := false
	for pos := 0; pos < len(data); {
		if len(data)-pos < 27 || string(data[pos:pos+4]) != "OggS" || data[pos+4] != 0 {
			return ErrInvalid
		}
		n := int(data[pos+26])
		if len(data)-pos < 27+n {
			return ErrInvalid
		}
		size := 0
		for _, v := range data[pos+27 : pos+27+n] {
			size += int(v)
		}
		head := pos + 27 + n
		if size > len(data)-head {
			return ErrInvalid
		}
		body := data[head : head+size]
		s := binary.LittleEndian.Uint32(data[pos+14 : pos+18])
		if !found {
			switch {
			case len(body) >= 19 && bytes.HasPrefix(body, []byte("OpusHead")):
				rate = 48000
				skip = binary.LittleEndian.Uint16(body[10:12])
				info.Codec = "opus"
			case len(body) >= 30 && bytes.HasPrefix(body, []byte("\x01vorbis")):
				rate = uint64(binary.LittleEndian.Uint32(body[12:16]))
				info.Codec = "vorbis"
			default:
				return ErrInvalid
			}
			serial = s
			found = true
		}
		if s == serial {
			g := binary.LittleEndian.Uint64(data[pos+6 : pos+14])
			if g != math.MaxUint64 {
				granule = g
			}
		}
		pos = head + size
	}
	if rate == 0 || granule <= uint64(skip) {
		return ErrInvalid
	}
	info.Duration = float64(granule-uint64(skip)) / float64(rate)
	return nil
}

func vint(data []byte, keepMarker bool) (uint64, int, bool) {
	if len(data) == 0 || data[0] == 0 {
		return 0, 0, false
	}
	n := 1
	mask := byte(0x80)
	for data[0]&mask == 0 {
		n++
		mask >>= 1
	}
	if n > 8 || n > len(data) {
		return 0, 0, false
	}
	value := uint64(data[0])
	if !keepMarker {
		value &= uint64(mask - 1)
	}
	for _, b := range data[1:n] {
		value = value<<8 | uint64(b)
	}
	return value, n, true
}
func ebmlUint(b []byte) uint64 {
	var n uint64
	for _, v := range b {
		n = n<<8 | uint64(v)
	}
	return n
}

// Inspect WebM timing and track metadata without decoding frames or invoking a
// subprocess. Traversal depth and every element boundary are bounded.
func webm(data []byte, info *Info) error {
	scale := uint64(1000000)
	duration := 0.0
	defaultNS := uint64(0)
	videoTrack := uint64(0)
	samples := uint64(0)
	var walk func([]byte, int) error
	walk = func(data []byte, depth int) error {
		if depth > 8 {
			return ErrInvalid
		}
		for pos := 0; pos < len(data); {
			id, n, ok := vint(data[pos:], true)
			if !ok {
				return ErrInvalid
			}
			pos += n
			size, k, ok := vint(data[pos:], false)
			if !ok {
				return ErrInvalid
			}
			pos += k
			if size == (uint64(1)<<(7*k))-1 {
				size = uint64(len(data) - pos)
			}
			if size > uint64(len(data)-pos) {
				return ErrInvalid
			}
			b := data[pos : pos+int(size)]
			pos += int(size)
			switch id {
			case 0x18538067, 0x1549a966, 0x1654ae6b, 0xe0, 0x1f43b675, 0xa0:
				if err := walk(b, depth+1); err != nil {
					return err
				}
			case 0xae:
				// Only video TrackEntry metadata contributes dimensions/default cadence.
				track, kind := uint64(0), uint64(0)
				for p := 0; p < len(b); {
					eid, en, good := vint(b[p:], true)
					if !good {
						return ErrInvalid
					}
					p += en
					es, ek, good := vint(b[p:], false)
					if !good {
						return ErrInvalid
					}
					p += ek
					if es > uint64(len(b)-p) {
						return ErrInvalid
					}
					v := b[p : p+int(es)]
					p += int(es)
					if eid == 0xd7 {
						track = ebmlUint(v)
					}
					if eid == 0x83 {
						kind = ebmlUint(v)
					}
				}
				if kind == 1 && videoTrack == 0 {
					videoTrack = track
					if err := walk(b, depth+1); err != nil {
						return err
					}
				}
			case 0x2ad7b1:
				scale = ebmlUint(b)
			case 0x4489:
				if len(b) == 4 {
					duration = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
				} else if len(b) == 8 {
					duration = math.Float64frombits(binary.BigEndian.Uint64(b))
				} else {
					return ErrInvalid
				}
			case 0xb0:
				info.Width = int(ebmlUint(b))
			case 0xba:
				info.Height = int(ebmlUint(b))
			case 0x23e383:
				defaultNS = ebmlUint(b)
			case 0x86:
				info.Codec = string(b)
			case 0xa3, 0xa1:
				track, n, ok := vint(b, false)
				if !ok || len(b) < n+3 {
					return ErrInvalid
				}
				if track == videoTrack {
					count := uint64(1)
					if b[n+2]&6 != 0 {
						if len(b) < n+4 {
							return ErrInvalid
						}
						count += uint64(b[n+3])
					}
					samples += count
				}
			}
		}
		return nil
	}
	if err := walk(data, 0); err != nil {
		return err
	}
	info.Duration = duration * float64(scale) / 1e9
	if info.Duration > 0 && samples > 0 {
		info.FPS = float64(samples) / info.Duration
	} else if defaultNS > 0 {
		info.FPS = 1e9 / float64(defaultNS)
	}
	if videoTrack == 0 || info.Width < 1 || info.Height < 1 || info.Duration <= 0 || info.FPS <= 0 {
		return ErrInvalid
	}
	return nil
}
