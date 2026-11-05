package external

import (
	"os"
	"path/filepath"
	"strings"
)

const sysVideo4Linux = "/sys/class/video4linux"

// CameraName returns the human-readable name of a /dev/videoN device, or the
// device path when the kernel reports none.
func CameraName(device string) string {
	return CameraNameIn(sysVideo4Linux, device)
}

// CameraNameIn is CameraName with an alternative sysfs video4linux directory.
func CameraNameIn(root, device string) string {
	if !ValidCameraDevice(device) {
		return device
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.Base(device), "name")) //nolint:gosec // device is validated above
	if err != nil {
		return device
	}
	if name := strings.TrimSpace(string(data)); name != "" {
		return name
	}
	return device
}

// CameraLayout places the overlay within a frame exactly as gpu-screen-recorder
// does: a fraction of the frame width, inset 2 % horizontally and 3 % vertically.
// The aspect is width over height of the camera picture.
func CameraLayout(frameW, frameH, aspect float64, size, position string) (x, y, w, h float64) {
	if aspect <= 0 {
		aspect = 16.0 / 9.0
	}
	w = frameW * float64(cameraWidthPercent(size)) / 100
	h = w / aspect
	insetX, insetY := frameW*0.02, frameH*0.03

	x, y = frameW-w-insetX, frameH-h-insetY
	switch position {
	case CameraTopLeft:
		x, y = insetX, insetY
	case CameraTopRight:
		y = insetY
	case CameraBottomLeft:
		x = insetX
	}
	return x, y, w, h
}

// PreviewMode picks a modest capture mode for the live preview: the largest
// mode up to 640 wide, preferring the aspect of the recorded mode, else 640x480.
func PreviewMode(modes []CameraMode, device string) (width, height int) {
	bw, bh, _ := BestCameraMode(modes, device)
	pick := func(sameAspect bool) (int, int) {
		bestW, bestH := 0, 0
		for _, m := range modes {
			if m.Device != device || m.Width > 640 {
				continue
			}
			if sameAspect && (bw <= 0 || m.Width*bh != m.Height*bw) {
				continue
			}
			if m.Width*m.Height > bestW*bestH {
				bestW, bestH = m.Width, m.Height
			}
		}
		return bestW, bestH
	}
	if w, h := pick(true); w > 0 {
		return w, h
	}
	if w, h := pick(false); w > 0 {
		return w, h
	}
	return 640, 480
}

// ContainRect returns the rectangle a picture of the given aspect occupies when
// scaled to fit within a frame without cropping, centred.
func ContainRect(frameW, frameH, aspect float64) (x, y, w, h float64) {
	if aspect <= 0 || frameW <= 0 || frameH <= 0 {
		return 0, 0, frameW, frameH
	}
	w, h = frameW, frameW/aspect
	if h > frameH {
		w, h = frameH*aspect, frameH
	}
	return (frameW - w) / 2, (frameH - h) / 2, w, h
}
