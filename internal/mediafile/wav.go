package mediafile

import (
	"bytes"
	"encoding/binary"
)

func wav(data []byte, info *Info) error {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return ErrInvalid
	}
	var format []byte
	var size, dataAlignment uint64
	// Inspect every physical chunk, retaining the existing tolerance of RIFF
	// length metadata. Never omit trailing data chunks from duration accounting.
	for pos := 12; pos < len(data); {
		if len(data)-pos < 8 {
			return ErrInvalid
		}
		n := uint64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		if n > uint64(len(data)-pos-8) {
			return ErrInvalid
		}
		end := pos + 8 + int(n)
		switch string(data[pos : pos+4]) {
		case "fmt ":
			if format != nil {
				return ErrInvalid
			}
			format = data[pos+8 : end]
		case "data":
			size += n
			// The GCD allows checking every chunk's alignment even if fmt
			// follows data, without allocating an entry for each chunk.
			a, b := dataAlignment, n
			for b != 0 {
				a, b = b, a%b
			}
			dataAlignment = a
		}
		pos = end
		if n%2 != 0 && pos < len(data) {
			pos++ // RIFF chunks are word-aligned; tolerate an unpadded EOF.
		}
	}
	if len(format) < 16 || size == 0 {
		return ErrInvalid
	}
	rate, align, fixed, err := wavFormat(format)
	if err != nil || fixed && dataAlignment%align != 0 {
		return ErrInvalid
	}
	info.Duration = float64(size) / float64(rate)
	return nil
}

// WAVEFORMATEX defines PCM/float byte rates from their sample containers;
// valid precision bits in extensible PCM must not replace the container size.
// https://learn.microsoft.com/en-us/windows/win32/api/mmreg/ns-mmreg-waveformatex
// https://learn.microsoft.com/en-us/windows/win32/api/mmreg/ns-mmreg-waveformatextensible
func wavFormat(format []byte) (rate, align uint64, fixed bool, err error) {
	if len(format) < 16 {
		return 0, 0, false, ErrInvalid
	}
	tag := uint32(binary.LittleEndian.Uint16(format))
	channels := uint64(binary.LittleEndian.Uint16(format[2:]))
	samples := uint64(binary.LittleEndian.Uint32(format[4:]))
	rate = uint64(binary.LittleEndian.Uint32(format[8:]))
	align = uint64(binary.LittleEndian.Uint16(format[12:]))
	bits := uint64(binary.LittleEndian.Uint16(format[14:]))
	if tag == 0 || channels == 0 || samples == 0 || rate == 0 || align == 0 || len(format) == 17 {
		return 0, 0, false, ErrInvalid
	}
	// PCM predates cbSize and explicitly ignores it. Other formats may carry
	// codec-specific extensions, which must fit inside their fmt chunk.
	if tag != 1 && len(format) >= 18 && int(binary.LittleEndian.Uint16(format[16:])) > len(format)-18 {
		return 0, 0, false, ErrInvalid
	}
	if tag == 0xfffe {
		if len(format) < 40 || binary.LittleEndian.Uint16(format[16:]) < 22 {
			return 0, 0, false, ErrInvalid
		}
		// Match the complete registered subtype, not just a GUID's low word.
		if !bytes.Equal(format[28:40], []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}) {
			return rate, align, false, nil
		}
		tag = binary.LittleEndian.Uint32(format[24:28])
		if tag == 1 || tag == 3 {
			valid := uint64(binary.LittleEndian.Uint16(format[18:20]))
			if valid > bits || tag == 3 && valid != 0 && valid != bits {
				return 0, 0, false, ErrInvalid
			}
		}
	}
	switch tag {
	case 1: // Integer PCM; retain byte-aligned 24/32-bit and multichannel files.
		if bits == 0 || bits%8 != 0 {
			return 0, 0, false, ErrInvalid
		}
	case 3: // IEEE float.
		if bits != 32 && bits != 64 {
			return 0, 0, false, ErrInvalid
		}
	case 6, 7: // A-law and mu-law: one byte per channel sample.
		if bits != 8 {
			return 0, 0, false, ErrInvalid
		}
	default:
		// Keep the existing average-rate estimate for compressed formats.
		// Their block layout cannot be derived using PCM equations.
		return rate, align, false, nil
	}
	if channels*(bits/8) != align || samples*align != rate {
		return 0, 0, false, ErrInvalid
	}
	return rate, align, true, nil
}
