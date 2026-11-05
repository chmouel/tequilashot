package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"sway-easyshot/internal/config"
	"sway-easyshot/internal/external"
	"sway-easyshot/internal/state"
)

func TestMovieGeometryRejectsInvalid(t *testing.T) {
	h := NewRecordingHandler(nil, nil)
	for _, g := range []string{"", "nonsense", "0,0 0x0", "1,2 -3x4"} {
		if err := h.MovieGeometry(context.Background(), 0, g, RecordOptions{}); err == nil {
			t.Errorf("expected error for %q", g)
		}
	}
}

// stubRecorder puts a fake gpu-screen-recorder first on PATH.
func stubRecorder(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --list-capture-options ] && exit 0\n" +
		"while [ $# -gt 0 ]; do [ \"$1\" = -o ] && out=$2; shift; done\n" + body
	if err := os.WriteFile(filepath.Join(dir, "gpu-screen-recorder"), []byte(script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newTestHandler(t *testing.T) (*RecordingHandler, *state.State) {
	t.Helper()
	dir := t.TempDir()
	st := state.NewState()
	return NewRecordingHandler(&config.Config{SaveLocation: dir, CacheFile: filepath.Join(dir, "cache")}, st), st
}

func TestStartRecordingEarlyFailure(t *testing.T) {
	stubRecorder(t, "touch \"$out\"\necho 'camera busy' >&2\nexit 1\n")
	h, st := newTestHandler(t)

	err := h.startRecording(context.Background(), "", "DP-1", RecordOptions{})
	if err == nil || !strings.Contains(err.Error(), "camera busy") {
		t.Fatalf("expected a camera busy error, got %v", err)
	}
	if st.GetState().Recording {
		t.Error("state still shows a recording")
	}
	if _, statErr := os.Stat(h.cfg.CacheFile); !os.IsNotExist(statErr) {
		t.Error("cache file was left behind")
	}
	if files, _ := filepath.Glob(filepath.Join(h.cfg.SaveLocation, "*.mp4")); len(files) != 0 {
		t.Errorf("empty output left behind: %v", files)
	}
}

func TestStartRecordingSucceedsWhenRecorderStays(t *testing.T) {
	stubRecorder(t, "exec sleep 30\n")
	h, st := newTestHandler(t)

	if err := h.startRecording(context.Background(), "", "DP-1", RecordOptions{}); err != nil {
		t.Fatal(err)
	}
	pid := st.GetRecordingPID()
	if !st.GetState().Recording || pid == 0 {
		t.Fatal("recording not published")
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func TestResolveCameraDisabled(t *testing.T) {
	got, err := resolveCamera(context.Background(), RecordOptions{Camera: external.CameraOverlay{Device: "/dev/video0"}})
	if err != nil || got.Camera.Enabled() {
		t.Fatalf("camera must stay off without UseCamera: %+v %v", got, err)
	}
}

func TestResolveCamera(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --list-v4l2-devices ]; then\n" +
		"echo '/dev/video2|1920x1080@30hz|mjpeg'\necho '/dev/video2|1280x720@25hz|mjpeg'\nfi\n"
	_ = os.WriteFile(filepath.Join(dir, "gpu-screen-recorder"), []byte(script), 0o700) //nolint:gosec
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := resolveCamera(context.Background(), RecordOptions{UseCamera: true})
	if err != nil || got.Camera.Device != "/dev/video2" || got.Camera.Width != 1280 || got.Camera.FPS != 25 {
		t.Fatalf("default device: %+v %v", got.Camera, err)
	}
	if _, err = resolveCamera(context.Background(), RecordOptions{UseCamera: true, Camera: external.CameraOverlay{Device: "/dev/video9"}}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("missing device: %v", err)
	}
	if _, err = resolveCamera(context.Background(), RecordOptions{UseCamera: true, Camera: external.CameraOverlay{Device: "/dev/video2", Size: "huge"}}); err == nil {
		t.Fatal("invalid size accepted")
	}
}

func waitForRecordingPID(t *testing.T, st *state.State) int {
	t.Helper()
	for i := 0; i < 500; i++ {
		if pid := st.GetRecordingPID(); pid != 0 {
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("recording never started")
	return 0
}

func assertOwnsCache(t *testing.T, h *RecordingHandler, st *state.State, pid int) {
	t.Helper()
	if got := st.GetRecordingPID(); got != pid {
		t.Errorf("state pid = %d, want %d", got, pid)
	}
	if _, err := os.Stat(h.cfg.CacheFile); err != nil {
		t.Errorf("newer recording lost its cache file: %v", err)
	}
}

func TestStopDuringStartupThenRestartKeepsNewRecording(t *testing.T) {
	stubRecorder(t, "touch \"$out\"\nsleep 30 &\ntrap 'kill $!; exit 0' INT\nwait\n")
	h, st := newTestHandler(t)

	first := make(chan error, 1)
	go func() {
		first <- h.startRecording(WithQuietFinish(context.Background()), "", "DP-1", RecordOptions{})
	}()
	waitForRecordingPID(t, st)
	time.Sleep(200 * time.Millisecond)

	if err := h.StopRecording(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.startRecording(context.Background(), "", "DP-1", RecordOptions{}); err != nil {
		t.Fatal(err)
	}
	pid := st.GetRecordingPID()
	if err := <-first; err != nil {
		t.Fatalf("stopped recording reported %v", err)
	}
	assertOwnsCache(t, h, st, pid)
	_ = syscall.Kill(pid, syscall.SIGINT)
}

func TestEarlyFailureDoesNotDisturbNewerRecording(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "seen")
	stubRecorder(t, "touch \"$out\"\nif [ -e "+marker+" ]; then exec sleep 30; fi\ntouch "+marker+"\nsleep 0.6\nexit 1\n")
	h, st := newTestHandler(t)

	first := make(chan error, 1)
	go func() {
		first <- h.startRecording(context.Background(), "", "DP-1", RecordOptions{})
	}()
	waitForRecordingPID(t, st)

	if err := h.startRecording(context.Background(), "", "DP-1", RecordOptions{}); err != nil {
		t.Fatal(err)
	}
	pid := st.GetRecordingPID()
	if err := <-first; err == nil {
		t.Fatal("expected the first recorder to fail")
	}
	assertOwnsCache(t, h, st, pid)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func TestStartRecordingContextCancelled(t *testing.T) {
	stubRecorder(t, "exec sleep 30\n")
	h, st := newTestHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitForRecordingPID(t, st)
		cancel()
	}()

	if err := h.startRecording(ctx, "", "DP-1", RecordOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if st.GetState().Recording {
		t.Error("state still shows a recording")
	}
	if _, err := os.Stat(h.cfg.CacheFile); !os.IsNotExist(err) {
		t.Error("cache file was left behind")
	}
}

func TestResolveMic(t *testing.T) {
	stubRecorder(t, "exit 0\n")
	// The stub's listing is empty, so only the default input is available.
	ctx := context.Background()

	opts, err := resolveMic(ctx, RecordOptions{Audio: external.AudioSources{Mic: true}})
	if err != nil || opts.Audio.MicDevice != "" {
		t.Errorf("default mic: %+v, %v", opts, err)
	}
	opts, err = resolveMic(ctx, RecordOptions{Audio: external.AudioSources{MicDevice: "gone"}})
	if err != nil || opts.Audio.MicDevice != "" {
		t.Errorf("device without mic must be dropped: %+v, %v", opts, err)
	}
	if _, err = resolveMic(ctx, RecordOptions{Audio: external.AudioSources{Mic: true, MicDevice: "gone"}}); err == nil || !strings.Contains(err.Error(), "Microphone gone is not available") {
		t.Errorf("missing device: %v", err)
	}
	if _, err = resolveMic(ctx, RecordOptions{Audio: external.AudioSources{Mic: true, MicDevice: "a|b"}}); err == nil {
		t.Error("invalid device accepted")
	}
}

func TestResolveMicFindsListedDevice(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'default_input|Default input'\necho 'alsa_input.x|Desk microphone'\n"
	if err := os.WriteFile(filepath.Join(dir, "gpu-screen-recorder"), []byte(script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	opts, err := resolveMic(context.Background(), RecordOptions{Audio: external.AudioSources{Mic: true, MicDevice: "alsa_input.x"}})
	if err != nil || opts.Audio.MicDevice != "alsa_input.x" {
		t.Errorf("got %+v, %v", opts, err)
	}
}
