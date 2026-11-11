//go:build !nogui

package segment

import (
	"image"
	"image/color"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestModnetInputSize(t *testing.T) {
	for _, tc := range []struct {
		w, h, rw, rh int
	}{
		{460, 460, 512, 512},
		{1069, 772, 704, 512},
		{1920, 1080, 896, 512},
		{600, 400, 576, 384},
		{400, 800, 384, 800},
		{800, 1600, 512, 1024},
		{3000, 100, 2976, 96},
		{10, 10, 512, 512},
	} {
		rw, rh := modnetInputSize(tc.w, tc.h)
		if rw != tc.rw || rh != tc.rh {
			t.Errorf("modnetInputSize(%d, %d) = %dx%d, want %dx%d", tc.w, tc.h, rw, rh, tc.rw, tc.rh)
		}
	}
}

func TestModnetFillScalesToMinusOneToOne(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 128, 255})
	const rw, rh = 2, 2
	dst := make([]float32, 3*rw*rh)
	modnetFill(dst, img, rw, rh)
	plane := rw * rh
	if got := dst[0]; math.Abs(float64(got)-1) > 1e-6 {
		t.Fatalf("red = %v, want 1", got)
	}
	if got := dst[plane]; math.Abs(float64(got)+1) > 1e-6 {
		t.Fatalf("green = %v, want -1", got)
	}
	want := float32(128)/127.5 - 1
	if got := dst[2*plane]; math.Abs(float64(got-want)) > 1e-6 {
		t.Fatalf("blue = %v, want %v", got, want)
	}
}

func TestModnetFillKeepsPictureInFrame(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	const rw, rh = 8, 8
	dst := make([]float32, 3*rw*rh)
	modnetFill(dst, img, rw, rh)
	if dst[0] < 0.99 {
		t.Fatalf("top left = %v, want the first pixel stretched", dst[0])
	}
}

func TestSelectMattingModel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		want     mattingModel
		explicit bool
	}{
		{"", modnetModel, false},
		{"modnet", modnetModel, true},
		{"MODNET", modnetModel, true},
		{"u2net", u2netModel, true},
		{"U2Net", u2netModel, true},
		{" ", modnetModel, false},
	} {
		got, explicit, err := selectMattingModel(tc.name)
		if err != nil {
			t.Fatalf("selectMattingModel(%q): %v", tc.name, err)
		}
		if got != tc.want || explicit != tc.explicit {
			t.Fatalf("selectMattingModel(%q) = %v, %v, want %v, %v", tc.name, got, explicit, tc.want, tc.explicit)
		}
	}
	if _, _, err := selectMattingModel("u2netp"); err == nil {
		t.Fatal("an unknown model name was accepted")
	}
}

func TestScaleMaskKeepsAlpha(t *testing.T) {
	mask := []float32{0, 1, 0.5, 0.25}
	gray := scaleMask(mask, 2, 2, 2, 2)
	if gray.Rect.Dx() != 2 || gray.Rect.Dy() != 2 {
		t.Fatalf("size = %v", gray.Rect)
	}
	for i, want := range mask {
		if got := gray.Pix[i]; got != byte(255*want) {
			t.Fatalf("alpha %d = %d, want %d", i, got, byte(255*want))
		}
	}
}

func TestNewSelectsModnetFromTheEnvironment(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.onnx")
	t.Setenv(MattingModelEnv, "modnet")
	t.Setenv(ModnetModelEnv, missing)
	if _, err := New(); err == nil || !strings.Contains(err.Error(), "MODNet model not found") {
		t.Fatalf("New() error = %v, want the missing MODNet model", err)
	}
	if got := ModnetModelPath(); got != missing {
		t.Fatalf("ModnetModelPath() = %q, want %q", got, missing)
	}
}

func TestNewRejectsAnUnknownModel(t *testing.T) {
	t.Setenv(MattingModelEnv, "u2netp")
	if _, err := New(); err == nil {
		t.Fatal("New() accepted an unknown model")
	}
}
