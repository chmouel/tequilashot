//go:build !nogui

package onnx

import (
	"errors"
	"strings"
	"testing"
)

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
