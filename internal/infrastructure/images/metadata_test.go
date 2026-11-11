package images

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/gen2brain/webp"
)

func sample() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 40, 24))
	for y := range 24 {
		for x := range 40 {
			img.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 10), 90, 255})
		}
	}
	return img
}

func jpegSegment(marker byte, payload []byte) []byte {
	out := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(out[2:], uint16(len(payload)+2))
	return append(out, payload...)
}

// exifBlock is a little-endian EXIF payload: orientation, then a stand-in
// for the GPS directory.
func exifBlock(orientation uint16) []byte {
	b := []byte("Exif\x00\x00II\x2A\x00\x08\x00\x00\x00\x02\x00")
	b = append(b, 0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, byte(orientation), byte(orientation>>8), 0x00, 0x00)
	b = append(b, 0x25, 0x88, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x26, 0x00, 0x00, 0x00) // GPS IFD pointer
	b = append(b, 0, 0, 0, 0)
	return append(b, []byte("GPS-SECRET-41.3111N-69.2797E")...)
}

func TestStripJPEG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, sample(), nil); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()
	// SOI, [metadata], the rest, then a trailer after EOI
	dirty := append([]byte{}, plain[:2]...)
	dirty = append(dirty, jpegSegment(jpegAPP1, exifBlock(6))...)
	dirty = append(dirty, jpegSegment(jpegAPP1, []byte("http://ns.adobe.com/xap/1.0/\x00<x>XMP-SECRET</x>"))...)
	dirty = append(dirty, jpegSegment(0xED, []byte("Photoshop 3.0\x00IPTC-SECRET"))...)
	dirty = append(dirty, jpegSegment(jpegAPP2, append([]byte("ICC_PROFILE\x00\x01\x01"), "keep-this-profile"...))...)
	dirty = append(dirty, jpegSegment(jpegAPP2, []byte("MPF\x00MPF-SECRET"))...)
	dirty = append(dirty, 0xFF) // fill byte before a marker
	dirty = append(dirty, jpegSegment(jpegCOM, []byte("COMMENT-SECRET"))...)
	dirty = append(dirty, plain[2:]...)
	dirty = append(dirty, []byte("TRAILER-SECRET")...)

	got := StripMetadata(dirty)
	if bytes.Contains(got, []byte("SECRET")) {
		t.Fatalf("metadata survived: %q", got[:min(len(got), 200)])
	}
	if !bytes.Contains(got, []byte("keep-this-profile")) {
		t.Error("colour profile dropped")
	}
	if !bytes.Contains(got, orientationSegment(6)) {
		t.Error("orientation lost")
	}
	if !bytes.HasSuffix(got, []byte{0xFF, jpegEOI}) {
		t.Error("does not end at EOI")
	}
	want, _ := jpeg.Decode(bytes.NewReader(plain))
	have, err := jpeg.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("stripped JPEG does not decode: %v", err)
	}
	if !bytes.Equal(want.(*image.YCbCr).Y, have.(*image.YCbCr).Y) {
		t.Error("pixels changed")
	}
	// an image without metadata comes back equal; upright EXIF adds nothing
	if again := StripMetadata(plain); !bytes.Equal(again, plain) {
		t.Error("clean JPEG changed")
	}
	upright := append(append(append([]byte{}, plain[:2]...), jpegSegment(jpegAPP1, exifBlock(1))...), plain[2:]...)
	if !bytes.Equal(StripMetadata(upright), plain) {
		t.Error("orientation 1 should leave no EXIF block")
	}
}

func TestStripJPEGProgressiveLikeScans(t *testing.T) {
	// two scans with a table segment between them; stuffed bytes, a restart marker and a fill byte inside the data stay
	data := []byte{0xFF, 0xD8}
	data = append(data, jpegSegment(jpegCOM, []byte("SECRET"))...)
	data = append(data, jpegSegment(jpegSOS, []byte{1, 2, 3})...)
	data = append(data, 0x11, 0xFF, 0x00, 0x22, 0xFF, 0xD0, 0x33)
	data = append(data, jpegSegment(0xC4, []byte{9, 9})...)
	data = append(data, jpegSegment(jpegSOS, []byte{4})...)
	data = append(data, 0x44, 0xFF, 0xFF, 0xD9)
	want := append([]byte{0xFF, 0xD8}, data[2+len(jpegSegment(jpegCOM, []byte("SECRET"))):]...)
	if got := StripMetadata(data); !bytes.Equal(got, want) {
		t.Errorf("got  % x\nwant % x", got, want)
	}
}

func TestStripMalformedIsUnchanged(t *testing.T) {
	for name, data := range map[string][]byte{
		"jpeg cut in a segment": {0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x40, 1, 2},
		"jpeg no marker":        {0xFF, 0xD8, 0x00, 0x00},
		"jpeg bad length":       {0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x01},
		"jpeg cut in a scan":    append([]byte{0xFF, 0xD8}, append(jpegSegment(jpegSOS, []byte{1}), 0x10, 0x20)...),
		"jpeg ends on fill":     {0xFF, 0xD8, 0xFF, 0xFF},
		"jpeg nested SOI":       {0xFF, 0xD8, 0xFF, 0xD8},
		"png cut":               append([]byte(pngMagic), 0, 0, 0, 9, 'I', 'H', 'D', 'R'),
		"webp cut":              []byte("RIFF\xff\x00\x00\x00WEBPVP8 "),
		"webp bad chunk":        []byte("RIFF\x10\x00\x00\x00WEBPVP8X\xff\x00\x00\x00"),
		"webp short chunk":      []byte("RIFF\x08\x00\x00\x00WEBPVP8X"),
		"webp empty header":     []byte("RIFF\x0c\x00\x00\x00WEBPVP8X\x00\x00\x00\x00"),
		"other":                 []byte("GIF89a...."),
	} {
		if got := StripMetadata(data); !bytes.Equal(got, data) {
			t.Errorf("%s: changed to % x", name, got)
		}
	}
}

func TestExifOrientation(t *testing.T) {
	be := []byte("MM\x00\x2A\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x08\x00\x00\x00\x00\x00\x00")
	if o := exifOrientation(be); o != 8 {
		t.Errorf("big endian orientation = %d", o)
	}
	for name, tiff := range map[string][]byte{
		"short":         []byte("MM"),
		"byte order":    []byte("XX\x00\x2A\x00\x00\x00\x08"),
		"magic":         []byte("MM\x00\x2B\x00\x00\x00\x08"),
		"offset":        []byte("MM\x00\x2A\x00\x00\x00\x7f"),
		"no such tag":   []byte("MM\x00\x2A\x00\x00\x00\x08\x00\x01\x01\x0f\x00\x03\x00\x00\x00\x01\x00\x08\x00\x00"),
		"wrong type":    []byte("MM\x00\x2A\x00\x00\x00\x08\x00\x01\x01\x12\x00\x04\x00\x00\x00\x01\x00\x08\x00\x00"),
		"out of range":  []byte("MM\x00\x2A\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x09\x00\x00"),
		"cut directory": []byte("MM\x00\x2A\x00\x00\x00\x08\x00\x01\x01\x12"),
	} {
		if o := exifOrientation(tiff); o != 0 {
			t.Errorf("%s: orientation = %d", name, o)
		}
	}
}

func pngChunk(kind string, body []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	out = append(out, kind...)
	out = append(out, body...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out[4:]))
}

func TestStripPNG(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()
	iend := len(plain) - 12
	dirty := append([]byte{}, plain[:iend]...)
	for _, kind := range []string{"tEXt", "zTXt", "iTXt", "eXIf", "tIME"} {
		dirty = append(dirty, pngChunk(kind, []byte("Comment\x00SECRET"))...)
	}
	dirty = append(dirty, plain[iend:]...)
	dirty = append(dirty, []byte("TRAILER-SECRET")...)
	got := StripMetadata(dirty)
	if !bytes.Equal(got, plain) {
		t.Fatalf("stripped PNG differs from the clean one (%d vs %d bytes)", len(got), len(plain))
	}
	if _, err := png.Decode(bytes.NewReader(got)); err != nil {
		t.Fatal(err)
	}
}

func riffChunk(kind string, body []byte) []byte {
	out := append([]byte(kind), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func TestStripWebP(t *testing.T) {
	var buf bytes.Buffer
	if err := webp.Encode(&buf, sample(), webp.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	simple := buf.Bytes()
	if got := StripMetadata(simple); !bytes.Equal(got, simple) {
		t.Error("simple WebP changed")
	}
	// rebuild it in the extended layout with EXIF and XMP chunks
	vp8x := []byte{0x08 | 0x04 | 0x20, 0, 0, 0, 39, 0, 0, 23, 0, 0} // EXIF, XMP and ICC flags; 40x24
	body := append([]byte("WEBP"), riffChunk("VP8X", vp8x)...)
	body = append(body, simple[12:]...)
	body = append(body, riffChunk("EXIF", []byte("GPS-SECRET"))...)
	body = append(body, riffChunk("XMP ", []byte("<x>SECRET</x>"))...)
	dirty := append([]byte("RIFF\x00\x00\x00\x00"), body...)
	binary.LittleEndian.PutUint32(dirty[4:], uint32(len(body)))

	got := StripMetadata(dirty)
	if bytes.Contains(got, []byte("SECRET")) {
		t.Fatal("metadata survived")
	}
	if int(binary.LittleEndian.Uint32(got[4:])) != len(got)-8 {
		t.Error("RIFF size not updated")
	}
	if flags := got[20]; flags != 0x20 {
		t.Errorf("VP8X flags = %#x, want only ICC", flags)
	}
	if err := Validate(got); err != nil {
		t.Fatalf("stripped WebP does not decode: %v", err)
	}
	// the last chunk may lack its padding byte
	odd := append([]byte("RIFF\x00\x00\x00\x00WEBP"), riffChunk("VP8X", vp8x)...)
	odd = append(odd, "ABCD\x01\x00\x00\x00z"...)
	binary.LittleEndian.PutUint32(odd[4:], uint32(len(odd)-8))
	if got := StripMetadata(odd); !bytes.HasSuffix(got, []byte("ABCD\x01\x00\x00\x00z")) {
		t.Errorf("unpadded last chunk: % x", got)
	}
}
