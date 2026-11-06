package external

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Camera overlay positions and sizes accepted by CameraOverlay.
const (
	CameraTopLeft     = "top-left"
	CameraTopRight    = "top-right"
	CameraBottomLeft  = "bottom-left"
	CameraBottomRight = "bottom-right"

	CameraSmall  = "small"
	CameraMedium = "medium"
	CameraLarge  = "large"

	CameraSquare = "square"
	CameraCircle = "circle"
)

var cameraDevicePattern = regexp.MustCompile(`^/dev/video\d+$`)

// ValidCameraDevice reports whether device is a plain /dev/videoN path.
func ValidCameraDevice(device string) bool {
	return cameraDevicePattern.MatchString(device)
}

// ValidCameraPosition reports whether position names a supported corner.
func ValidCameraPosition(position string) bool {
	switch position {
	case CameraTopLeft, CameraTopRight, CameraBottomLeft, CameraBottomRight:
		return true
	}
	return false
}

// ValidCameraSize reports whether size names a supported overlay size.
func ValidCameraSize(size string) bool {
	switch size {
	case CameraSmall, CameraMedium, CameraLarge:
		return true
	}
	return false
}

// ValidCameraShape reports whether shape names a supported overlay shape.
func ValidCameraShape(shape string) bool {
	return shape == CameraSquare || shape == CameraCircle
}

// CameraOverlay describes a webcam picture-in-picture layered over a recording.
type CameraOverlay struct {
	Device   string // empty disables the overlay
	Position string // defaults to bottom-right
	Size     string // defaults to medium
	Shape    string // defaults to square
	Mirror   bool
	// RemoveBackground makes everything but the person transparent. It is
	// only available for the round camera, which is drawn by tequilashot.
	RemoveBackground bool
	// NoRing hides the white ring; it needs RemoveBackground.
	NoRing bool
	Width  int // chosen camera mode; zero lets gpu-screen-recorder pick
	Height int
	FPS    int
}

// Enabled reports whether the overlay is switched on.
func (c CameraOverlay) Enabled() bool {
	return c.Device != ""
}

// Round reports whether the overlay is drawn as an on-screen circle rather
// than composited by gpu-screen-recorder.
func (c CameraOverlay) Round() bool {
	return c.Enabled() && c.Shape == CameraCircle
}

// Validate rejects values that could alter the gpu-screen-recorder source syntax,
// which splits on '|' and ';'.
func (c CameraOverlay) Validate() error {
	if !ValidCameraDevice(c.Device) {
		return fmt.Errorf("invalid camera device %q, expected /dev/videoN", c.Device)
	}
	if c.Position != "" && !ValidCameraPosition(c.Position) {
		return fmt.Errorf("invalid camera position %q (valid: top-left, top-right, bottom-left, bottom-right)", c.Position)
	}
	if c.Size != "" && !ValidCameraSize(c.Size) {
		return fmt.Errorf("invalid camera size %q (valid: small, medium, large)", c.Size)
	}
	if c.Shape != "" && !ValidCameraShape(c.Shape) {
		return fmt.Errorf("invalid camera shape %q (valid: square, circle)", c.Shape)
	}
	if c.RemoveBackground && !c.Round() {
		return fmt.Errorf("removing the camera background needs the circle shape")
	}
	if c.NoRing && !c.RemoveBackground {
		return fmt.Errorf("hiding the camera ring needs the background removed")
	}
	if c.Width < 0 || c.Height < 0 || c.FPS < 0 {
		return fmt.Errorf("invalid camera mode %dx%d@%d", c.Width, c.Height, c.FPS)
	}
	return nil
}

// cameraWidthPercent is the overlay width as a percentage of the frame width.
func cameraWidthPercent(size string) int {
	switch size {
	case CameraSmall:
		return 15
	case CameraLarge:
		return 35
	}
	return 25
}

// gsrSource returns the camera source of the gpu-screen-recorder -w value.
func (c CameraOverlay) gsrSource() string {
	width := fmt.Sprintf("%d%%", cameraWidthPercent(c.Size))

	halign, valign, x, y := "end", "end", "-2%", "-3%"
	switch c.Position {
	case CameraTopLeft:
		halign, valign, x, y = "start", "start", "2%", "3%"
	case CameraTopRight:
		halign, valign, x, y = "end", "start", "-2%", "3%"
	case CameraBottomLeft:
		halign, valign, x, y = "start", "end", "2%", "-3%"
	}

	parts := []string{
		c.Device,
		"width=" + width,
		"halign=" + halign,
		"valign=" + valign,
		"x=" + x,
		"y=" + y,
	}
	if c.Mirror {
		parts = append(parts, "hflip=true")
	}
	if c.Width > 0 && c.Height > 0 {
		parts = append(parts, fmt.Sprintf("camera_width=%d", c.Width), fmt.Sprintf("camera_height=%d", c.Height))
		if c.FPS > 0 {
			parts = append(parts, fmt.Sprintf("camera_fps=%d", c.FPS))
		}
	}
	return strings.Join(parts, ";")
}

// CameraMode is one capture mode reported by gpu-screen-recorder.
type CameraMode struct {
	Device string
	Width  int
	Height int
	FPS    float64
	Format string
}

// ParseCameraModes parses --list-v4l2-devices output, skipping malformed lines.
func ParseCameraModes(listing string) []CameraMode {
	var modes []CameraMode
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "|")
		if len(fields) != 3 || !ValidCameraDevice(fields[0]) {
			continue
		}
		size, rate, ok := strings.Cut(fields[1], "@")
		if !ok {
			continue
		}
		ws, hs, ok := strings.Cut(size, "x")
		if !ok {
			continue
		}
		w, werr := strconv.Atoi(ws)
		h, herr := strconv.Atoi(hs)
		if werr != nil || herr != nil || w <= 0 || h <= 0 {
			continue
		}
		rateNum, ok := strings.CutSuffix(rate, "hz")
		if !ok {
			continue
		}
		fps, err := strconv.ParseFloat(rateNum, 64)
		if err != nil || fps <= 0 || math.IsInf(fps, 0) || math.IsNaN(fps) {
			continue
		}
		modes = append(modes, CameraMode{Device: fields[0], Width: w, Height: h, FPS: fps, Format: fields[2]})
	}
	return modes
}

// BestCameraMode picks a mode for device: mjpeg 1280x720 at the highest rate up
// to 30, else the largest mode up to 1920 wide. It returns zeros when nothing
// suits, and a zero rate when the chosen rate is not a whole number.
func BestCameraMode(modes []CameraMode, device string) (width, height, fps int) {
	var best *CameraMode
	better := func(m, cur *CameraMode) bool {
		if cur == nil {
			return true
		}
		if m.Width*m.Height != cur.Width*cur.Height {
			return m.Width*m.Height > cur.Width*cur.Height
		}
		return rate(m) > rate(cur)
	}

	for i := range modes {
		m := &modes[i]
		if m.Device == device && m.Format == "mjpeg" && m.Width == 1280 && m.Height == 720 && m.FPS < 31 && (best == nil || rate(m) > rate(best)) {
			best = m
		}
	}
	if best == nil {
		for i := range modes {
			m := &modes[i]
			if m.Device == device && m.Width <= 1920 && better(m, best) {
				best = m
			}
		}
	}
	if best == nil {
		return 0, 0, 0
	}
	if best.FPS != math.Trunc(best.FPS) {
		return best.Width, best.Height, 0
	}
	return best.Width, best.Height, int(best.FPS)
}

// rate returns the whole-number rate used for comparisons, preferring rates up to 30.
func rate(m *CameraMode) int {
	r := int(math.Floor(m.FPS))
	if r > 30 {
		return -r
	}
	return r
}

// ListCameraModes returns every capture mode gpu-screen-recorder reports for
// webcams, leaving out v4l2loopback devices such as the virtual camera.
func ListCameraModes(ctx context.Context) ([]CameraMode, error) {
	out, err := exec.CommandContext(ctx, "gpu-screen-recorder", "--list-v4l2-devices").Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to list cameras: %w", err)
	}
	return withoutDevices(ParseCameraModes(string(out)), LoopbackDevices()), nil
}

// CameraDevices returns the distinct devices in modes, in listing order.
func CameraDevices(modes []CameraMode) []string {
	var devices []string
	seen := map[string]bool{}
	for _, m := range modes {
		if !seen[m.Device] {
			seen[m.Device] = true
			devices = append(devices, m.Device)
		}
	}
	return devices
}

// ListCameras returns the capture-capable camera devices.
func ListCameras(ctx context.Context) ([]string, error) {
	modes, err := ListCameraModes(ctx)
	if err != nil {
		return nil, err
	}
	return CameraDevices(modes), nil
}

// CameraIndex returns the position of want among devices, or 0 when absent.
func CameraIndex(devices []string, want string) int {
	for i, d := range devices {
		if d == want {
			return i
		}
	}
	return 0
}
