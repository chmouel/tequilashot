//go:build !nogui

package segment

import (
	"image"
	"image/color"
	"testing"
)

func TestApplyMaskPremultipliesAndScales(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 100, 50, 255
	}
	mask := make([]float32, side*side)
	for y := range side {
		for x := side / 2; x < side; x++ {
			mask[y*side+x] = 1
		}
	}
	applyMask(img, mask, nil)
	left := img.RGBAAt(2, 32)
	right := img.RGBAAt(61, 32)
	if left != (color.RGBA{}) {
		t.Fatalf("background pixel = %v, want transparent", left)
	}
	if right != (color.RGBA{200, 100, 50, 255}) {
		t.Fatalf("person pixel = %v, want opaque original", right)
	}
}

func TestNormaliseFlatMaskIsTransparent(t *testing.T) {
	dst := []float32{1, 1, 1}
	normalise([]float32{0.5, 0.5, 0.5}, dst)
	for _, v := range dst {
		if v != 0 {
			t.Fatalf("flat mask kept %v", v)
		}
	}
}

func TestApplyMaskBlendsOverBackground(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	bg := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 100, 50, 255
		bg.Pix[i], bg.Pix[i+1], bg.Pix[i+2], bg.Pix[i+3] = 10, 20, 30, 255
	}
	mask := make([]float32, side*side)
	for y := range side {
		for x := side / 2; x < side; x++ {
			mask[y*side+x] = 1
		}
	}
	applyMask(img, mask, bg)
	if got := img.RGBAAt(2, 32); got != (color.RGBA{10, 20, 30, 255}) {
		t.Fatalf("background pixel = %v, want the background picture", got)
	}
	if got := img.RGBAAt(61, 32); got != (color.RGBA{200, 100, 50, 255}) {
		t.Fatalf("person pixel = %v, want opaque original", got)
	}
}

func TestCoverScaleCropsToAspect(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 10 && x < 30 {
				c = color.RGBA{0, 255, 0, 255}
			}
			src.SetRGBA(x, y, c)
		}
	}
	out := coverScale(src, 10, 10)
	if out.Rect.Dx() != 10 || out.Rect.Dy() != 10 {
		t.Fatalf("size = %v", out.Rect)
	}
	for _, p := range [][2]int{{0, 0}, {9, 9}, {5, 5}} {
		if got := out.RGBAAt(p[0], p[1]); got != (color.RGBA{0, 255, 0, 255}) {
			t.Fatalf("pixel %v = %v, want the centre crop", p, got)
		}
	}
}

func TestFlipHorizontal(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 1))
	img.SetRGBA(0, 0, color.RGBA{1, 0, 0, 255})
	img.SetRGBA(2, 0, color.RGBA{3, 0, 0, 255})
	flipHorizontal(img)
	if img.RGBAAt(0, 0).R != 3 || img.RGBAAt(2, 0).R != 1 {
		t.Fatalf("not flipped: %v", img.Pix)
	}
}
