package gui

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/chmouel/tequilashot/internal/sway"
)

const (
	windowPreviewWidth  = 112
	windowPreviewHeight = 64
)

func captureWindowPreview(ctx context.Context, w sway.Window) ([]byte, error) {
	if w.Width <= 0 || w.Height <= 0 {
		return nil, fmt.Errorf("the window has no visible area")
	}
	scale := min(1.0, float64(windowPreviewWidth)/float64(w.Width), float64(windowPreviewHeight)/float64(w.Height))
	return exec.CommandContext(ctx, "grim", "-g", w.Geometry(), //nolint:gosec
		"-s", strconv.FormatFloat(scale, 'f', 8, 64), "-t", "jpeg", "-q", "70", "-").Output()
}
