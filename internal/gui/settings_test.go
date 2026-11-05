package gui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "record-gui.json")
	want := Settings{Source: SourceWindow, Monitor: "DP-1", CropTop: 40, Desktop: false, Mic: true, Countdown: 5}
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
