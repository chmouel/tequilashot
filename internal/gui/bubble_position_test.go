package gui

import (
	"image"
	"testing"

	"github.com/chmouel/tequilashot/internal/external"
)

func TestBubbleCornerCycle(t *testing.T) {
	area := image.Rect(100, 200, 1100, 800)
	monitor := image.Rect(0, 0, 1920, 1080)
	position := image.Pt(939, 641)
	for _, want := range []struct {
		position image.Point
		corner   string
	}{
		{image.Pt(120, 641), external.CameraBottomLeft},
		{image.Pt(120, 218), external.CameraTopLeft},
		{image.Pt(939, 218), external.CameraTopRight},
		{image.Pt(939, 641), external.CameraBottomRight},
	} {
		got, corner, err := nextBubbleCorner(area, monitor, 141, external.CameraMedium, position)
		if err != nil || got != want.position || corner != want.corner {
			t.Fatalf("from %v got %v %q %v, want %v %q", position, got, corner, err, want.position, want.corner)
		}
		position = got
	}
	got, corner, err := nextBubbleCorner(area, monitor, 141, external.CameraMedium, image.Pt(400, 400))
	if err != nil || got != image.Pt(120, 218) || corner != external.CameraTopLeft {
		t.Fatalf("off-corner position: %v %q %v", got, corner, err)
	}
}

func TestBubbleCornerClipsToRecordedAreaAndMonitor(t *testing.T) {
	area := image.Rect(-100, 100, 900, 700)
	monitor := image.Rect(0, 0, 500, 800)
	got, corner, err := nextBubbleCorner(area, monitor, 141, external.CameraMedium, image.Pt(0, 118))
	if err != nil || corner != external.CameraTopRight || got != image.Pt(359, 118) {
		t.Fatalf("clipped corner: %v %q %v", got, corner, err)
	}
	if _, _, err := nextBubbleCorner(area, image.Rect(0, 0, 100, 800), 141, external.CameraMedium, image.Point{}); err == nil {
		t.Fatal("accepted an area narrower than the camera")
	}
}
