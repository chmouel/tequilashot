package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/state"
)

type fakeLive struct {
	done    chan struct{}
	once    sync.Once
	stopped bool
}

func newFakeLive() *fakeLive { return &fakeLive{done: make(chan struct{})} }

func (f *fakeLive) Stop()                 { f.stopped = true; f.end() }
func (f *fakeLive) end()                  { f.once.Do(func() { close(f.done) }) }
func (f *fakeLive) Done() <-chan struct{} { return f.done }
func (f *fakeLive) Stopped() bool         { return f.stopped }
func (f *fakeLive) Failure() string       { return "output gone" }

// stubVirtualCamera replaces the processes and compositor queries used by the
// virtual camera, returning the specs it was started with and the notifications sent.
func stubVirtualCamera(t *testing.T, cam liveCamera, startErr error) (*[]external.VirtualCameraSpec, func() []string) {
	t.Helper()
	origBackend, origStart, origOutput, origWindow, origSlurp, origNotify := resolveVirtualBackend, startVirtualCamera, selectOutput, focusedWindowGeometry, slurpRegion, sendNotification
	origBubble, origGeometry := startBubble, outputGeometry
	t.Cleanup(func() {
		resolveVirtualBackend, startVirtualCamera, selectOutput, focusedWindowGeometry, slurpRegion, sendNotification = origBackend, origStart, origOutput, origWindow, origSlurp, origNotify
		startBubble, outputGeometry = origBubble, origGeometry
	})

	var specs []external.VirtualCameraSpec
	var notes []string
	var mu sync.Mutex
	resolveVirtualBackend = func(string) (string, string, error) { return external.VirtualCameraV4L2, "/dev/video10", nil }
	startVirtualCamera = func(spec external.VirtualCameraSpec) (liveCamera, error) {
		specs = append(specs, spec)
		return cam, startErr
	}
	selectOutput = func(context.Context, bool) (string, error) { return "DP-1", nil }
	focusedWindowGeometry = func(context.Context) (string, error) { return "5,6 700x500", nil }
	slurpRegion = func(context.Context, string) (string, error) { return "1,2 300x200", nil }
	outputGeometry = func(context.Context, string) (string, error) { return "0,0 1920x1080", nil }
	sendNotification = func(_ int, _, title, body string) error {
		mu.Lock()
		defer mu.Unlock()
		notes = append(notes, title+": "+body)
		return nil
	}
	return &specs, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(notes)
	}
}

func newVirtualHandler() (*VirtualCameraHandler, *state.State) {
	st := state.NewState()
	return NewVirtualCameraHandler(&config.Config{}, st), st
}

func TestVirtualCameraSource(t *testing.T) {
	stubVirtualCamera(t, newFakeLive(), nil)
	tests := []struct {
		name           string
		opts           VirtualCameraOptions
		geometry, out  string
		wantErrContain string
	}{
		{name: "focused screen", out: "DP-1"},
		{name: "named output", opts: VirtualCameraOptions{Output: "HDMI-A-1"}, out: "HDMI-A-1"},
		{name: "geometry", opts: VirtualCameraOptions{Geometry: "10,20 640x480"}, geometry: "10,20 640x480"},
		{name: "selection", opts: VirtualCameraOptions{Selection: true}, geometry: "1,2 300x200"},
		{name: "window", opts: VirtualCameraOptions{CurrentWindow: true}, geometry: "5,6 700x500"},
		{name: "bad geometry", opts: VirtualCameraOptions{Geometry: "0,0 0x0"}, wantErrContain: "invalid geometry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geometry, out, err := virtualSource(context.Background(), tt.opts)
			if tt.wantErrContain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || geometry != tt.geometry || out != tt.out {
				t.Errorf("got %q %q %v", geometry, out, err)
			}
		})
	}
}

func TestVirtualCameraStartStop(t *testing.T) {
	cam := newFakeLive()
	specs, _ := stubVirtualCamera(t, cam, nil)
	h, st := newVirtualHandler()

	if err := h.Start(context.Background(), VirtualCameraOptions{Output: "DP-2"}); err != nil {
		t.Fatal(err)
	}
	want := external.VirtualCameraSpec{Output: "DP-2", Backend: external.VirtualCameraV4L2, Device: "/dev/video10"}
	if len(*specs) != 1 || (*specs)[0] != want {
		t.Errorf("specs = %+v", *specs)
	}
	if !st.VirtualCamera() || !st.GetState().VirtualCamera {
		t.Error("state does not show the virtual camera")
	}
	if err := h.Start(context.Background(), VirtualCameraOptions{}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second start: %v", err)
	}
	if err := h.Toggle(context.Background(), VirtualCameraOptions{}); err != nil {
		t.Fatal(err)
	}
	if !cam.stopped || st.VirtualCamera() {
		t.Error("toggle did not stop the virtual camera")
	}
	if err := h.Stop(context.Background()); err == nil {
		t.Error("stopping twice succeeded")
	}
}

func TestVirtualCameraUnexpectedExit(t *testing.T) {
	cam := newFakeLive()
	_, notes := stubVirtualCamera(t, cam, nil)
	bubble := &fakeBubble{stopped: make(chan struct{})}
	startBubble = func(external.CameraOverlay, int, int, int) (stopper, error) { return bubble, nil }
	stubCameraList(t)
	h, st := newVirtualHandler()

	if err := h.Start(context.Background(), VirtualCameraOptions{UseCamera: true}); err != nil {
		t.Fatal(err)
	}
	cam.end()
	select {
	case <-bubble.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the round camera outlived the virtual camera")
	}
	deadline := time.Now().Add(5 * time.Second)
	for st.VirtualCamera() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st.VirtualCamera() {
		t.Fatal("state still shows the virtual camera")
	}
	for time.Now().Before(deadline) {
		if got := notes(); len(got) > 0 && strings.Contains(got[len(got)-1], "output gone") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("no failure notification, got %q", notes())
}

// stubCameraList puts a fake gpu-screen-recorder listing one camera first on PATH.
func stubCameraList(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = --list-v4l2-devices ] && echo '/dev/video2|1280x720@30hz|mjpeg'\n"
	if err := os.WriteFile(filepath.Join(dir, "gpu-screen-recorder"), []byte(script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestVirtualCameraFailedStartRemovesBubble(t *testing.T) {
	stubVirtualCamera(t, nil, errors.New("the virtual camera stopped at once: no output"))
	bubble := &fakeBubble{stopped: make(chan struct{})}
	startBubble = func(external.CameraOverlay, int, int, int) (stopper, error) { return bubble, nil }
	stubCameraList(t)
	h, st := newVirtualHandler()

	if err := h.Start(context.Background(), VirtualCameraOptions{UseCamera: true}); err == nil {
		t.Fatal("start succeeded")
	}
	select {
	case <-bubble.stopped:
	default:
		t.Error("the round camera was left on screen")
	}
	if st.VirtualCamera() {
		t.Error("state shows the virtual camera")
	}
	if h.current != nil || h.starting {
		t.Error("handler still holds the failed start")
	}
}

func TestRecordingRefusesCameraWhileLive(t *testing.T) {
	h, st := newTestHandler(t)
	st.SetVirtualCamera(true)
	err := h.MovieGeometry(context.Background(), 0, "0,0 100x100", RecordOptions{UseCamera: true})
	if err == nil || !strings.Contains(err.Error(), "virtual camera") {
		t.Fatalf("err = %v", err)
	}
}
