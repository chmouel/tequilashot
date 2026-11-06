//go:build !nogui

// Package segment separates a person from the background of a camera frame
// using a local ONNX person-segmentation model (U²-Net human segmentation) run by ONNX Runtime.
package segment

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	// ModelEnv names the model file, overriding the default location.
	ModelEnv = "TEQUILASHOT_SEGMENTATION_MODEL"
	// LibraryEnv names the ONNX Runtime shared library to load.
	LibraryEnv = "TEQUILASHOT_ONNXRUNTIME_LIB"

	modelName = "u2net_human_seg.onnx"
	side      = 320

	// The mask is stretched to 0..1, then everything below edgeLo is dropped
	// and everything above edgeHi kept whole.
	edgeLo, edgeHi = 0.1, 0.5
)

var (
	mean = [3]float32{0.485, 0.456, 0.406}
	std  = [3]float32{0.229, 0.224, 0.225}

	libraryCandidates = []string{
		"/usr/lib/libonnxruntime.so",
		"/usr/lib64/libonnxruntime.so",
		"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
		"/usr/lib/aarch64-linux-gnu/libonnxruntime.so",
		"/usr/local/lib/libonnxruntime.so",
	}

	envOnce sync.Once
	envErr  error
)

// ModelPath returns the model file to load. Nothing is downloaded: the file
// has to be installed by the user.
func ModelPath() string {
	if p := os.Getenv(ModelEnv); p != "" {
		return p
	}
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "tequilashot", modelName)
}

func libraryPath() (string, error) {
	if p := os.Getenv(LibraryEnv); p != "" {
		return p, nil
	}
	for _, p := range libraryCandidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ONNX Runtime library not found; install it or set %s", LibraryEnv)
}

func initRuntime() error {
	envOnce.Do(func() {
		lib, err := libraryPath()
		if err != nil {
			envErr = err
			return
		}
		ort.SetSharedLibraryPath(lib)
		if err := ort.InitializeEnvironment(); err != nil {
			envErr = fmt.Errorf("failed to start ONNX Runtime: %w", err)
		}
	})
	return envErr
}

// Matter turns camera frames into frames whose background is transparent. It
// is not safe for concurrent use.
type Matter struct {
	session *ort.DynamicAdvancedSession
	input   *ort.Tensor[float32]
	small   []float32
}

// New loads the model. It fails when the runtime or the model is missing.
func New() (*Matter, error) {
	model := ModelPath()
	if _, err := os.Stat(model); err != nil {
		return nil, fmt.Errorf("segmentation model not found at %s (set %s to use another file)", model, ModelEnv)
	}
	if err := initRuntime(); err != nil {
		return nil, err
	}
	input, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, side, side))
	if err != nil {
		return nil, err
	}
	inputs, outputs, err := ort.GetInputOutputInfo(model)
	if err != nil || len(inputs) != 1 || len(outputs) == 0 {
		_ = input.Destroy()
		return nil, fmt.Errorf("unsupported segmentation model %s", model)
	}
	session, err := ort.NewDynamicAdvancedSession(model, []string{inputs[0].Name}, []string{outputs[0].Name}, nil)
	if err != nil {
		_ = input.Destroy()
		return nil, fmt.Errorf("failed to load the segmentation model: %w", err)
	}
	return &Matter{session: session, input: input, small: make([]float32, side*side)}, nil
}

// Close releases the model.
func (m *Matter) Close() {
	if m == nil {
		return
	}
	_ = m.session.Destroy()
	_ = m.input.Destroy()
}

// Cut decodes a JPEG frame and returns it as premultiplied RGBA with the
// background made transparent.
func (m *Matter) Cut(frame []byte) (*image.RGBA, error) {
	src, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	return m.CutImage(src)
}

// CutImage makes the background of an already decoded frame transparent.
func (m *Matter) CutImage(src image.Image) (*image.RGBA, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("empty frame")
	}
	rgba := toRGBA(src)

	data := m.input.GetData()
	plane := side * side
	for y := range side {
		sy := b.Min.Y + y*h/side
		for x := range side {
			sx := b.Min.X + x*w/side
			off := rgba.PixOffset(sx, sy)
			for c := range 3 {
				data[c*plane+y*side+x] = (float32(rgba.Pix[off+c])/255 - mean[c]) / std[c]
			}
		}
	}

	outputs := []ort.Value{nil}
	if err := m.session.Run([]ort.Value{m.input}, outputs); err != nil {
		return nil, fmt.Errorf("segmentation failed: %w", err)
	}
	defer func() { _ = outputs[0].Destroy() }()
	mask, ok := outputs[0].(*ort.Tensor[float32])
	if !ok || len(mask.GetData()) != plane {
		return nil, errors.New("unexpected segmentation output")
	}
	normalise(mask.GetData(), m.small)
	applyMask(rgba, m.small)
	return rgba, nil
}

func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, src, b.Min, draw.Src)
	return out
}

// normalise stretches the raw mask to 0..1 and sharpens its edge.
func normalise(raw, dst []float32) {
	lo, hi := raw[0], raw[0]
	for _, v := range raw {
		lo, hi = min(lo, v), max(hi, v)
	}
	span := hi - lo
	if span < 1e-6 {
		clear(dst)
		return
	}
	for i, v := range raw {
		t := (v - lo) / span
		dst[i] = clamp01((t - edgeLo) / (edgeHi - edgeLo))
	}
}

func clamp01(v float32) float32 { return max(0, min(1, v)) }

// applyMask scales the mask up bilinearly and premultiplies the picture by it.
func applyMask(img *image.RGBA, mask []float32) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	for y := range h {
		fy := (float32(y)+0.5)*side/float32(h) - 0.5
		y0 := int(max(0, fy))
		y1 := min(y0+1, side-1)
		ty := max(0, fy-float32(y0))
		for x := range w {
			fx := (float32(x)+0.5)*side/float32(w) - 0.5
			x0 := int(max(0, fx))
			x1 := min(x0+1, side-1)
			tx := max(0, fx-float32(x0))
			top := mask[y0*side+x0]*(1-tx) + mask[y0*side+x1]*tx
			bot := mask[y1*side+x0]*(1-tx) + mask[y1*side+x1]*tx
			a := top*(1-ty) + bot*ty
			o := img.PixOffset(x, y)
			img.Pix[o] = byte(float32(img.Pix[o]) * a)
			img.Pix[o+1] = byte(float32(img.Pix[o+1]) * a)
			img.Pix[o+2] = byte(float32(img.Pix[o+2]) * a)
			img.Pix[o+3] = byte(255 * a)
		}
	}
}
