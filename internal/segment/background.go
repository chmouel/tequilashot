//go:build !nogui

package segment

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // registers the JPEG decoder for LoadBackground
	_ "image/png"  // registers the PNG decoder for LoadBackground
	"os"
)

// LoadBackground decodes a PNG or JPEG file for use with SetBackground.
func LoadBackground(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open the camera background: %w", err)
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read the camera background %s (PNG or JPEG expected): %w", path, err)
	}
	if img.Bounds().Empty() {
		return nil, errors.New("the camera background is empty")
	}
	return img, nil
}

// SetBackground makes Cut and CutImage place the person over img instead of
// leaving the background transparent. flip mirrors img horizontally, for a
// picture that is mirrored again when shown. A nil img restores transparency.
func (m *Matter) SetBackground(img image.Image, flip bool) {
	m.scaled = nil
	if img == nil {
		m.bg = nil
		return
	}
	b := img.Bounds()
	opaque := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(opaque, opaque.Rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
	draw.Draw(opaque, opaque.Rect, img, b.Min, draw.Over)
	if flip {
		flipHorizontal(opaque)
	}
	m.bg = opaque
}

// background returns the background scaled to cover w by h, or nil when none
// is set. The result is kept until the frame size changes.
func (m *Matter) background(w, h int) *image.RGBA {
	if m.bg == nil {
		return nil
	}
	if m.scaled == nil || m.scaled.Rect.Dx() != w || m.scaled.Rect.Dy() != h {
		m.scaled = coverScale(m.bg, w, h)
	}
	return m.scaled
}

func flipHorizontal(img *image.RGBA) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	for y := range h {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for l, r := 0, w-1; l < r; l, r = l+1, r-1 {
			for c := range 4 {
				row[l*4+c], row[r*4+c] = row[r*4+c], row[l*4+c]
			}
		}
	}
}

// coverScale fills a w by h picture with the centre of src, cropped to the
// same aspect ratio, averaging the source pixels under each output pixel.
func coverScale(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	cw, ch := sw, sh
	if sw*h > sh*w {
		cw = sh * w / h
	} else {
		ch = sw * h / w
	}
	cw, ch = max(cw, 1), max(ch, 1)
	x0, y0 := (sw-cw)/2, (sh-ch)/2
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		sy0 := y0 + y*ch/h
		sy1 := max(sy0+1, y0+(y+1)*ch/h)
		for x := range w {
			sx0 := x0 + x*cw/w
			sx1 := max(sx0+1, x0+(x+1)*cw/w)
			var r, g, b, n int
			for sy := sy0; sy < sy1 && sy < sh; sy++ {
				for sx := sx0; sx < sx1 && sx < sw; sx++ {
					o := src.PixOffset(sx, sy)
					r += int(src.Pix[o])
					g += int(src.Pix[o+1])
					b += int(src.Pix[o+2])
					n++
				}
			}
			if n == 0 {
				continue
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = byte(r/n), byte(g/n), byte(b/n), 255
		}
	}
	return out
}
