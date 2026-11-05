package daemon

import "testing"

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
		{"camera": true, "camera_mirror": "true"},
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
		"camera_size": "large", "camera_mirror": true,
	})
	if !got.enabled || got.overlay.Device != "/dev/video2" || got.overlay.Position != "top-left" || got.overlay.Size != "large" || !got.overlay.Mirror {
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
