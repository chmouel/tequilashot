//go:build !nogui

// Package segment separates a person from the background of a camera frame
// using a local ONNX portrait-matting model (MODNet, or U²-Net) run by ONNX
// Runtime.
package segment

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/chmouel/tequilashot/internal/onnx"
	"github.com/chmouel/tequilashot/internal/prefs"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	// ModelEnv names the model file, overriding the default location.
	ModelEnv = prefs.EnvSegmentationModel
	// LibraryEnv names the ONNX Runtime shared library to load.
	LibraryEnv = onnx.LibraryEnv

	side = 320

	// The mask is stretched to 0..1, then everything below edgeLo is dropped
	// and everything above edgeHi kept whole.
	edgeLo, edgeHi = 0.1, 0.5
)

var (
	mean = [3]float32{0.485, 0.456, 0.406}
	std  = [3]float32{0.229, 0.224, 0.225}
)

// ModelPath returns the U²-Net model file to load. Nothing is downloaded: the
// file has to be installed by the user.
func ModelPath() string {
	return prefs.SegmentationModelPath()
}

// DefaultModelPath is where the U²-Net model is looked for when no other
// location is configured.
func DefaultModelPath() string {
	return prefs.DefaultSegmentationModelPath()
}

// LoadedLibrary returns the ONNX Runtime library this process has loaded, or
// an empty string. A different library takes effect only in a new process.
func LoadedLibrary() string { return onnx.LoadedLibrary() }

// matting computes a single-channel alpha in 0..1, mw by mh, for img.
type matting interface {
	alpha(img *image.RGBA) (mask []float32, mw, mh int, err error)
	close()
}

// Matter turns camera frames into frames whose background is transparent, or
// replaced when SetBackground is used. It is not safe for concurrent use.
type Matter struct {
	impl   matting
	bg     *image.RGBA // see SetBackground
	scaled *image.RGBA
}

type mattingModel int

const (
	u2netModel mattingModel = iota
	modnetModel
)

// New loads the matting model named by the environment. MODNet is the default;
// see MattingModelEnv for the opt-in U²-Net.
func New() (*Matter, error) {
	return NewWithModel("")
}

// NewWithModel loads the model a caller asked for by name, such as the
// --camera-matting-model flag: the environment decides when name is empty, then
// MODNet. The benchmark in hack/mattebench names the model to compare both.
func NewWithModel(name string) (*Matter, error) {
	kind, path, err := resolveMattingModel(mattingChoice(name))
	if err != nil {
		return nil, err
	}
	var impl matting
	if kind == modnetModel {
		impl, err = newModnet(path)
	} else {
		impl, err = newU2Net(path)
	}
	if err != nil {
		return nil, err
	}
	return &Matter{impl: impl}, nil
}

// mattingChoice is the model to resolve: the one a caller named, otherwise the
// environment's.
func mattingChoice(name string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return os.Getenv(MattingModelEnv)
}

// ModelInstalled reports whether the model in use is in place, following the
// same choice as New.
func ModelInstalled() bool {
	_, path, err := resolveMattingModel(mattingChoice(""))
	if err != nil {
		return false
	}
	absent, err := modelAbsent(path)
	return err == nil && !absent
}

// NeedsDownload reports whether downloading MODNet would enable the background
// removal: it is the model in use and its file is missing.
func NeedsDownload() bool {
	kind, path, err := resolveMattingModel(mattingChoice(""))
	if err != nil || kind != modnetModel {
		return false
	}
	absent, err := modelAbsent(path)
	return err == nil && absent
}

// ModelProblem explains why the background removal cannot start, or returns nil
// when it can. The dialogue offers the MODNet download before asking.
func ModelProblem() error {
	kind, path, err := resolveMattingModel(mattingChoice(""))
	if err != nil {
		return err
	}
	absent, err := modelAbsent(path)
	if err != nil {
		return err
	}
	if !absent {
		return nil
	}
	name, env := "MODNet", ModnetModelEnv
	if kind == u2netModel {
		name, env = "U²-Net", ModelEnv
	}
	return fmt.Errorf("%s model not found at %s (set %s to use another file)", name, path, env)
}

// modelAbsent reports whether path is missing. Any other stat error is returned
// rather than mistaken for a missing file, which would change the model.
func modelAbsent(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, os.ErrNotExist):
		return true, nil
	}
	return false, fmt.Errorf("cannot read %s: %w", path, err)
}

// fallbackWarning keeps the GUI's repeated installed checks from logging the
// same warning on every refresh.
var fallbackWarning sync.Once

// resolveMattingModel picks the model and its file for name. An upgrade that
// still has only the U²-Net file keeps matting until MODNet is downloaded, but
// only whilst MODNet is the implicit default.
func resolveMattingModel(name string) (mattingModel, string, error) {
	kind, explicit, err := selectMattingModel(name)
	if err != nil {
		return 0, "", err
	}
	if kind != modnetModel {
		return kind, ModelPath(), nil
	}
	modnetPath := ModnetModelPath()
	absent, err := modelAbsent(modnetPath)
	if err != nil {
		return 0, "", err
	}
	if explicit || !absent {
		return kind, modnetPath, nil
	}
	u2netPath := ModelPath()
	u2netAbsent, err := modelAbsent(u2netPath)
	if err != nil {
		return 0, "", err
	}
	if u2netAbsent {
		return kind, modnetPath, nil
	}
	fallbackWarning.Do(func() {
		log.Printf("MODNet model not found at %s, using %s instead; download MODNet from the Preferences dialogue or set %s to use another file", modnetPath, u2netPath, ModnetModelEnv)
	})
	return u2netModel, u2netPath, nil
}

// selectMattingModel names the model, and reports whether name asked for it
// rather than leaving it to the default.
func selectMattingModel(name string) (mattingModel, bool, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "modnet":
		return modnetModel, strings.TrimSpace(name) != "", nil
	case "u2net":
		return u2netModel, true, nil
	default:
		return 0, false, fmt.Errorf("unknown matting model %q; use modnet or u2net", name)
	}
}

// Close releases the model.
func (m *Matter) Close() {
	if m == nil {
		return
	}
	m.impl.close()
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

	mask, mw, mh, err := m.impl.alpha(rgba)
	if err != nil {
		return nil, err
	}
	applyMask(rgba, mask, mw, mh, m.background(w, h))
	return rgba, nil
}

// Matte returns the alpha matte for src at its own size: 0 is background and
// 255 is foreground. It is meant for inspection and comparison.
func (m *Matter) Matte(src image.Image) (*image.Gray, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("empty frame")
	}
	mask, mw, mh, err := m.impl.alpha(toRGBA(src))
	if err != nil {
		return nil, err
	}
	return scaleMask(mask, mw, mh, w, h), nil
}

// u2net runs the U²-Net human-segmentation model.
type u2net struct {
	session *ort.DynamicAdvancedSession
	input   *ort.Tensor[float32]
	small   []float32
}

func newU2Net(model string) (*u2net, error) {
	if _, err := os.Stat(model); err != nil {
		return nil, fmt.Errorf("U²-Net model not found at %s (set %s to use another file)", model, ModelEnv)
	}
	if err := onnx.Init(); err != nil {
		return nil, err
	}
	input, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, side, side))
	if err != nil {
		return nil, err
	}
	inputs, outputs, err := ort.GetInputOutputInfo(model)
	if err != nil || len(inputs) != 1 || len(outputs) == 0 {
		_ = input.Destroy()
		return nil, fmt.Errorf("unsupported U²-Net model %s", model)
	}
	session, err := ort.NewDynamicAdvancedSession(model, []string{inputs[0].Name}, []string{outputs[0].Name}, nil)
	if err != nil {
		_ = input.Destroy()
		return nil, fmt.Errorf("failed to load the U²-Net model: %w", err)
	}
	return &u2net{session: session, input: input, small: make([]float32, side*side)}, nil
}

func (u *u2net) alpha(img *image.RGBA) ([]float32, int, int, error) {
	b := img.Rect
	w, h := b.Dx(), b.Dy()
	data := u.input.GetData()
	plane := side * side
	for y := range side {
		sy := y * h / side
		for x := range side {
			sx := x * w / side
			off := img.PixOffset(sx, sy)
			for c := range 3 {
				data[c*plane+y*side+x] = (float32(img.Pix[off+c])/255 - mean[c]) / std[c]
			}
		}
	}

	outputs := []ort.Value{nil}
	if err := u.session.Run([]ort.Value{u.input}, outputs); err != nil {
		return nil, 0, 0, fmt.Errorf("segmentation failed: %w", err)
	}
	defer func() { _ = outputs[0].Destroy() }()
	mask, ok := outputs[0].(*ort.Tensor[float32])
	if !ok || len(mask.GetData()) != plane {
		return nil, 0, 0, errors.New("unexpected segmentation output")
	}
	normalise(mask.GetData(), u.small)
	return u.small, side, side, nil
}

func (u *u2net) close() {
	_ = u.session.Destroy()
	_ = u.input.Destroy()
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

// sampleMask bilinearly samples a mw by mh mask at pixel (x, y) of a w by h
// picture.
func sampleMask(mask []float32, mw, mh, w, h, x, y int) float32 {
	fy := (float32(y)+0.5)*float32(mh)/float32(h) - 0.5
	y0 := int(max(0, fy))
	y1 := min(y0+1, mh-1)
	ty := max(0, fy-float32(y0))
	fx := (float32(x)+0.5)*float32(mw)/float32(w) - 0.5
	x0 := int(max(0, fx))
	x1 := min(x0+1, mw-1)
	tx := max(0, fx-float32(x0))
	top := mask[y0*mw+x0]*(1-tx) + mask[y0*mw+x1]*tx
	bot := mask[y1*mw+x0]*(1-tx) + mask[y1*mw+x1]*tx
	return top*(1-ty) + bot*ty
}

func scaleMask(mask []float32, mw, mh, w, h int) *image.Gray {
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			out.Pix[y*out.Stride+x] = byte(255 * sampleMask(mask, mw, mh, w, h, x, y))
		}
	}
	return out
}

// applyMask scales the mask up bilinearly and premultiplies the picture by it.
// With a bg of the same size the person is blended over it and stays opaque.
func applyMask(img *image.RGBA, mask []float32, mw, mh int, bg *image.RGBA) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	for y := range h {
		for x := range w {
			a := sampleMask(mask, mw, mh, w, h, x, y)
			o := img.PixOffset(x, y)
			if bg != nil {
				inv := 1 - a
				bo := bg.PixOffset(x, y)
				img.Pix[o] = byte(float32(img.Pix[o])*a + float32(bg.Pix[bo])*inv)
				img.Pix[o+1] = byte(float32(img.Pix[o+1])*a + float32(bg.Pix[bo+1])*inv)
				img.Pix[o+2] = byte(float32(img.Pix[o+2])*a + float32(bg.Pix[bo+2])*inv)
				img.Pix[o+3] = 255
				continue
			}
			img.Pix[o] = byte(float32(img.Pix[o]) * a)
			img.Pix[o+1] = byte(float32(img.Pix[o+1]) * a)
			img.Pix[o+2] = byte(float32(img.Pix[o+2]) * a)
			img.Pix[o+3] = byte(255 * a)
		}
	}
}
