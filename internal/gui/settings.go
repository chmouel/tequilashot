package gui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"sway-easyshot/pkg/protocol"
)

// Source kinds selectable in the dialogue.
const (
	SourceMonitor = "monitor"
	SourceWindow  = "window"
	SourceRegion  = "region"
)

// Settings holds the choices remembered between launches.
type Settings struct {
	Source    string `json:"source"`
	Monitor   string `json:"monitor"`
	CropTop   int    `json:"crop_top"`
	Desktop   bool   `json:"desktop_audio"`
	Mic       bool   `json:"microphone"`
	Countdown int    `json:"countdown"`
}

// DefaultSettings returns the settings used on first launch.
func DefaultSettings() Settings {
	return Settings{Source: SourceMonitor, Desktop: true}
}

// settingsPath locates record-gui.json under the user's configuration directory.
func settingsPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "sway-easyshot", "record-gui.json"), nil
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
	return s
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

// Selection is the concrete target chosen for a recording.
type Selection struct {
	Monitor  string
	Geometry string
}

// BuildRequest converts the dialogue choices into a daemon request.
func BuildRequest(s Settings, sel Selection) (protocol.Request, error) {
	opts := map[string]interface{}{
		"delay": s.Countdown,
		"audio": s.Desktop,
		"mic":   s.Mic,
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
