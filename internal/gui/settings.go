package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/pkg/protocol"
)

// Capture modes and source kinds selectable in the dialogue
const (
	ModeVideo      = "video"
	ModeReplay     = "replay"
	ModeScreenshot = "screenshot"
	// ModeCamera offers the source as a virtual camera.
	ModeCamera = "camera"

	SourceMonitor = "monitor"
	SourceWindow  = "window"
	SourceRegion  = "region"
)

// Settings holds the choices remembered between launches.
type Settings struct {
	ReplaySeconds int    `json:"replay_seconds"`
	Mode          string `json:"mode"`
	Source        string `json:"source"`
	Monitor       string `json:"monitor"`
	CropTop       int    `json:"crop_top"`
	Desktop       bool   `json:"desktop_audio"`
	Mic           bool   `json:"microphone"`
	MicDevice     string `json:"mic_device"`
	Countdown     int    `json:"countdown"`

	Camera         bool   `json:"camera"`
	CameraDevice   string `json:"camera_device"`
	CameraPosition string `json:"camera_position"`
	CameraSize     string `json:"camera_size"`
	CameraShape    string `json:"camera_shape"`
	CameraMirror   bool   `json:"camera_mirror"`

	CameraRemoveBackground bool `json:"camera_remove_background"`
	CameraNoRing           bool `json:"camera_no_ring"`
}

// DefaultSettings returns the settings used on first launch.
func DefaultSettings() Settings {
	return Settings{
		Mode:           ModeVideo,
		ReplaySeconds:  60,
		Source:         SourceMonitor,
		Desktop:        true,
		CameraPosition: external.CameraBottomRight,
		CameraSize:     external.CameraMedium,
		CameraShape:    external.CameraSquare,
	}
}

// settingsPath locates tequilashot.json under the user's configuration directory.
func settingsPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "tequilashot", "tequilashot.json"), nil
}

// LoadSettings reads settings from path, falling back to defaults for a
// missing or unreadable file and for out-of-range values.
func LoadSettings(path string) Settings {
	s := DefaultSettings()
	data, err := os.ReadFile(path) //nolint:gosec // path is the user's own settings file
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return DefaultSettings()
	}
	switch s.Mode {
	case ModeVideo, ModeScreenshot, ModeCamera, ModeReplay:
	default:
		s.Mode = ModeVideo
	}
	switch s.Source {
	case SourceMonitor, SourceWindow, SourceRegion:
	default:
		s.Source = SourceMonitor
	}
	if !external.ValidReplaySeconds(s.ReplaySeconds) {
		s.ReplaySeconds = 60
	}
	if s.CropTop < 0 {
		s.CropTop = 0
	}
	switch s.Countdown {
	case 0, 3, 5, 10:
	default:
		s.Countdown = 0
	}
	if !external.ValidCameraPosition(s.CameraPosition) {
		s.CameraPosition = external.CameraBottomRight
	}
	if !external.ValidCameraSize(s.CameraSize) {
		s.CameraSize = external.CameraMedium
	}
	if !external.ValidCameraShape(s.CameraShape) {
		s.CameraShape = external.CameraSquare
	}
	if !external.ValidCameraDevice(s.CameraDevice) {
		s.CameraDevice = ""
	}
	if external.ValidateMicDevice(s.MicDevice) != nil {
		s.MicDevice = ""
	}
	return s
}

// micLabels names the devices by description, numbering any that share one.
func micLabels(devices []external.AudioDevice) []string {
	count := map[string]int{}
	for _, d := range devices {
		count[d.Name]++
	}
	seen := map[string]int{}
	labels := make([]string, len(devices))
	for i, d := range devices {
		labels[i] = d.Name
		if count[d.Name] > 1 {
			seen[d.Name]++
			labels[i] = fmt.Sprintf("%s (%d)", d.Name, seen[d.Name])
		}
	}
	return labels
}

// SaveSettings writes settings to path, creating parent directories.
func SaveSettings(path string, s Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Selection is the concrete target chosen for a capture
type Selection struct {
	Monitor  string
	Geometry string
	// CameraDevice is the camera to use; empty when none is available.
	CameraDevice string
}

// BuildRequest converts the dialogue choices into a daemon request.
func BuildRequest(s Settings, sel Selection) (protocol.Request, error) {
	if s.Mode == ModeReplay {
		req := protocol.Request{Command: "execute", Action: "replay-start", Options: map[string]interface{}{
			"output": sel.Monitor, "seconds": s.ReplaySeconds, "audio": s.Desktop, "mic": s.Mic,
		}}
		if sel.Monitor == "" {
			return req, errors.New("no monitor selected")
		}
		if !external.ValidReplaySeconds(s.ReplaySeconds) {
			return req, errors.New("replay duration must be 30, 60 or 120 seconds")
		}
		if s.Mic && s.MicDevice != "" {
			req.Options["mic_device"] = s.MicDevice
		}
		return req, nil
	}
	if s.Mode == ModeScreenshot {
		req := protocol.Request{Command: "execute", Action: "screenshot", Options: map[string]interface{}{"delay": s.Countdown}}
		switch s.Source {
		case SourceMonitor:
			if sel.Monitor == "" {
				return req, errors.New("no monitor selected")
			}
			req.Options["output"] = sel.Monitor
		case SourceWindow:
			if sel.Geometry == "" {
				return req, errors.New("no window selected")
			}
			req.Options["geometry"] = sel.Geometry
		case SourceRegion:
		default:
			return req, errors.New("unknown source " + s.Source)
		}
		return req, nil
	}
	if s.Mode == ModeCamera {
		return buildCameraRequest(s, sel)
	}
	opts := map[string]interface{}{
		"delay": s.Countdown,
		"audio": s.Desktop,
		"mic":   s.Mic,
	}
	if s.Mic && s.MicDevice != "" {
		opts["mic_device"] = s.MicDevice
	}
	if s.Camera && sel.CameraDevice != "" {
		opts["camera"] = true
		opts["camera_device"] = sel.CameraDevice
		opts["camera_position"] = s.CameraPosition
		opts["camera_size"] = s.CameraSize
		opts["camera_shape"] = s.CameraShape
		opts["camera_mirror"] = s.CameraMirror
		opts["camera_remove_background"] = s.CameraRemoveBackground && s.CameraShape == external.CameraCircle
		opts["camera_no_ring"] = s.CameraNoRing && s.CameraRemoveBackground && s.CameraShape == external.CameraCircle
	}
	req := protocol.Request{Command: "execute", Options: opts}

	switch s.Source {
	case SourceMonitor:
		if sel.Monitor == "" {
			return req, errors.New("no monitor selected")
		}
		req.Action = "movie-screen"
		opts["output"] = sel.Monitor
		opts["crop_top"] = s.CropTop
	case SourceWindow:
		if sel.Geometry == "" {
			return req, errors.New("no window selected")
		}
		req.Action = "movie-geometry"
		opts["geometry"] = sel.Geometry
	case SourceRegion:
		req.Action = "movie-selection"
	default:
		return req, errors.New("unknown source " + s.Source)
	}
	return req, nil
}

// buildCameraRequest asks for the virtual camera; its webcam is always round.
func buildCameraRequest(s Settings, sel Selection) (protocol.Request, error) {
	opts := map[string]interface{}{}
	req := protocol.Request{Command: "execute", Action: "virtual-camera-start", Options: opts}
	if s.Camera && sel.CameraDevice != "" {
		opts["camera"] = true
		opts["camera_device"] = sel.CameraDevice
		opts["camera_position"] = s.CameraPosition
		opts["camera_size"] = s.CameraSize
		opts["camera_mirror"] = s.CameraMirror
		opts["camera_remove_background"] = s.CameraRemoveBackground
		opts["camera_no_ring"] = s.CameraNoRing && s.CameraRemoveBackground
	}
	switch s.Source {
	case SourceMonitor:
		if sel.Monitor == "" {
			return req, errors.New("no monitor selected")
		}
		opts["output"] = sel.Monitor
	case SourceWindow:
		if sel.Geometry == "" {
			return req, errors.New("no window selected")
		}
		opts["geometry"] = sel.Geometry
	case SourceRegion:
		opts["selection"] = true
	default:
		return req, errors.New("unknown source " + s.Source)
	}
	return req, nil
}
