package images

import (
	"bytes"
	"encoding/binary"
)

// StripMetadata removes what a camera or editor wrote next to the pixels
// (EXIF with GPS position and device, XMP, IPTC, comments, text chunks) from
// a JPEG, PNG or WebP without re-encoding it, so the stored original is the
// same picture byte for byte. A JPEG keeps its EXIF orientation (as a minimal
// EXIF block holding nothing else), its colour profile and the Adobe marker,
// since those change how it is displayed. Data it cannot parse, and any
// other format, comes back unchanged.
func StripMetadata(data []byte) []byte {
	var out []byte
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8}):
		out = stripJPEG(data)
	case bytes.HasPrefix(data, []byte(pngMagic)):
		out = stripPNG(data)
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		out = stripWebP(data)
	}
	if out == nil {
		return data
	}
	return out
}

// JPEG

const (
	jpegSOI   = 0xD8
	jpegEOI   = 0xD9
	jpegSOS   = 0xDA
	jpegTEM   = 0x01
	jpegCOM   = 0xFE
	jpegAPP0  = 0xE0
	jpegAPP1  = 0xE1
	jpegAPP2  = 0xE2
	jpegAPP14 = 0xEE
	jpegAPP15 = 0xEF
)

var (
	exifHeader = []byte("Exif\x00\x00")
	iccHeader  = []byte("ICC_PROFILE\x00")
)

// stripJPEG walks the segments up to the end-of-image marker; whatever
// follows it (a trailer such as an embedded video or a second picture) is
// dropped. nil means the data is not a well-formed JPEG.
func stripJPEG(data []byte) []byte {
	out := make([]byte, 0, len(data))
	out = append(out, 0xFF, jpegSOI)
	oriented := false
	i := 2
	for {
		// a marker is 0xFF, any number of 0xFF fill bytes, then the code
		if i >= len(data) || data[i] != 0xFF {
			return nil
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return nil
		}
		m := data[i]
		i++
		switch {
		case m == jpegEOI:
			return append(out, 0xFF, jpegEOI)
		case m == jpegTEM || (m >= 0xD0 && m <= 0xD7): // no payload
			out = append(out, 0xFF, m)
			continue
		case m == 0x00 || m == jpegSOI:
			return nil
		}
		if i+2 > len(data) {
			return nil
		}
		n := int(binary.BigEndian.Uint16(data[i:]))
		if n < 2 || i+n > len(data) {
			return nil
		}
		payload := data[i+2 : i+n]
		i += n // fill bytes before the marker are not copied
		switch {
		case m == jpegAPP1 && bytes.HasPrefix(payload, exifHeader):
			if o := exifOrientation(payload[len(exifHeader):]); o > 1 && !oriented {
				out = append(out, orientationSegment(o)...)
				oriented = true
			}
		case m == jpegCOM, m >= jpegAPP1 && m <= jpegAPP15 && m != jpegAPP14 && (m != jpegAPP2 || !bytes.HasPrefix(payload, iccHeader)):
			// XMP, IPTC (APP13), maker and vendor blocks, comments
		default:
			out = append(out, 0xFF, m)
			out = append(out, data[i-n:i]...)
		}
		if m != jpegSOS {
			continue
		}
		// entropy-coded data runs to the next real marker: 0xFF followed by
		// anything but a stuffed zero, a restart marker or more fill
		start := i
		for {
			j := bytes.IndexByte(data[i:], 0xFF)
			if j < 0 || i+j+1 >= len(data) {
				return nil
			}
			i += j
			if next := data[i+1]; next == 0x00 || next == 0xFF || (next >= 0xD0 && next <= 0xD7) {
				i++
				if next != 0xFF {
					i++
				}
				continue
			}
			break
		}
		out = append(out, data[start:i]...)
	}
}

// exifOrientation reads tag 0x0112 of the first image directory of a TIFF
// block, or 0.
func exifOrientation(tiff []byte) uint16 {
	if len(tiff) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(tiff[2:]) != 42 {
		return 0
	}
	off := int(bo.Uint32(tiff[4:]))
	if off < 8 || off+2 > len(tiff) {
		return 0
	}
	count := int(bo.Uint16(tiff[off:]))
	for e := off + 2; count > 0 && e+12 <= len(tiff); e, count = e+12, count-1 {
		if bo.Uint16(tiff[e:]) != 0x0112 {
			continue
		}
		if bo.Uint16(tiff[e+2:]) != 3 || bo.Uint32(tiff[e+4:]) != 1 { // one SHORT
			return 0
		}
		if o := bo.Uint16(tiff[e+8:]); o >= 1 && o <= 8 {
			return o
		}
		return 0
	}
	return 0
}

// orientationSegment is an EXIF block with the orientation tag and nothing else.
func orientationSegment(o uint16) []byte {
	return []byte{
		0xFF, jpegAPP1, 0x00, 34, // length counts itself and the 32 bytes below
		'E', 'x', 'i', 'f', 0, 0,
		'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08, // big endian, directory at 8
		0x00, 0x01, // one entry
		0x01, 0x12, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01, 0x00, byte(o & 0x0F), 0x00, 0x00, // 1..8
		0x00, 0x00, 0x00, 0x00, // no further directory
	}
}

// PNG

const pngMagic = "\x89PNG\r\n\x1a\n"

// stripPNG drops the text, EXIF and timestamp chunks; nothing after IEND is kept.
func stripPNG(data []byte) []byte {
	out := make([]byte, 0, len(data))
	out = append(out, pngMagic...)
	for i := len(pngMagic); ; {
		if i+12 > len(data) {
			return nil
		}
		n := int(binary.BigEndian.Uint32(data[i:]))
		if n < 0 || i+12+n > len(data) {
			return nil
		}
		kind := string(data[i+4 : i+8])
		chunk := data[i : i+12+n]
		i += 12 + n
		switch kind {
		case "tEXt", "zTXt", "iTXt", "eXIf", "tIME":
			continue
		}
		out = append(out, chunk...)
		if kind == "IEND" {
			return out
		}
	}
}

// WebP

// stripWebP drops the EXIF and XMP chunks of an extended (VP8X) file and
// clears their flags in its header.
func stripWebP(data []byte) []byte {
	out := make([]byte, 0, len(data))
	out = append(out, data[:12]...)
	end := 8 + int(binary.LittleEndian.Uint32(data[4:]))
	if end > len(data) || end < 12 {
		return nil
	}
	for i := 12; i < end; {
		if i+8 > end {
			return nil
		}
		kind := string(data[i : i+4])
		n := int(binary.LittleEndian.Uint32(data[i+4:]))
		size := 8 + n + n&1 // chunks are padded to an even length
		if n < 0 || i+8+n > end {
			return nil
		}
		if i+size > end { // a final chunk without its padding byte
			size = end - i
		}
		chunk := data[i : i+size]
		i += size
		switch kind {
		case "EXIF", "XMP ":
			continue
		case "VP8X":
			if n < 1 {
				return nil
			}
			at := len(out) + 8
			out = append(out, chunk...)
			out[at] &^= 0x08 | 0x04 // EXIF, XMP
			continue
		}
		out = append(out, chunk...)
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8)) //nolint:gosec // no longer than the input, whose size fitted
	return out
}
