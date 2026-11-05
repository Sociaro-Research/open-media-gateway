"""Validate positive embedded PNG browser fixtures without external decoders."""
import base64
from pathlib import Path
import re
import struct
import unittest
import zlib


ROOT = Path(__file__).resolve().parents[2]
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
# Covers Buffer.from literals and data:image/png URLs; negative cases live below.
PNG_LITERAL = re.compile(r"iVBORw0KGgo[A-Za-z0-9+/=]+")
VALID_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII=")


def validate_png(data):
    """Check the noninterlaced PNG formats used by the synthetic UI fixtures."""
    if not data.startswith(PNG_SIGNATURE):
        raise ValueError("Invalid PNG signature")
    offset, chunks, compressed, ended = 8, [], bytearray(), False
    while offset < len(data):
        if len(data) - offset < 12:
            raise ValueError("Truncated PNG chunk header or CRC")
        size = struct.unpack_from(">I", data, offset)[0]
        end = offset + 12 + size
        if end > len(data):
            raise ValueError("Truncated PNG chunk payload or CRC")
        kind = data[offset + 4:offset + 8]
        payload = data[offset + 8:end - 4]
        stored_crc = struct.unpack_from(">I", data, end - 4)[0]
        if stored_crc != zlib.crc32(kind + payload) & 0xFFFFFFFF:
            raise ValueError("Invalid " + kind.decode("ascii", errors="replace") + " CRC")
        if not chunks and (kind != b"IHDR" or size != 13):
            raise ValueError("PNG must begin with a 13-byte IHDR")
        if kind == b"IHDR":
            if chunks:
                raise ValueError("Repeated IHDR")
            width, height, depth, color, compression, filtering, interlace = struct.unpack(">IIBBBBB", payload)
            allowed = {0: (1, 2, 4, 8, 16), 2: (8, 16), 3: (1, 2, 4, 8), 4: (8, 16), 6: (8, 16)}
            if not width or not height or depth not in allowed.get(color, ()):
                raise ValueError("Unsupported PNG dimensions or color format")
            if compression or filtering or interlace:
                raise ValueError("Fixture must use standard noninterlaced PNG encoding")
        elif kind == b"IDAT":
            if b"IDAT" in chunks and chunks[-1] != b"IDAT":
                raise ValueError("Noncontiguous IDAT chunks")
            compressed.extend(payload)
        elif kind == b"IEND":
            if size or end != len(data):
                raise ValueError("Invalid IEND or trailing PNG bytes")
            ended = True
        chunks.append(kind)
        offset = end
    if not ended or not compressed:
        raise ValueError("Missing IDAT or IEND")
    if color == 3 and b"PLTE" not in chunks:
        raise ValueError("Indexed PNG requires a palette")
    try:
        decoder = zlib.decompressobj()
        pixels = decoder.decompress(compressed) + decoder.flush()
    except zlib.error as error:
        raise ValueError("Invalid PNG zlib payload") from error
    if not decoder.eof or decoder.unused_data or decoder.unconsumed_tail:
        raise ValueError("Incomplete or trailing PNG zlib payload")
    channels = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[color]
    stride = 1 + (width * channels * depth + 7) // 8
    if len(pixels) != height * stride or any(pixels[row * stride] > 4 for row in range(height)):
        raise ValueError("Invalid PNG scanline length or filter")
    return width, height, pixels


def png_with_pixels(pixels):
    """Build a valid-CRC fixture with controlled compressed scanline contents."""
    def chunk(kind, payload):
        return struct.pack(">I", len(payload)) + kind + payload + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
    return PNG_SIGNATURE + chunk(b"IHDR", struct.pack(">IIBBBBB", 1, 1, 8, 4, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(pixels)) + chunk(b"IEND", b"")


class BrowserPNGFixtures(unittest.TestCase):
    def test_positive_browser_png_literals(self):
        found = 0
        for path in sorted((ROOT / "scripts/ui").glob("*.cjs")):
            source = path.read_text()
            for match in PNG_LITERAL.finditer(source):
                found += 1
                with self.subTest(path=path.relative_to(ROOT), line=source.count("\n", 0, match.start()) + 1):
                    validate_png(base64.b64decode(match.group(), validate=True))
        self.assertGreater(found, 0, "The browser PNG inventory must not be empty")

    def test_valid_fixture_preserves_pixel_payload(self):
        self.assertEqual(validate_png(VALID_PNG), (1, 1, b"\x01\xff\xff"))

    def test_old_invalid_crc_variants_are_rejected(self):
        for crc in (0xEF9A4F4B, 0xEFA34599, 0xEF9A3F5B):
            damaged = bytearray(VALID_PNG)
            damaged[52:56] = struct.pack(">I", crc)
            with self.subTest(crc=hex(crc)), self.assertRaisesRegex(ValueError, "IDAT CRC"):
                validate_png(bytes(damaged))

    def test_truncated_chunks_are_rejected(self):
        for data in (VALID_PNG[:12], VALID_PNG[:55], VALID_PNG[:-1]):
            with self.subTest(length=len(data)), self.assertRaisesRegex(ValueError, "Truncated"):
                validate_png(data)

    def test_invalid_signature_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "signature"):
            validate_png(b"not PNG" + VALID_PNG[7:])

    def test_scanline_length_and_filter_are_checked(self):
        for pixels in (b"\x00\xff", b"\x00\xff\xff\xff", b"\x05\xff\xff"):
            with self.subTest(pixels=pixels), self.assertRaisesRegex(ValueError, "scanline"):
                validate_png(png_with_pixels(pixels))

    def test_missing_iend_and_trailing_bytes_are_rejected(self):
        for data in (VALID_PNG[:-12], VALID_PNG + b"extra"):
            with self.subTest(length=len(data)), self.assertRaises(ValueError):
                validate_png(data)


if __name__ == "__main__":
    unittest.main()
