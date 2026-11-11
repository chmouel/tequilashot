package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/pkg/protocol"
)

func TestScreenshotResponse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "grim"), []byte("#!/bin/sh\nfor last; do :; done\nprintf png > \"$last\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	d := New(&config.Config{SaveLocation: dir}, false)
	defer d.cancel()
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	go d.handleConnection(server)
	if err := json.NewEncoder(client).Encode(protocol.Request{
		Command: "execute", Action: "screenshot", Options: map[string]interface{}{"output": "DP-1", "delay": 0},
	}); err != nil {
		t.Fatal(err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.File == "" || resp.State == nil || resp.State.Recording || resp.State.RecordingFile != "" {
		t.Fatalf("unexpected screenshot response: %+v", resp)
	}
	if data, err := os.ReadFile(resp.File); err != nil || string(data) != "png" {
		t.Fatalf("reported screenshot was not saved: %q, %v", data, err)
	}
}

func mustParseCamera(t *testing.T, options map[string]any) cameraRequest {
	t.Helper()
	got, err := parseCameraRequest(options)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestParseCameraRequestRejectsWrongTypes(t *testing.T) {
	for _, options := range []map[string]any{
		{"camera": "yes"},
		{"camera": true, "camera_device": 123.0},
		{"camera": true, "camera_position": true},
		{"camera": true, "camera_size": 1.0},
		{"camera": true, "camera_shape": false},
		{"camera": true, "camera_mirror": "true"},
		{"camera": true, "camera_matting_model": 1.0},
		{"camera": true, "camera_matting_model": true},
		{"camera": false, "camera_device": 123.0},
	} {
		if _, err := parseCameraRequest(options); err == nil {
			t.Errorf("expected an error for %v", options)
		}
	}
}

func TestParseCameraRequest(t *testing.T) {
	if got := mustParseCamera(t, map[string]any{"camera_device": "/dev/video0"}); got.enabled || got.overlay.Device != "" {
		t.Errorf("camera options without camera=true must be ignored: %+v", got)
	}
	if got := mustParseCamera(t, nil); got.enabled {
		t.Errorf("nil options enabled the camera")
	}
	got := mustParseCamera(t, map[string]any{
		"camera": true, "camera_device": "/dev/video2", "camera_position": "top-left",
		"camera_size": "large", "camera_shape": "circle", "camera_mirror": true, "camera_remove_background": true, "camera_no_ring": true,
		"camera_matting_model": "u2net",
	})
	if !got.enabled || got.overlay.Device != "/dev/video2" || got.overlay.Position != "top-left" || got.overlay.Size != "large" || got.overlay.Shape != "circle" || !got.overlay.Mirror || !got.overlay.RemoveBackground || !got.overlay.NoRing || got.overlay.MattingModel != "u2net" {
		t.Errorf("unexpected request: %+v", got)
	}
}

func TestParseMicDevice(t *testing.T) {
	for _, options := range []map[string]any{{"mic_device": 1.0}, {"mic_device": true}, {"mic_device": nil}} {
		if _, err := parseMicDevice(options); err == nil {
			t.Errorf("expected an error for %v", options)
		}
	}
	if got, err := parseMicDevice(map[string]any{"mic_device": "alsa_input.x"}); err != nil || got != "alsa_input.x" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := parseMicDevice(nil); err != nil || got != "" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestReplayRequests(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := New(&config.Config{SaveLocation: t.TempDir()}, false)
	defer d.cancel()
	for _, seconds := range []any{"60", true, nil, 60.5, 0.0, 86400.0} {
		resp := d.executeCommand(protocol.Request{Action: "replay-start", Options: map[string]interface{}{"seconds": seconds}})
		if resp.Success || resp.Message != "replay duration must be 30, 60 or 120 seconds" {
			t.Errorf("accepted invalid duration %v: %+v", seconds, resp)
		}
	}
	for _, action := range []string{"replay-save", "replay-stop"} {
		resp := d.executeCommand(protocol.Request{Action: action})
		if resp.Success || resp.File != "" || resp.State == nil || resp.State.Replay {
			t.Errorf("unexpected inactive replay response: %+v", resp)
		}
	}
	d.state.SetReplay(true, "DP-1", 60)
	resp := d.executeCommand(protocol.Request{Action: "waybar-status", Options: map[string]interface{}{
		"icons": map[string]interface{}{"Replay": "R"},
	}})
	var status protocol.WaybarStatus
	if err := json.Unmarshal([]byte(resp.Message), &status); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || !resp.State.Replay || status.Class != "replay" || status.Text != "R" {
		t.Fatalf("unexpected replay response: %+v %+v", resp, status)
	}
}

func TestCycleCameraWithoutBubble(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := New(&config.Config{}, false)
	defer d.cancel()
	resp := d.executeCommand(protocol.Request{Action: "camera-cycle"})
	if resp.Success || resp.Message != "the round camera is not running" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
