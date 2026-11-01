// Package testmedia supplies tiny synthetic container fixtures for local tests.
// It is imported only by tests; no user media or provider connection is involved.
package testmedia

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
)

func PNG512(seed string) []byte {
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	sum := sha256.Sum256([]byte(seed))
	im.Set(0, 0, color.RGBA{sum[0], sum[1], sum[2], 255})
	_ = png.Encode(&b, im)
	raw := b.Bytes()
	n := 512 - len(raw) - 12
	body := make([]byte, n)
	copy(body, []byte(seed))
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(n))
	chunk.WriteString("teSt")
	chunk.Write(body)
	_ = binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(chunk.Bytes()[4:]))
	out := append([]byte{}, raw[:len(raw)-12]...)
	out = append(out, chunk.Bytes()...)
	return append(out, raw[len(raw)-12:]...)
}
func Atom(kind string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)+8))
	b.WriteString(kind)
	b.Write(data)
	return b.Bytes()
}
func MP4(ticks, scale, frames uint32) []byte {
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[76:], 540<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 960<<16)
	mdhd := make([]byte, 24)
	binary.BigEndian.PutUint32(mdhd[12:], scale)
	binary.BigEndian.PutUint32(mdhd[16:], ticks)
	hdlr := make([]byte, 12)
	copy(hdlr[8:], "vide")
	stts := make([]byte, 16)
	binary.BigEndian.PutUint32(stts[4:], 1)
	binary.BigEndian.PutUint32(stts[8:], frames)
	if frames > 0 {
		binary.BigEndian.PutUint32(stts[12:], ticks/frames)
		if remainder := ticks % frames; remainder != 0 {
			stts = append(stts, make([]byte, 8)...)
			binary.BigEndian.PutUint32(stts[4:], 2)
			binary.BigEndian.PutUint32(stts[8:], frames-remainder)
			binary.BigEndian.PutUint32(stts[16:], remainder)
			binary.BigEndian.PutUint32(stts[20:], ticks/frames+1)
		}
	}
	stsd := make([]byte, 16)
	binary.BigEndian.PutUint32(stsd[4:], 1)
	binary.BigEndian.PutUint32(stsd[8:], 8)
	copy(stsd[12:], "avc1")
	mdia := append(Atom("mdhd", mdhd), Atom("hdlr", hdlr)...)
	stbl := append(Atom("stsd", stsd), Atom("stts", stts)...)
	mdia = append(mdia, Atom("minf", Atom("stbl", stbl))...)
	return append(Atom("ftyp", []byte("isom0000isom")), Atom("moov", Atom("trak", append(Atom("tkhd", tkhd), Atom("mdia", mdia)...)))...)
}
func MP3() []byte { frame := make([]byte, 417); copy(frame, []byte{0xff, 0xfb, 0x90, 0}); return frame }
func WAV() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+8000))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint32(8000))
	_ = binary.Write(&b, binary.LittleEndian, uint32(8000))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(8))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(8000))
	b.Write(make([]byte, 8000))
	return b.Bytes()
}
