package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"sway-easyshot/internal/external"
	"sway-easyshot/pkg/protocol"
)

// Capture modes and source kinds selectable in the dialogue
const (
	ModeVideo      = "video"
	ModeScreenshot = "screenshot"

	SourceMonitor = "monitor"
	SourceWindow  = "window"
	SourceRegion  = "region"
)

// Settings holds the choices remembered between launches.
type Settings struct {
	Mode      string `json:"mode"`
	Source    string `json:"source"`
	Monitor   string `json:"monitor"`
	CropTop   int    `json:"crop_top"`
	Desktop   bool   `json:"desktop_audio"`
	Mic       bool   `json:"microphone"`
	MicDevice string `json:"mic_device"`
	Countdown int    `json:"countdown"`

	Camera         bool   `json:"camera"`
	CameraDevice   string `json:"camera_device"`
	CameraPosition string `json:"camera_position"`
	CameraSize     string `json:"camera_size"`
	CameraMirror   bool   `json:"camera_mirror"`
}

// DefaultSettings returns the settings used on first launch.
func DefaultSettings() Settings {
	return Settings{
		Mode:           ModeVideo,
		Source:         SourceMonitor,
		Desktop:        true,
		CameraPosition: external.CameraBottomRight,
		CameraSize:     external.CameraMedium,
	}
}

// settingsPath locates sway-easyshot.json under the user's configuration directory.
func settingsPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "sway-easyshot", "sway-easyshot.json"), nil
}

// LoadSettings reads settings from path, falling back to defaults for a
// missing or unreadable file and for out-of-range values.
func LoadSettings(path string) Settings {
	s := DefaultSettings()
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return DefaultSettings()
	}
	if s.Mode != ModeVideo && s.Mode != ModeScreenshot {
		s.Mode = ModeVideo
	}
	switch s.Source {
	case SourceMonitor, SourceWindow, SourceRegion:
	default:
		s.Source = SourceMonitor
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
		opts["camera_mirror"] = s.CameraMirror
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
