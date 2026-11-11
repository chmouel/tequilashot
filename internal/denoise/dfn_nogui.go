//go:build nogui

package denoise

import "errors"

var errNoRuntime = errors.New("noise removal is unavailable: built without ONNX Runtime support")

func loadModel(string) (model, func(), error) { return nil, nil, errNoRuntime }

func runtimeReady() error { return errNoRuntime }
