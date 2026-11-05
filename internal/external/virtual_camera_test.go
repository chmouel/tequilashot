package external

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const loopbackDev = "/dev/video10"

func TestIsLoopbackCapability(t *testing.T) {
	capability := func(driver string) []byte {
		b := make([]byte, 104)
		copy(b, driver)
		return b
	}
	if !isLoopbackCapability(capability("v4l2 loopback")) {
		t.Error("v4l2loopback driver not recognised")
	}
	if isLoopbackCapability(capability("uvcvideo")) {
		t.Error("uvcvideo taken for a loopback device")
	}
	if isLoopbackCapability([]byte("v4l2 loopback")) {
		t.Error("short buffer accepted")
	}
}

func TestWithoutDevices(t *testing.T) {
	modes := []CameraMode{{Device: "/dev/video0"}, {Device: loopbackDev}, {Device: "/dev/video0"}}
	got := CameraDevices(withoutDevices(modes, []string{loopbackDev}))
	if !slices.Equal(got, []string{"/dev/video0"}) {
		t.Errorf("devices = %v", got)
	}
}

func TestWfRecorderArgs(t *testing.T) {
	filter := "scale=1280:720:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=1280:720:(ow-iw)/2:(oh-ih)/2"
	common := []string{"-r", "30", "-F", filter, "-c", "rawvideo", "-x", "yuv420p"}

	got := wfRecorderArgs(VirtualCameraSpec{Output: "DP-1", Backend: VirtualCameraPipeWire})
	want := slices.Concat([]string{"-D", "-y", "-o", "DP-1"}, common, []string{"-m", "rawvideo", "-f", "/dev/stdout"})
	if !slices.Equal(got, want) {
		t.Errorf("pipewire args = %q", got)
	}

	got = wfRecorderArgs(VirtualCameraSpec{Geometry: "10,20 300x200", Backend: VirtualCameraV4L2, Device: loopbackDev})
	want = slices.Concat([]string{"-D", "-y", "-g", "10,20 300x200"}, common, []string{"-m", "v4l2", "-f", loopbackDev})
	if !slices.Equal(got, want) {
		t.Errorf("v4l2 args = %q", got)
	}
}

func TestResolveVirtualCameraBackend(t *testing.T) {
	origDevices, origLook := loopbackDevices, lookPath
	t.Cleanup(func() { loopbackDevices, lookPath = origDevices, origLook })

	var devices []string
	missing := ""
	loopbackDevices = func() []string { return devices }
	lookPath = func(name string) (string, error) {
		if name == missing {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}

	tests := []struct {
		name, backend, missing string
		devices                []string
		want, device, err      string
	}{
		{name: "auto prefers loopback", backend: "auto", devices: []string{loopbackDev}, want: VirtualCameraV4L2, device: loopbackDev},
		{name: "auto falls back to pipewire", backend: "", want: VirtualCameraPipeWire},
		{name: "pipewire ignores loopback", backend: "pipewire", devices: []string{loopbackDev}, want: VirtualCameraPipeWire},
		{name: "loopback missing", backend: "v4l2loopback", err: "no v4l2loopback device"},
		{name: "gst missing", backend: "auto", missing: "gst-launch-1.0", err: "gst-launch-1.0"},
		{name: "wf-recorder missing", backend: "auto", devices: []string{loopbackDev}, missing: "wf-recorder", err: "wf-recorder"},
		{name: "unknown", backend: "obs", err: "unknown virtual camera backend"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devices, missing = tt.devices, tt.missing
			got, device, err := ResolveVirtualCameraBackend(tt.backend)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want || device != tt.device {
				t.Errorf("got %q %q %v, want %q %q", got, device, err, tt.want, tt.device)
			}
		})
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\n\nb\rc\r\nd\n", 3); got != "b; c; d" {
		t.Errorf("lastLines = %q", got)
	}
}

// fakeTools puts shell scripts named after the given tools first on PATH.
func fakeTools(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil { //nolint:gosec // test helper script
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestStartVirtualCameraEarlyFailure(t *testing.T) {
	fakeTools(t, map[string]string{"wf-recorder": "echo 'Failed to find output DP-9' >&2\nexit 1\n"})
	_, err := StartVirtualCamera(VirtualCameraSpec{Output: "DP-9", Backend: VirtualCameraV4L2, Device: loopbackDev})
	if err == nil || !strings.Contains(err.Error(), "Failed to find output DP-9") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartVirtualCameraV4L2KeepsRunning(t *testing.T) {
	fakeTools(t, map[string]string{"wf-recorder": "trap 'exit 0' INT\nwhile :; do sleep 0.1; done\n"})
	v, err := StartVirtualCamera(VirtualCameraSpec{Output: "DP-1", Backend: VirtualCameraV4L2, Device: loopbackDev})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Stop()
	select {
	case <-v.Done():
		t.Fatalf("capture ended without a sink: %s", v.Failure())
	case <-time.After(virtualCameraStopLimit + time.Second):
	}
}

func TestStartVirtualCameraStop(t *testing.T) {
	fakeTools(t, map[string]string{
		"wf-recorder":    "trap 'exit 0' INT\nwhile :; do echo frame; sleep 0.1; done\n",
		"gst-launch-1.0": "cat >/dev/null\n",
	})
	v, err := StartVirtualCamera(VirtualCameraSpec{Output: "DP-1", Backend: VirtualCameraPipeWire})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	v.Stop()
	if !v.Stopped() {
		t.Error("Stopped() = false after Stop")
	}
	select {
	case <-v.Done():
	default:
		t.Fatal("Done not closed after Stop")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("stop took %s", elapsed)
	}
}
