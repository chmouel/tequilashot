package gui

import (
	"os"
	"path/filepath"
	"testing"

	"sway-easyshot/internal/external"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "record-gui.json")
	want := Settings{Source: SourceWindow, Monitor: "DP-1", CropTop: 40, Desktop: false, Mic: true, Countdown: 5,
		Camera: true, CameraDevice: "/dev/video2", CameraPosition: "top-left", CameraSize: "large", CameraMirror: true}
	if err := SaveSettings(path, want); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(path); got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestLoadSettingsFallbacks(t *testing.T) {
	dir := t.TempDir()
	if got := LoadSettings(filepath.Join(dir, "missing.json")); got != DefaultSettings() {
		t.Fatalf("missing file: %+v", got)
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"source":"x","crop_top":-3,"countdown":7}`), 0o600)
	got := LoadSettings(bad)
	if got.Source != SourceMonitor || got.CropTop != 0 || got.Countdown != 0 {
		t.Fatalf("sanitising failed: %+v", got)
	}
	if got.CameraPosition != "bottom-right" || got.CameraSize != "medium" || got.CameraDevice != "" {
		t.Fatalf("camera defaults missing: %+v", got)
	}

	badCam := filepath.Join(dir, "badcam.json")
	_ = os.WriteFile(badCam, []byte(`{"camera":true,"camera_device":"/dev/video0|DP-1","camera_position":"centre","camera_size":"huge"}`), 0o600)
	got = LoadSettings(badCam)
	if !got.Camera || got.CameraDevice != "" || got.CameraPosition != "bottom-right" || got.CameraSize != "medium" {
		t.Fatalf("camera sanitising failed: %+v", got)
	}
}

func TestBuildRequest(t *testing.T) {
	s := Settings{Source: SourceMonitor, CropTop: 30, Desktop: true, Countdown: 3}
	req, err := BuildRequest(s, Selection{Monitor: "DP-2"})
	if err != nil || req.Action != "movie-screen" || req.Options["output"] != "DP-2" || req.Options["crop_top"] != 30 || req.Options["delay"] != 3 {
		t.Fatalf("monitor: %+v %v", req, err)
	}
	s.Source = SourceWindow
	req, err = BuildRequest(s, Selection{Geometry: "1,2 3x4"})
	if err != nil || req.Action != "movie-geometry" || req.Options["geometry"] != "1,2 3x4" {
		t.Fatalf("window: %+v %v", req, err)
	}
	if _, err = BuildRequest(s, Selection{}); err == nil {
		t.Fatal("expected error without window")
	}
	s.Source = SourceRegion
	if req, err = BuildRequest(s, Selection{}); err != nil || req.Action != "movie-selection" {
		t.Fatalf("region: %+v %v", req, err)
	}
}

func TestBuildRequestCamera(t *testing.T) {
	s := DefaultSettings()
	s.Source = SourceRegion
	s.CameraPosition, s.CameraSize, s.CameraMirror = "top-right", "small", true

	for name, tc := range map[string]struct {
		camera bool
		device string
	}{"off": {false, "/dev/video0"}, "no device": {true, ""}} {
		s.Camera = tc.camera
		req, _ := BuildRequest(s, Selection{CameraDevice: tc.device})
		if _, ok := req.Options["camera"]; ok {
			t.Errorf("%s: camera option present: %+v", name, req.Options)
		}
	}

	s.Camera = true
	req, _ := BuildRequest(s, Selection{CameraDevice: "/dev/video4"})
	o := req.Options
	if o["camera"] != true || o["camera_device"] != "/dev/video4" || o["camera_position"] != "top-right" || o["camera_size"] != "small" || o["camera_mirror"] != true {
		t.Errorf("camera options wrong: %+v", o)
	}
}

func TestMicDeviceSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	_ = os.WriteFile(path, []byte(`{"microphone":true,"mic_device":"alsa_input.x"}`), 0o600)
	if got := LoadSettings(path); got.MicDevice != "alsa_input.x" {
		t.Errorf("valid device lost: %+v", got)
	}
	_ = os.WriteFile(path, []byte(`{"microphone":true,"mic_device":"a|b"}`), 0o600)
	if got := LoadSettings(path); got.MicDevice != "" {
		t.Errorf("unsafe device kept: %+v", got)
	}

	s := DefaultSettings()
	s.Mic, s.MicDevice = true, "alsa_input.x"
	req, err := BuildRequest(s, Selection{Monitor: "DP-1"})
	if err != nil || req.Options["mic_device"] != "alsa_input.x" {
		t.Errorf("mic_device missing: %v, %v", req.Options, err)
	}
	s.Mic = false
	req, _ = BuildRequest(s, Selection{Monitor: "DP-1"})
	if _, ok := req.Options["mic_device"]; ok {
		t.Errorf("mic_device sent with the microphone off: %v", req.Options)
	}
}

func TestMicLabels(t *testing.T) {
	got := micLabels([]external.AudioDevice{{ID: "a", Name: "Webcam"}, {ID: "b", Name: "Desk"}, {ID: "c", Name: "Webcam"}})
	want := []string{"Webcam (1)", "Desk", "Webcam (2)"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("labels = %v, want %v", got, want)
			break
		}
	}
}
