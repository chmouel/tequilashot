//go:build !nogui

package segment

import (
	"errors"
	"fmt"
	"image"
	"math"
	"os"

	"github.com/chmouel/tequilashot/internal/onnx"
	"github.com/chmouel/tequilashot/internal/prefs"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	// MattingModelEnv selects the matting model: modnet (the default) or
	// u2net.
	MattingModelEnv = prefs.EnvMattingModel
	// ModnetModelEnv names the MODNet model file, overriding the default
	// location.
	ModnetModelEnv = prefs.EnvModnetModel

	// ModnetModelSize is the exact download size in bytes.
	ModnetModelSize = 25_888_640

	// modnetRefSize is the short edge MODNet expects, and modnetStride the
	// multiple both edges are rounded down to by the model's own tooling.
	modnetRefSize = 512
	modnetStride  = 32

	// alphaTolerance lets float rounding through when checking that the model
	// outputs an alpha matte rather than logits.
	alphaTolerance = 0.01
)

// ModnetModelPath returns the MODNet model file: the environment, then the
// preferences, then the default location.
func ModnetModelPath() string { return prefs.ModnetModelPath() }

// DefaultModnetModelPath is where the MODNet model is looked for and
// downloaded when no other location is configured.
func DefaultModnetModelPath() string { return prefs.DefaultModnetModelPath() }

// modnet runs the MODNet portrait-matting model. Unlike U²-Net its output is
// already an alpha matte in 0..1, so it is used as is rather than stretched.
type modnet struct {
	session *ort.DynamicAdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	mask    []float32
	iw, ih  int
}

func newModnet(model string) (*modnet, error) {
	if _, err := os.Stat(model); err != nil {
		return nil, fmt.Errorf("MODNet model not found at %s (set %s to use another file)", model, ModnetModelEnv)
	}
	if err := onnx.Init(); err != nil {
		return nil, err
	}
	inputs, outputs, err := ort.GetInputOutputInfo(model)
	if err != nil || len(inputs) != 1 || len(outputs) != 1 || !modnetShapes(inputs[0].Dimensions, outputs[0].Dimensions) {
		return nil, fmt.Errorf("unsupported MODNet model %s", model)
	}
	session, err := ort.NewDynamicAdvancedSession(model, []string{inputs[0].Name}, []string{outputs[0].Name}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load the MODNet model: %w", err)
	}
	return &modnet{session: session}, nil
}

func modnetShapes(in, out []int64) bool {
	return len(in) == 4 && in[1] == 3 && len(out) == 4 && out[1] == 1
}

// resize prepares tensors for a rw by rh frame, reusing them whilst the frame
// size stays the same.
func (m *modnet) resize(rw, rh int) error {
	if m.input != nil && m.iw == rw && m.ih == rh {
		return nil
	}
	m.releaseTensors()
	input, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, int64(rh), int64(rw)))
	if err != nil {
		return err
	}
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, int64(rh), int64(rw)))
	if err != nil {
		_ = input.Destroy()
		return err
	}
	m.input, m.output, m.mask, m.iw, m.ih = input, output, make([]float32, rw*rh), rw, rh
	return nil
}

func (m *modnet) releaseTensors() {
	if m.input == nil {
		return
	}
	_ = m.input.Destroy()
	_ = m.output.Destroy()
	m.input, m.output, m.mask = nil, nil, nil
	m.iw, m.ih = 0, 0
}

func (m *modnet) alpha(img *image.RGBA) ([]float32, int, int, error) {
	b := img.Rect
	w, h := b.Dx(), b.Dy()
	rw, rh := modnetInputSize(w, h)
	if err := m.resize(rw, rh); err != nil {
		return nil, 0, 0, err
	}
	modnetFill(m.input.GetData(), img, rw, rh)
	if err := m.session.Run([]ort.Value{m.input}, []ort.Value{m.output}); err != nil {
		return nil, 0, 0, fmt.Errorf("MODNet segmentation failed: %w", err)
	}
	raw := m.output.GetData()
	if len(raw) != rw*rh {
		return nil, 0, 0, errors.New("unexpected MODNet output")
	}
	for _, v := range raw {
		if v < -alphaTolerance || v > 1+alphaTolerance {
			return nil, 0, 0, errors.New("MODNet output is not an alpha matte; expected a model with a final sigmoid")
		}
	}
	for i, v := range raw {
		m.mask[i] = clamp01(v)
	}
	return m.mask, rw, rh, nil
}

func (m *modnet) close() {
	m.releaseTensors()
	_ = m.session.Destroy()
}

// modnetInputSize returns the model input size for a w by h picture, following
// the model's own tooling: the short edge becomes modnetRefSize unless the
// picture already straddles it, then both edges are rounded down to
// modnetStride and kept at 32 or more.
func modnetInputSize(w, h int) (int, int) {
	rw, rh := w, h
	if max(w, h) < modnetRefSize || min(w, h) > modnetRefSize {
		if w >= h {
			rh = modnetRefSize
			rw = max(1, w*modnetRefSize/h)
		} else {
			rw = modnetRefSize
			rh = max(1, h*modnetRefSize/w)
		}
	}
	rw -= rw % modnetStride
	rh -= rh % modnetStride
	return max(rw, modnetStride), max(rh, modnetStride)
}

// modnetFill writes img to dst as a NCHW RGB tensor scaled to -1..1, sampling
// the picture bilinearly.
func modnetFill(dst []float32, img *image.RGBA, rw, rh int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	plane := rw * rh
	for y := range rh {
		sy := (float64(y)+0.5)*float64(h)/float64(rh) - 0.5
		for x := range rw {
			sx := (float64(x)+0.5)*float64(w)/float64(rw) - 0.5
			r, g, b := sampleBilinear(img, sx, sy)
			i := y*rw + x
			dst[i] = float32(r/127.5 - 1)
			dst[plane+i] = float32(g/127.5 - 1)
			dst[2*plane+i] = float32(b/127.5 - 1)
		}
	}
}

// sampleBilinear returns the colour at fractional coordinates, clamped at the
// edges.
func sampleBilinear(img *image.RGBA, fx, fy float64) (float64, float64, float64) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	x0 := int(math.Floor(fx))
	y0 := int(math.Floor(fy))
	tx := fx - math.Floor(fx)
	ty := fy - math.Floor(fy)
	xa, xb := min(max(x0, 0), w-1), min(max(x0+1, 0), w-1)
	ya, yb := min(max(y0, 0), h-1), min(max(y0+1, 0), h-1)
	var c [3]float64
	for i := range 3 {
		top := float64(img.Pix[img.PixOffset(xa, ya)+i])*(1-tx) + float64(img.Pix[img.PixOffset(xb, ya)+i])*tx
		bot := float64(img.Pix[img.PixOffset(xa, yb)+i])*(1-tx) + float64(img.Pix[img.PixOffset(xb, yb)+i])*tx
		c[i] = top*(1-ty) + bot*ty
	}
	return c[0], c[1], c[2]
}
