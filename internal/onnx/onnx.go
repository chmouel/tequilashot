//go:build !nogui

// Package onnx starts the system ONNX Runtime once per process for the
// packages that run models with it.
package onnx

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chmouel/tequilashot/internal/prefs"

	ort "github.com/yalue/onnxruntime_go"
)

// LibraryEnv names the ONNX Runtime shared library to load.
const LibraryEnv = prefs.EnvOnnxRuntimeLib

// minRuntime is the oldest ONNX Runtime providing the API the binding uses.
const minRuntime = "1.29"

var (
	libraryCandidates = []string{
		"/usr/lib/libonnxruntime.so",
		"/usr/lib64/libonnxruntime.so",
		"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
		"/usr/lib/aarch64-linux-gnu/libonnxruntime.so",
		"/usr/local/lib/libonnxruntime.so",
	}

	versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?$`)

	envOnce sync.Once
	envErr  error
	loaded  atomic.Value
)

// LibraryPath returns the ONNX Runtime library to load: the environment, then
// the preferences, then the usual system locations.
func LibraryPath() (string, error) {
	if p := os.Getenv(LibraryEnv); p != "" {
		return p, nil
	}
	if p := prefs.Current().OnnxRuntimeLib; p != "" {
		return p, nil
	}
	for _, p := range libraryCandidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ONNX Runtime library not found; install it or set %s", LibraryEnv)
}

// LoadedLibrary returns the ONNX Runtime library this process has loaded, or
// an empty string. A different library takes effect only in a new process.
func LoadedLibrary() string {
	lib, _ := loaded.Load().(string)
	return lib
}

// Init starts ONNX Runtime. Only the first call does any work; later calls
// return its outcome.
func Init() error {
	envOnce.Do(func() {
		lib, err := LibraryPath()
		if err != nil {
			envErr = err
			return
		}
		ort.SetSharedLibraryPath(lib)
		if err := ort.InitializeEnvironment(); err != nil {
			envErr = startError(lib, err)
			return
		}
		loaded.Store(lib)
	})
	return envErr
}

// startError explains a failed start. The binding is built against a fixed
// ONNX Runtime API; a library that predates it cannot provide that API.
func startError(lib string, err error) error {
	if !strings.Contains(err.Error(), "ORT API base") {
		return fmt.Errorf("failed to start ONNX Runtime from %s: %w", lib, err)
	}
	found := lib
	if resolved, rerr := filepath.EvalSymlinks(lib); rerr == nil {
		found = resolved
	}
	have := "an older version"
	if m := versionRe.FindString(filepath.Base(found)); m != "" {
		have = m
	}
	return fmt.Errorf("%s (%s) is too old: this build needs ONNX Runtime %s or newer; upgrade it or set %s", found, have, minRuntime, LibraryEnv)
}
