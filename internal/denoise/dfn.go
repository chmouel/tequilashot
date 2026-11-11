//go:build !nogui

package denoise

import (
	"fmt"

	"github.com/chmouel/tequilashot/internal/onnx"

	ort "github.com/yalue/onnxruntime_go"
)

// loadModel opens the DeepFilterNet3 graph at path. The returned function
// runs it on a block of frames; close releases it.
func loadModel(path string) (run model, closeFn func(), err error) {
	if err := onnx.Init(); err != nil {
		return nil, nil, err
	}
	session, err := ort.NewDynamicAdvancedSession(path,
		[]string{"feat_erb", "feat_spec"}, []string{"erb_mask", "df_coefs"}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("load the noise-removal model %s: %w", path, err)
	}
	run = func(featErb, featSpec []float32, t int) ([]float32, []float32, error) {
		erb, err := ort.NewTensor(ort.NewShape(1, 1, int64(t), erbBands), featErb)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = erb.Destroy() }()
		spec, err := ort.NewTensor(ort.NewShape(1, 2, int64(t), dfBins), featSpec)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = spec.Destroy() }()
		outputs := []ort.Value{nil, nil}
		if err := session.Run([]ort.Value{erb, spec}, outputs); err != nil {
			return nil, nil, fmt.Errorf("noise removal failed: %w", err)
		}
		defer func() {
			for _, o := range outputs {
				if o != nil {
					_ = o.Destroy()
				}
			}
		}()
		mask, ok1 := outputs[0].(*ort.Tensor[float32])
		coefs, ok2 := outputs[1].(*ort.Tensor[float32])
		if !ok1 || !ok2 {
			return nil, nil, fmt.Errorf("the noise-removal model returned unexpected outputs")
		}
		return append([]float32(nil), mask.GetData()...), append([]float32(nil), coefs.GetData()...), nil
	}
	return run, func() { _ = session.Destroy() }, nil
}

// runtimeReady returns an error when ONNX Runtime cannot be found.
func runtimeReady() error {
	_, err := onnx.LibraryPath()
	return err
}
