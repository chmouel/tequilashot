// Package prefs holds the preferences edited in the Preferences dialogue and
// shared by the dialogue, the daemon and the helpers they start.
package prefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
)

// Environment variables that take precedence over the preferences file.
const (
	EnvSaveLocation      = "TEQUILASHOT_SAVE_LOCATION"
	EnvAIModel           = "TEQUILASHOT_AI_MODEL"
	EnvSegmentationModel = "TEQUILASHOT_SEGMENTATION_MODEL"
	EnvOnnxRuntimeLib    = "TEQUILASHOT_ONNXRUNTIME_LIB"
)

// Defaults for the values the dialogue offers.
const (
	DefaultFrameRate     = 60
	DefaultMaxWidth      = 1920
	DefaultCleanupDays   = 3
	DefaultReplayBitrate = 20000
	DefaultAIModel       = "gemini:gemini-2.5-flash-image"

	MinCleanupDays   = 1
	MaxCleanupDays   = 365
	MinReplayBitrate = 2000
	MaxReplayBitrate = 100000
)

// Choices offered for the enumerated recording preferences; the empty string
// leaves the decision to gpu-screen-recorder and a width of 0 keeps the
// source size.
var (
	FrameRates  = []int{24, 30, 60, 120, 144}
	MaxWidths   = []int{0, 1280, 1920, 2560, 3840}
	VideoCodecs = []string{"", "h264", "hevc", "av1"}
	Qualities   = []string{"", "medium", "high", "very_high", "ultra"}
	Encoders    = []string{"", "cpu"}
)

// Programs lists the helpers whose location may be configured. Compositor
// and system tools (swaymsg, hyprctl, pactl, pgrep, gdbus, xdg-open) are
// always taken from PATH.
var Programs = []string{
	"gpu-screen-recorder", "grim", "slurp", "wf-recorder", "gst-launch-1.0",
	"ffmpeg", "ffprobe", "wl-copy", "wl-paste", "satty", "aichat", "zenity",
	"wofi", "nautilus",
}

// Prefs are the user's preferences. Start from Default rather than the zero
// value.
type Prefs struct {
	SaveLocation string `json:"save_location,omitempty"`
	// SaveLocationChanged records that the folder was chosen or reset in the
	// dialogue, which withdraws the implicit clean-up consent.
	SaveLocationChanged bool `json:"save_location_changed,omitempty"`
	CleanupEnabled      bool `json:"cleanup_enabled"`
	// CleanupFolder is the folder the user agreed to have cleaned.
	CleanupFolder string `json:"cleanup_folder,omitempty"`
	CleanupDays   int    `json:"cleanup_days"`

	FrameRate     int    `json:"frame_rate"`
	MaxWidth      int    `json:"max_width"`
	VideoCodec    string `json:"video_codec,omitempty"`
	Quality       string `json:"quality,omitempty"`
	RecordCursor  bool   `json:"record_cursor"`
	Encoder       string `json:"encoder,omitempty"`
	RecordingArgs string `json:"recording_args,omitempty"`

	ReplayBitrate int    `json:"replay_bitrate_kbps"`
	ReplayArgs    string `json:"replay_args,omitempty"`

	ScreenshotCursor bool   `json:"screenshot_cursor"`
	SelectionColour  string `json:"selection_colour,omitempty"`
	AIModel          string `json:"ai_model,omitempty"`

	ProgramPaths map[string]string `json:"programs,omitempty"`

	SegmentationModel string `json:"segmentation_model,omitempty"`
	OnnxRuntimeLib    string `json:"onnxruntime_lib,omitempty"`
	ModelMirror       string `json:"model_mirror,omitempty"`
}

// Default returns the preferences used when nothing has been saved.
func Default() Prefs {
	return Prefs{
		CleanupEnabled: true,
		CleanupDays:    DefaultCleanupDays,
		FrameRate:      DefaultFrameRate,
		MaxWidth:       DefaultMaxWidth,
		RecordCursor:   true,
		ReplayBitrate:  DefaultReplayBitrate,
	}
}

// Clone returns a copy that can be modified without affecting p.
func (p Prefs) Clone() Prefs {
	p.ProgramPaths = maps.Clone(p.ProgramPaths)
	return p
}

// Path locates preferences.json under the user's configuration directory.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "tequilashot", "preferences.json")
}

// Load reads preferences from path. A missing file yields the defaults and
// invalid values are replaced by their defaults with a log message. A file
// that exists but cannot be read as a preferences object yields the defaults
// with old-file deletion switched off, so that a damaged file cannot cause
// deletions the user did not choose.
func Load(path string) (Prefs, error) {
	p := Default()
	data, err := os.ReadFile(path) //nolint:gosec // the user's own preferences file
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
		return refuse(p), err
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return refuse(p), fmt.Errorf("%s: not a preferences object", path)
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return refuse(p), fmt.Errorf("%s: %w", path, err)
	}
	for _, msg := range p.sanitise() {
		log.Printf("preferences: %s", msg)
	}
	return p, nil
}

// refuse keeps the defaults for harmless settings and switches off the one
// setting that can delete the user's files.
func refuse(p Prefs) Prefs {
	p.CleanupEnabled = false
	return p
}

// sanitise resets invalid values to their defaults and describes each reset.
func (p *Prefs) sanitise() []string {
	var msgs []string
	d := Default()
	check := func(ok bool, field string, fix func()) {
		if !ok {
			msgs = append(msgs, "invalid "+field+", using the default")
			fix()
		}
	}
	// An unusable period is never shortened to the default: that would delete
	// files the user meant to keep, so deletion stops until a period is set.
	if p.CleanupDays < MinCleanupDays || p.CleanupDays > MaxCleanupDays {
		msgs = append(msgs, "invalid cleanup_days, old files are no longer deleted")
		p.CleanupDays = d.CleanupDays
		p.CleanupEnabled = false
	}
	check(slices.Contains(FrameRates, p.FrameRate), "frame_rate", func() { p.FrameRate = d.FrameRate })
	check(slices.Contains(MaxWidths, p.MaxWidth), "max_width", func() { p.MaxWidth = d.MaxWidth })
	check(slices.Contains(VideoCodecs, p.VideoCodec), "video_codec", func() { p.VideoCodec = "" })
	check(slices.Contains(Qualities, p.Quality), "quality", func() { p.Quality = "" })
	check(slices.Contains(Encoders, p.Encoder), "encoder", func() { p.Encoder = "" })
	check(p.ReplayBitrate >= MinReplayBitrate && p.ReplayBitrate <= MaxReplayBitrate, "replay_bitrate_kbps", func() { p.ReplayBitrate = d.ReplayBitrate })
	check(p.SelectionColour == "" || ValidColour(p.SelectionColour), "selection_colour", func() { p.SelectionColour = "" })
	_, err := RecorderArgs(p.RecordingArgs, false)
	check(err == nil, "recording_args", func() { p.RecordingArgs = "" })
	_, err = RecorderArgs(p.ReplayArgs, true)
	check(err == nil, "replay_args", func() { p.ReplayArgs = "" })
	check(p.ModelMirror == "" || ValidateMirror(p.ModelMirror) == nil, "model_mirror", func() { p.ModelMirror = "" })
	for name, path := range p.ProgramPaths {
		check(slices.Contains(Programs, name) && strings.TrimSpace(path) != "", "programs."+name, func() { delete(p.ProgramPaths, name) })
	}
	return msgs
}

// Save writes p to path atomically, creating parent directories.
func Save(path string, p Prefs) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".preferences-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var current atomic.Pointer[Prefs]

// Current returns the preferences in effect for this process. Treat the
// result as read-only; use Clone before changing it.
func Current() Prefs {
	if p := current.Load(); p != nil {
		return *p
	}
	return Default()
}

// Set makes p the preferences in effect for this process.
func Set(p Prefs) {
	p = p.Clone()
	current.Store(&p)
}

// Reload reads the preferences file again. A file that cannot be read or
// parsed is reported and its fail-closed value is published, so callers never
// act on an administrative default the user may have turned off.
func Reload() error {
	p, err := Load(Path())
	if err != nil {
		log.Printf("preferences: %v", err)
	}
	Set(p)
	return err
}

// SegmentationModelPath returns the background-removal model file: the
// environment, then the preferences, then the default location.
func SegmentationModelPath() string {
	if p := os.Getenv(EnvSegmentationModel); p != "" {
		return p
	}
	if p := Current().SegmentationModel; p != "" {
		return p
	}
	return DefaultSegmentationModelPath()
}

// DefaultSegmentationModelPath is where the model is looked for and
// downloaded when no other location is configured.
func DefaultSegmentationModelPath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "tequilashot", "u2net_human_seg.onnx")
}

// Program returns the configured path for a helper, or its name.
func (p Prefs) Program(name string) string {
	if path := strings.TrimSpace(p.ProgramPaths[name]); path != "" {
		return path
	}
	return name
}

// Bin returns the program to run for a helper under the current preferences.
func Bin(name string) string {
	return Current().Program(name)
}

// LegacySaveDir is the save folder chosen without the preferences file: the
// environment override or ~/Downloads/Screenshots.
func LegacySaveDir() string {
	if dir := os.Getenv(EnvSaveLocation); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads", "Screenshots")
}

// SaveDir returns the folder captures are written to; the environment
// override wins over the preferences.
func (p Prefs) SaveDir() string {
	if dir := os.Getenv(EnvSaveLocation); dir != "" {
		return dir
	}
	if p.SaveLocation != "" {
		return p.SaveLocation
	}
	return LegacySaveDir()
}

// AIModelName returns the aichat model used to name screenshots.
func (p Prefs) AIModelName() string {
	if m := os.Getenv(EnvAIModel); m != "" {
		return m
	}
	if p.AIModel != "" {
		return p.AIModel
	}
	return DefaultAIModel
}

// CleanupActive reports whether old files in saveDir may be deleted.
// Until the folder is changed in the dialogue, the original behaviour
// applies to the legacy folder; afterwards the user must agree to clean-up
// for the specific folder in use.
func (p Prefs) CleanupActive(saveDir string) bool {
	return p.CleanupActiveWith(saveDir, LegacySaveDir())
}

// CleanupActiveWith is CleanupActive with the legacy folder supplied by the
// caller, so the GUI can judge using the daemon's environment.
func (p Prefs) CleanupActiveWith(saveDir, legacy string) bool {
	if !p.CleanupEnabled {
		return false
	}
	consent := p.CleanupFolder
	if consent == "" {
		if p.SaveLocationChanged {
			return false
		}
		consent = legacy
	}
	return SameDir(consent, saveDir)
}

// SameDir reports whether two paths name the same directory, lexically.
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if abs, err := filepath.Abs(a); err == nil {
		a = abs
	}
	if abs, err := filepath.Abs(b); err == nil {
		b = abs
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

var colourRE = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// ValidColour accepts #RRGGBB and #RRGGBBAA.
func ValidColour(c string) bool { return colourRE.MatchString(c) }

// ValidateMirror accepts an https URL for the model download.
func ValidateMirror(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("the mirror must be an https:// address")
	}
	return nil
}
