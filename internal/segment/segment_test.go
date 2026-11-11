//go:build !nogui

package segment

import (
	"errors"
	"image"
	"image/color"
	"strings"
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
	applyMask(img, mask)
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

func TestStartErrorReportsOldRuntime(t *testing.T) {
	err := startError("/nonexistent/libonnxruntime.so.1.20.1", errors.New("Platform-specific initialization failed: Error setting ORT API base: 2"))
	for _, want := range []string{"1.20.1", "too old", minRuntime} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	other := startError("/x/lib.so", errors.New("boom"))
	if !strings.Contains(other.Error(), "/x/lib.so") || !strings.Contains(other.Error(), "boom") {
		t.Fatalf("generic error = %q", other)
	}
}
