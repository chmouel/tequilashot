package commands

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/notify"
	"github.com/chmouel/tequilashot/internal/state"
	"github.com/chmouel/tequilashot/internal/sway"
)

// VirtualCameraOptions describes a virtual camera request. The source is, in
// order of preference, Geometry, Output, a slurp Selection, the
// CurrentWindow, or else the focused screen.
type VirtualCameraOptions struct {
	Output        string
	Geometry      string
	Selection     bool
	CurrentWindow bool
	Backend       string
	// UseCamera puts the round webcam on the source; Camera.Device may be
	// empty to pick the first camera.
	UseCamera bool
	Camera    external.CameraOverlay
}

// liveCamera is a running virtual camera capture.
type liveCamera interface {
	Stop()
	Done() <-chan struct{}
	Stopped() bool
	Failure() string
}

var (
	resolveVirtualBackend = external.ResolveVirtualCameraBackend
	startVirtualCamera    = func(spec external.VirtualCameraSpec) (liveCamera, error) {
		return external.StartVirtualCamera(spec)
	}
	selectOutput          = sway.SelectOutput
	focusedWindowGeometry = sway.GetFocusedWindowGeometry
	slurpRegion           = external.Slurp
	sendNotification      = notify.Send
)

// VirtualCameraHandler runs the virtual camera that apps such as Google Meet
// can pick up.
type VirtualCameraHandler struct {
	cfg   *config.Config
	state *state.State

	mu       sync.Mutex
	starting bool
	current  *virtualSession
}

type virtualSession struct {
	cam    liveCamera
	bubble stopper
	once   sync.Once
}

// NewVirtualCameraHandler creates a virtual camera handler.
func NewVirtualCameraHandler(cfg *config.Config, st *state.State) *VirtualCameraHandler {
	return &VirtualCameraHandler{cfg: cfg, state: st}
}

// Start publishes the chosen source, with the round webcam on top when asked,
// as a virtual camera.
func (h *VirtualCameraHandler) Start(ctx context.Context, opts VirtualCameraOptions) error {
	h.mu.Lock()
	if h.starting || h.current != nil {
		h.mu.Unlock()
		return errors.New("the virtual camera is already running")
	}
	h.starting = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.starting = false
		h.mu.Unlock()
	}()

	backend, device, err := resolveVirtualBackend(opts.Backend)
	if err != nil {
		return err
	}
	if opts.UseCamera {
		opts.Camera.Shape = external.CameraCircle
		resolved, err := resolveCamera(ctx, RecordOptions{UseCamera: true, Camera: opts.Camera})
		if err != nil {
			return err
		}
		opts.Camera = resolved.Camera
	}
	geometry, output, err := virtualSource(ctx, opts)
	if err != nil {
		return err
	}

	bubble, err := showBubble(ctx, geometry, output, opts.Camera)
	if err != nil {
		return err
	}
	cam, err := startVirtualCamera(external.VirtualCameraSpec{Geometry: geometry, Output: output, Backend: backend, Device: device})
	if err != nil {
		if bubble != nil {
			bubble.Stop()
		}
		return err
	}

	sess := &virtualSession{cam: cam, bubble: bubble}
	h.mu.Lock()
	h.current = sess
	h.state.SetVirtualCamera(true)
	h.mu.Unlock()
	go h.watch(sess)

	where := device
	if backend == external.VirtualCameraPipeWire {
		where = "PipeWire"
	}
	_ = sendNotification(3000, h.cfg.RecordingStartIcon, "Virtual camera live", where)
	return nil
}

// virtualSource resolves the requested source to a geometry or an output.
func virtualSource(ctx context.Context, opts VirtualCameraOptions) (geometry, output string, err error) {
	switch {
	case opts.Geometry != "":
		var x, y, w, h int
		if _, err := fmt.Sscanf(opts.Geometry, "%d,%d %dx%d", &x, &y, &w, &h); err != nil || w <= 0 || h <= 0 {
			return "", "", fmt.Errorf("invalid geometry %q, expected \"X,Y WxH\"", opts.Geometry)
		}
		return opts.Geometry, "", nil
	case opts.Output != "":
		return "", opts.Output, nil
	case opts.Selection:
		geom, err := slurpRegion(ctx, "")
		if err != nil || geom == "" {
			return "", "", fmt.Errorf("selection cancelled or failed: %w", err)
		}
		return geom, "", nil
	case opts.CurrentWindow:
		geom, err := focusedWindowGeometry(ctx)
		if err != nil {
			return "", "", fmt.Errorf("failed to get window geometry: %w", err)
		}
		return geom, "", nil
	}
	out, err := selectOutput(ctx, true)
	if err != nil || out == "" {
		return "", "", fmt.Errorf("failed to find the focused screen: %w", err)
	}
	return "", out, nil
}

// watch tidies up once the capture ends and reports a capture that failed.
func (h *VirtualCameraHandler) watch(sess *virtualSession) {
	<-sess.cam.Done()
	h.finish(sess)
	if !sess.cam.Stopped() {
		_ = sendNotification(5000, h.cfg.RecordingStopIcon, "Virtual camera stopped", sess.cam.Failure())
	}
}

// finish removes the bubble and clears the state, once per session.
func (h *VirtualCameraHandler) finish(sess *virtualSession) {
	sess.once.Do(func() {
		if sess.bubble != nil {
			sess.bubble.Stop()
		}
		h.mu.Lock()
		if h.current == sess {
			h.current = nil
			h.state.SetVirtualCamera(false)
		}
		h.mu.Unlock()
	})
}

// Stop ends the virtual camera; the state is clear when it returns.
func (h *VirtualCameraHandler) Stop(context.Context) error {
	h.mu.Lock()
	sess := h.current
	h.mu.Unlock()
	if sess == nil {
		return errors.New("the virtual camera is not running")
	}
	sess.cam.Stop()
	h.finish(sess)
	_ = sendNotification(2000, h.cfg.RecordingStopIcon, "Virtual camera stopped", "")
	return nil
}

// Toggle stops a running virtual camera, or starts one.
func (h *VirtualCameraHandler) Toggle(ctx context.Context, opts VirtualCameraOptions) error {
	h.mu.Lock()
	running := h.current != nil
	h.mu.Unlock()
	if running {
		return h.Stop(ctx)
	}
	return h.Start(ctx, opts)
}

// Shutdown stops the virtual camera, if any, without a notification.
func (h *VirtualCameraHandler) Shutdown() {
	h.mu.Lock()
	sess := h.current
	h.mu.Unlock()
	if sess != nil {
		sess.cam.Stop()
		h.finish(sess)
	}
}
