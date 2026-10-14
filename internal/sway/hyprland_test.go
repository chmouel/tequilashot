package sway

import (
	"testing"
)

const hyprActiveWindowFixture = `{
  "address": "0x559a69e8dc80",
  "at": [7, 2198],
  "size": [1906, 1035],
  "workspace": {"id": 11, "name": "11"},
  "monitor": 0,
  "class": "kitty"
}`

const hyprMonitorsFixture = `[
  {"id": 0, "name": "DP-1", "make": "Dell Inc.", "model": "DELL U2722QC",
   "x": 0, "y": 2160, "width": 1920, "height": 1080, "focused": true},
  {"id": 1, "name": "DP-2", "make": "Dell Inc.", "model": "DELL S2722QC",
   "x": 0, "y": 0, "width": 3840, "height": 2160, "focused": false}
]`

func TestParseHyprActiveWindow(t *testing.T) {
	got, err := parseHyprActiveWindow([]byte(hyprActiveWindowFixture))
	if err != nil {
		t.Fatal(err)
	}
	if want := "7,2198 1906x1035"; got != want {
		t.Errorf("geometry = %q, want %q", got, want)
	}
}

func TestParseHyprMonitors(t *testing.T) {
	monitors, err := parseHyprMonitors([]byte(hyprMonitorsFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(monitors) != 2 {
		t.Fatalf("got %d monitors, want 2", len(monitors))
	}
	if monitors[0].Name != "DP-1" || !monitors[0].Focused {
		t.Errorf("monitors[0] = %+v, want focused DP-1", monitors[0])
	}
	if monitors[1].Name != "DP-2" || monitors[1].Focused {
		t.Errorf("monitors[1] = %+v, want unfocused DP-2", monitors[1])
	}
	if monitors[1].Width != 3840 || monitors[1].Height != 2160 {
		t.Errorf("monitors[1] size = %dx%d, want 3840x2160", monitors[1].Width, monitors[1].Height)
	}
}

func TestIsHyprland(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	if !isHyprland() {
		t.Error("isHyprland() = false, want true with HYPRLAND_INSTANCE_SIGNATURE set")
	}
}
