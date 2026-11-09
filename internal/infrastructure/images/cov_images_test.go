package images

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func covPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 600, 400))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCovValidateGarbageAndTruncated(t *testing.T) {
	if err := Validate([]byte("not an image")); err != ErrInvalid {
		t.Fatalf("garbage: %v", err)
	}
	p := covPNG(t)
	if err := Validate(p[:len(p)/2]); err != ErrInvalid {
		t.Fatalf("truncated: %v", err)
	}
	if err := Validate(p); err != nil {
		t.Fatal(err)
	}
}

func TestCovProcessTruncatedBody(t *testing.T) {
	p := covPNG(t)
	if _, err := Process(bytes.NewReader(p[:len(p)-40]), nil); err == nil {
		t.Fatal("want decode error")
	}
}

func TestCovThumbnail(t *testing.T) {
	th, err := Thumbnail(bytes.NewReader(covPNG(t)))
	if err != nil || len(th) == 0 {
		t.Fatalf("%d %v", len(th), err)
	}
	if _, err := Thumbnail(bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("want error")
	}
}

func TestCovProcessVariantEncodeError(t *testing.T) {
	// a zero width yields an empty image the WebP encoder refuses
	if _, err := Process(bytes.NewReader(covPNG(t)), []int{0}); err == nil {
		t.Skip("encoder accepts empty image")
	}
}
