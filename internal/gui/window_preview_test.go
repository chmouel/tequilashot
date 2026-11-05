package gui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chmouel/tequilashot/internal/sway"
)

func TestCaptureWindowPreview(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "grim"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, w := range []sway.Window{
		{X: -1920, Y: 20, Width: 1920, Height: 1080},
		{X: 30, Y: -2160, Width: 1080, Height: 2160},
		{Width: 60, Height: 40},
	} {
		data, err := captureWindowPreview(context.Background(), w)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(args) != 9 || args[0] != "-g" || args[1] != w.Geometry() || args[2] != "-s" || strings.Join(args[4:], " ") != "-t jpeg -q 70 -" {
			t.Fatalf("unexpected capture arguments: %q", args)
		}
		scale, err := strconv.ParseFloat(args[3], 64)
		if err != nil || scale <= 0 || scale > 1 || float64(w.Width)*scale > windowPreviewWidth+0.001 || float64(w.Height)*scale > windowPreviewHeight+0.001 {
			t.Fatalf("preview exceeds its bounds: %v, scale %s", w, args[3])
		}
	}
	for _, w := range []sway.Window{{Width: 0, Height: 100}, {Width: 100, Height: -1}} {
		if _, err := captureWindowPreview(context.Background(), w); err == nil {
			t.Errorf("expected an error for a window without visible area: %v", w)
		}
	}
}
