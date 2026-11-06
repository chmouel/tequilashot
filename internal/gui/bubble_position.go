package gui

import (
	"fmt"
	"image"
	"math"

	"github.com/chmouel/tequilashot/internal/external"
)

func nextBubbleCorner(area, monitor image.Rectangle, diameter int, size string, current image.Point) (image.Point, string, error) {
	visible := area.Intersect(monitor)
	if visible.Dx() < diameter || visible.Dy() < diameter {
		return image.Point{}, "", fmt.Errorf("the recorded area is too small for the round camera on this monitor")
	}
	positions := []string{external.CameraTopLeft, external.CameraTopRight, external.CameraBottomRight, external.CameraBottomLeft}
	corners := make([]image.Point, len(positions))
	nearest, nearestDistance := 0, math.MaxInt
	for i, position := range positions {
		x, y, _ := external.CircleLayout(float64(area.Dx()), float64(area.Dy()), size, position)
		corners[i] = image.Pt(
			max(visible.Min.X, min(area.Min.X+int(math.Round(x)), visible.Max.X-diameter)),
			max(visible.Min.Y, min(area.Min.Y+int(math.Round(y)), visible.Max.Y-diameter)),
		)
		distance := absInt(current.X-corners[i].X) + absInt(current.Y-corners[i].Y)
		if distance < nearestDistance {
			nearest, nearestDistance = i, distance
		}
	}
	if nearestDistance <= 30 {
		nearest = (nearest + 1) % len(corners)
	}
	return corners[nearest], positions[nearest], nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
