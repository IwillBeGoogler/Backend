package images

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/gif"

	"github.com/gen2brain/webp"
)

// ToWebP re-encodes an upload as WebP, the format every original is stored in.
// A JPEG becomes lossy (quality 82), turned the way its EXIF orientation says
// since WebP output carries no metadata; a PNG and a still GIF become
// lossless, so screenshots and drawings stay exact and transparency is kept;
// an animated GIF stays animated (lossy, frames composited as a viewer
// would show them); a WebP is compressed again, or returned as it is when
// that would not make it smaller. Data that does not decode is ErrInvalid,
// more than MaxPixels (in all frames together) ErrTooLarge; any other error
// is the encoder's.
func ToWebP(data []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, ErrTooLarge
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	opts := webp.Options{Quality: 82, Method: 4}
	switch format {
	case "gif":
		return gifToWebP(data)
	case "webp":
		return recompressWebP(data)
	case "jpeg":
		src = orient(src, jpegOrientation(data))
	case "png":
		opts = webp.Options{Lossless: true, Method: 4}
	default:
		return nil, fmt.Errorf("images: %s is not converted to WebP", format)
	}
	var buf bytes.Buffer
	if err := webp.Encode(&buf, src, opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jpegOrientation is the EXIF orientation (1..8) of a JPEG, or 0.
func jpegOrientation(data []byte) int {
	for i := 2; i+4 <= len(data) && data[i] == 0xFF; {
		m := data[i+1]
		if m == 0xFF { // fill byte
			i++
			continue
		}
		if m == jpegSOS || m == jpegEOI {
			return 0
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return 0
		}
		payload := data[i+4 : i+2+n]
		if m == jpegAPP1 && bytes.HasPrefix(payload, exifHeader) {
			return int(exifOrientation(payload[len(exifHeader):]))
		}
		i += 2 + n
	}
	return 0
}

// orient returns src turned upright for EXIF orientation o (2..8); 0 and 1
// return src itself.
func orient(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	in := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(in, in.Bounds(), src, b.Min, draw.Src)
	dw, dh := w, h
	if o >= 5 { // the four that swap the sides
		dw, dh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch o {
			case 2: // mirrored
				sx, sy = w-1-x, y
			case 3: // upside down
				sx, sy = w-1-x, h-1-y
			case 4: // flipped
				sx, sy = x, h-1-y
			case 5: // mirrored, then a quarter turn
				sx, sy = y, x
			case 6: // a quarter turn clockwise
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			default: // 8: a quarter turn counter-clockwise
				sx, sy = w-1-y, x
			}
			copy(out.Pix[out.PixOffset(x, y):][:4], in.Pix[in.PixOffset(sx, sy):][:4])
		}
	}
	return out
}

// gifToWebP converts a GIF; the frames of an animation are composited onto a
// full canvas first (a GIF frame may cover only part of it and say how it is
// cleared afterwards).
func gifToWebP(data []byte) ([]byte, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || len(g.Image) == 0 {
		return nil, ErrInvalid
	}
	w, h := g.Config.Width, g.Config.Height
	if w <= 0 || h <= 0 {
		b := g.Image[0].Bounds()
		w, h = b.Dx(), b.Dy()
	}
	if int64(w)*int64(h)*int64(len(g.Image)) > MaxPixels {
		return nil, ErrTooLarge
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	anim := &webp.WEBP{LoopCount: max(g.LoopCount, 0)}
	if g.LoopCount < 0 {
		anim.LoopCount = 1 // play once
	}
	for i, frame := range g.Image {
		var before *image.RGBA
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			before = image.NewRGBA(canvas.Bounds())
			copy(before.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		shot := image.NewRGBA(canvas.Bounds())
		copy(shot.Pix, canvas.Pix)
		anim.Image = append(anim.Image, shot)
		delay := 100 // browsers show 10 ms and less as 100 ms
		if i < len(g.Delay) && g.Delay[i] > 1 {
			delay = g.Delay[i] * 10
		}
		anim.Delay = append(anim.Delay, delay)
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, before.Pix)
		}
	}
	var buf bytes.Buffer
	if len(anim.Image) == 1 {
		err = webp.Encode(&buf, anim.Image[0], webp.Options{Lossless: true, Method: 4})
	} else {
		err = webp.EncodeAll(&buf, anim, webp.Options{Quality: 80, Method: 4})
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// recompressWebP encodes a WebP lossy at quality 82 (all frames of an
// animation) and returns the result only when it is smaller.
func recompressWebP(data []byte) ([]byte, error) {
	anim, err := webp.DecodeAll(bytes.NewReader(data))
	if err != nil || len(anim.Image) == 0 {
		return nil, ErrInvalid
	}
	b := anim.Image[0].Bounds()
	if int64(b.Dx())*int64(b.Dy())*int64(len(anim.Image)) > MaxPixels {
		return nil, ErrTooLarge
	}
	var buf bytes.Buffer
	opts := webp.Options{Quality: 82, Method: 4}
	if len(anim.Image) == 1 {
		err = webp.Encode(&buf, anim.Image[0], opts)
	} else {
		err = webp.EncodeAll(&buf, anim, opts)
	}
	if err != nil {
		return nil, err
	}
	if buf.Len() >= len(data) {
		return data, nil
	}
	return buf.Bytes(), nil
}
