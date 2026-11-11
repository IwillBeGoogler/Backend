package images

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/gen2brain/webp"
	xwebp "golang.org/x/image/webp"
)

// quadrants is 40x20: red top-left, green top-right, blue bottom-left, white bottom-right.
func quadrants() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			c := color.RGBA{255, 0, 0, 255}
			switch {
			case x >= 20 && y < 10:
				c = color.RGBA{0, 255, 0, 255}
			case x < 20 && y >= 10:
				c = color.RGBA{0, 0, 255, 255}
			case x >= 20 && y >= 10:
				c = color.RGBA{255, 255, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// name of the quadrant colour at a point, tolerant of lossy coding
func colourAt(img image.Image, x, y int) string {
	r, g, b, _ := img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y).RGBA()
	hi := func(v uint32) bool { return v > 0xA000 }
	switch {
	case hi(r) && hi(g) && hi(b):
		return "white"
	case hi(r):
		return "red"
	case hi(g):
		return "green"
	case hi(b):
		return "blue"
	}
	return "dark"
}

func TestOrient(t *testing.T) {
	// corners of the upright picture, clockwise from top-left, for each EXIF orientation of the quadrants image
	want := map[int][4]string{
		1: {"red", "green", "white", "blue"},
		2: {"green", "red", "blue", "white"},
		3: {"white", "blue", "red", "green"},
		4: {"blue", "white", "green", "red"},
		5: {"red", "blue", "white", "green"},
		6: {"blue", "red", "green", "white"},
		7: {"white", "green", "red", "blue"},
		8: {"green", "white", "blue", "red"},
	}
	for o, corners := range want {
		got := orient(quadrants(), o)
		w, h := got.Bounds().Dx(), got.Bounds().Dy()
		if swapped := o >= 5; (w == 20) != swapped || (h == 40) != swapped {
			t.Errorf("orientation %d: size %dx%d", o, w, h)
		}
		have := [4]string{colourAt(got, 2, 2), colourAt(got, w-3, 2), colourAt(got, w-3, h-3), colourAt(got, 2, h-3)}
		if have != corners {
			t.Errorf("orientation %d: corners %v, want %v", o, have, corners)
		}
	}
	if src := quadrants(); orient(src, 0) != image.Image(src) || orient(src, 9) != image.Image(src) {
		t.Error("unknown orientation must return the source")
	}
}

func TestToWebPJPEG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, quadrants(), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()
	out, err := ToWebP(plain)
	if err != nil {
		t.Fatal(err)
	}
	img, err := xwebp.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output is not WebP: %v", err)
	}
	if img.Bounds().Dx() != 40 || colourAt(img, 2, 2) != "red" {
		t.Errorf("plain JPEG: %v, top-left %s", img.Bounds(), colourAt(img, 2, 2))
	}

	// a phone photo held upright: stored sideways with orientation 6, after a fill byte and another segment
	turned := append([]byte{}, plain[:2]...)
	turned = append(turned, jpegSegment(jpegAPP0, []byte("JFIF\x00"))...)
	turned = append(turned, 0xFF)
	turned = append(turned, jpegSegment(jpegAPP1, exifBlock(6))...)
	turned = append(turned, plain[2:]...)
	if o := jpegOrientation(turned); o != 6 {
		t.Fatalf("orientation = %d", o)
	}
	out, err = ToWebP(turned)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS-SECRET")) {
		t.Error("EXIF survived the conversion")
	}
	img, err = xwebp.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 40 || colourAt(img, 2, 2) != "blue" || colourAt(img, 17, 2) != "red" {
		t.Errorf("turned JPEG: %v, top %s %s", b, colourAt(img, 2, 2), colourAt(img, 17, 2))
	}
}

func TestToWebPPNGIsLossless(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			src.SetNRGBA(x, y, color.NRGBA{uint8(x * 16), uint8(y * 16), 77, uint8(255 - x*8)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	out, err := ToWebP(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := xwebp.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []image.Point{{0, 0}, {7, 3}, {15, 15}} {
		got := color.NRGBAModel.Convert(img.At(p.X, p.Y)).(color.NRGBA)
		if want := src.NRGBAAt(p.X, p.Y); got != want {
			t.Errorf("pixel %v = %v, want %v", p, got, want)
		}
	}
}

func TestToWebPRefuses(t *testing.T) {
	if _, err := ToWebP([]byte("not an image")); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbage: %v", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, quadrants()); err != nil {
		t.Fatal(err)
	}
	cut := buf.Bytes()[:buf.Len()-30]
	if _, err := ToWebP(cut); !errors.Is(err, ErrInvalid) {
		t.Errorf("cut PNG: %v", err)
	}
	// header claiming more than MaxPixels
	huge := append([]byte{}, pngHeader(50_000, 50_000)...)
	if _, err := ToWebP(huge); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized: %v", err)
	}
}

func pngHeader(w, h uint32) []byte {
	ihdr := []byte{byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w), byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h), 8, 6, 0, 0, 0}
	return append([]byte(pngMagic), pngChunk("IHDR", ihdr)...)
}

func TestJPEGOrientationEdges(t *testing.T) {
	for name, data := range map[string][]byte{
		"no exif":    append([]byte{0xFF, 0xD8}, append(jpegSegment(jpegAPP0, []byte("JFIF")), 0xFF, jpegSOS, 0, 2)...),
		"ends":       {0xFF, 0xD8, 0xFF, jpegEOI, 0, 0},
		"bad length": {0xFF, 0xD8, 0xFF, jpegAPP1, 0x00, 0x01},
		"cut":        {0xFF, 0xD8, 0xFF, jpegAPP1, 0x00, 0x40, 1},
		"not jpeg":   []byte("plain"),
	} {
		if o := jpegOrientation(data); o != 0 {
			t.Errorf("%s: orientation %d", name, o)
		}
	}
}

func animatedGIF(frames int) []byte {
	pal := color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	g := &gif.GIF{Config: image.Config{Width: 30, Height: 20}}
	for i := range frames {
		// each frame covers only a part of the canvas, as optimised GIFs do
		f := image.NewPaletted(image.Rect(i*5, 0, i*5+10, 10), pal)
		for y := range 10 {
			for x := i * 5; x < i*5+10; x++ {
				f.SetColorIndex(x, y, uint8(1+i%2))
			}
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 5+i)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestToWebPGIF(t *testing.T) {
	out, err := ToWebP(animatedGIF(3))
	if err != nil {
		t.Fatal(err)
	}
	anim, err := webp.DecodeAll(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("not a WebP: %v", err)
	}
	if len(anim.Image) != 3 {
		t.Fatalf("frames = %d, want 3", len(anim.Image))
	}
	if b := anim.Image[0].Bounds(); b.Dx() != 30 || b.Dy() != 20 {
		t.Errorf("canvas = %v", b)
	}
	// frames accumulate on the canvas: the third still shows the first one's left edge, the first does not show the third
	if colourAt(anim.Image[2], 2, 2) != "red" || colourAt(anim.Image[0], 25, 2) == "blue" {
		t.Errorf("compositing: %s / %s", colourAt(anim.Image[2], 2, 2), colourAt(anim.Image[0], 25, 2))
	}
	if anim.Delay[0] != 50 || anim.Delay[2] != 70 { // centiseconds become milliseconds
		t.Errorf("delays = %v", anim.Delay)
	}

	still, err := ToWebP(animatedGIF(1))
	if err != nil {
		t.Fatal(err)
	}
	if one, err := webp.DecodeAll(bytes.NewReader(still)); err != nil || len(one.Image) != 1 {
		t.Errorf("still GIF: %v, %d frames", err, len(one.Image))
	}
	if _, err := ToWebP(append([]byte("GIF89a"), 1, 2, 3)); !errors.Is(err, ErrInvalid) {
		t.Errorf("broken GIF: %v", err)
	}
}

func TestToWebPWebP(t *testing.T) {
	var big bytes.Buffer
	if err := webp.Encode(&big, quadrants(), webp.Options{Quality: 100, Method: 0}); err != nil {
		t.Fatal(err)
	}
	out, err := ToWebP(big.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) >= big.Len() {
		t.Errorf("recompressed %d bytes into %d", big.Len(), len(out))
	}
	var small bytes.Buffer
	if err := webp.Encode(&small, quadrants(), webp.Options{Quality: 1, Method: 6}); err != nil {
		t.Fatal(err)
	}
	if out, err := ToWebP(small.Bytes()); err != nil || !bytes.Equal(out, small.Bytes()) {
		t.Errorf("an already small WebP must come back unchanged (%v)", err)
	}
	// an animation stays one
	anim, err := ToWebP(animatedGIF(2))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ToWebP(anim)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := webp.DecodeAll(bytes.NewReader(again)); err != nil || len(got.Image) != 2 {
		t.Errorf("animated WebP: %v, %d frames", err, len(got.Image))
	}
}

// The worker makes the thumbnail and the variants from the stored original, which is now often an animated WebP.
func TestProcessAnimatedWebP(t *testing.T) {
	anim, err := ToWebP(animatedGIF(3))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Process(bytes.NewReader(anim), []int{20})
	if err != nil {
		t.Fatalf("Process on an animated WebP: %v", err)
	}
	if len(p.Thumbnail) == 0 || len(p.Variants[20]) == 0 {
		t.Error("nothing made")
	}
}
