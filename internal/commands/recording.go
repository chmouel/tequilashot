package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"sway-easyshot/internal/config"
	"sway-easyshot/internal/external"
	"sway-easyshot/internal/notify"
	"sway-easyshot/internal/state"
	"sway-easyshot/internal/sway"
)

// RecordingHandler provides methods for video recording operations.
type RecordingHandler struct {
	cfg   *config.Config
	state *state.State

	mu      sync.Mutex
	current *recordSession
}

// recordSession is one recorder run; stopping and the cache file belong to it.
type recordSession struct {
	base     string
	pid      int
	stopping atomic.Bool
}

// ownsCache reports whether sess is still the latest session; callers hold h.mu.
func (h *RecordingHandler) ownsCache(sess *recordSession) bool {
	return h.current == sess
}

// releaseSession clears the recording state and the cache file, but only
// while sess is still the latest session.
func (h *RecordingHandler) releaseSession(sess *recordSession, clearState bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.ownsCache(sess) {
		return
	}
	if clearState {
		h.state.SetRecording(false, "", 0)
	}
	_ = os.Remove(h.cfg.CacheFile)
}

// NewRecordingHandler creates a new recording handler instance.
func NewRecordingHandler(cfg *config.Config, st *state.State) *RecordingHandler {
	return &RecordingHandler{
		cfg:   cfg,
		state: st,
	}
}

// RecordOptions carries the optional extras of a recording request.
type RecordOptions struct {
	Audio external.AudioSources
	// UseCamera requests the webcam overlay; Camera.Device may be empty to
	// pick the first available camera.
	UseCamera bool
	Camera    external.CameraOverlay
}

// resolveCamera validates the camera request and picks the device and capture
// mode, so that a missing camera fails before any countdown or selection.
func resolveCamera(ctx context.Context, opts RecordOptions) (RecordOptions, error) {
	if !opts.UseCamera {
		opts.Camera = external.CameraOverlay{}
		return opts, nil
	}

	modes, err := external.ListCameraModes(ctx)
	if err != nil {
		return opts, err
	}
	devices := external.CameraDevices(modes)

	cam := opts.Camera
	if cam.Device == "" {
		if len(devices) == 0 {
			return opts, fmt.Errorf("no camera is available")
		}
		cam.Device = devices[0]
	}
	if err := cam.Validate(); err != nil {
		return opts, err
	}
	if !slices.Contains(devices, cam.Device) {
		return opts, fmt.Errorf("camera %s is not available", cam.Device)
	}

	cam.Width, cam.Height, cam.FPS = external.BestCameraMode(modes, cam.Device)
	opts.Camera = cam
	return opts, nil
}

// resolveMic checks that a requested microphone exists, so that a missing
// device fails before any countdown or selection.
func resolveMic(ctx context.Context, opts RecordOptions) (RecordOptions, error) {
	if !opts.Audio.Mic {
		opts.Audio.MicDevice = ""
		return opts, nil
	}
	id := opts.Audio.MicDevice
	if id == "" {
		return opts, nil
	}
	if err := external.ValidateMicDevice(id); err != nil {
		return opts, err
	}
	devices, err := external.ListMicrophones(ctx)
	if err != nil {
		return opts, err
	}
	for _, d := range devices {
		if d.ID == id {
			return opts, nil
		}
	}
	return opts, fmt.Errorf("Microphone %s is not available", id)
}

// resolveOptions validates the camera and microphone requests.
func resolveOptions(ctx context.Context, opts RecordOptions) (RecordOptions, error) {
	opts, err := resolveMic(ctx, opts)
	if err != nil {
		return opts, err
	}
	return resolveCamera(ctx, opts)
}

type quietFinishKey struct{}

// WithQuietFinish marks a start request whose client presents the result itself.
func WithQuietFinish(ctx context.Context) context.Context {
	return context.WithValue(ctx, quietFinishKey{}, true)
}

// MovieSelection records a video of a selected region.
func (h *RecordingHandler) MovieSelection(ctx context.Context, delay int, opts RecordOptions) error {
	opts, err := resolveOptions(ctx, opts)
	if err != nil {
		return err
	}

	if err := notify.CaptureDelay(delay, "movie selection", h.cfg.RecordingStartIcon); err != nil {
		return err
	}

	geom, err := external.Slurp(ctx, "")
	if err != nil || geom == "" {
		return fmt.Errorf("selection cancelled or failed: %w", err)
	}

	sleepWithCountdown(h.state, delay)

	return h.startRecording(ctx, geom, "", opts)
}

// MovieScreen records a video of the screen (or current screen if useCurrentScreen is true).
func (h *RecordingHandler) MovieScreen(ctx context.Context, delay int, useCurrentScreen bool, cropTop int, opts RecordOptions) error {
	opts, err := resolveOptions(ctx, opts)
	if err != nil {
		return err
	}

	output, err := sway.SelectOutput(ctx, useCurrentScreen)
	if err != nil || output == "" {
		return fmt.Errorf("failed to select output: %w", err)
	}

	return h.MovieOutput(ctx, delay, output, cropTop, opts)
}

// MovieOutput records a video of the named output, optionally skipping cropTop pixels at its top.
func (h *RecordingHandler) MovieOutput(ctx context.Context, delay int, output string, cropTop int, opts RecordOptions) error {
	opts, err := resolveOptions(ctx, opts)
	if err != nil {
		return err
	}

	if err := notify.CaptureDelay(delay, "movie screen", h.cfg.RecordingStartIcon); err != nil {
		return err
	}

	sleepWithCountdown(h.state, delay)

	if cropTop > 0 {
		geom, err := sway.GetOutputGeometry(ctx, output)
		if err != nil {
			return fmt.Errorf("failed to get output geometry: %w", err)
		}
		var x, y, w, ht int
		if _, err := fmt.Sscanf(geom, "%d,%d %dx%d", &x, &y, &w, &ht); err != nil {
			return fmt.Errorf("failed to parse output geometry: %w", err)
		}
		croppedGeom := fmt.Sprintf("%d,%d %dx%d", x, y+cropTop, w, ht-cropTop)
		return h.startRecording(ctx, croppedGeom, "", opts)
	}

	return h.startRecording(ctx, "", output, opts)
}

// MovieGeometry records a video of an explicit "X,Y WxH" area.
func (h *RecordingHandler) MovieGeometry(ctx context.Context, delay int, geometry string, opts RecordOptions) error {
	var x, y, w, ht int
	if _, err := fmt.Sscanf(geometry, "%d,%d %dx%d", &x, &y, &w, &ht); err != nil || w <= 0 || ht <= 0 {
		return fmt.Errorf("invalid geometry %q, expected \"X,Y WxH\"", geometry)
	}

	opts, err := resolveOptions(ctx, opts)
	if err != nil {
		return err
	}

	if err := notify.CaptureDelay(delay, "movie geometry", h.cfg.RecordingStartIcon); err != nil {
		return err
	}

	sleepWithCountdown(h.state, delay)

	return h.startRecording(ctx, geometry, "", opts)
}

// MovieCurrentWindow records a video of the currently focused window.
func (h *RecordingHandler) MovieCurrentWindow(ctx context.Context, delay int, opts RecordOptions) error {
	opts, err := resolveOptions(ctx, opts)
	if err != nil {
		return err
	}

	if err := notify.CaptureDelay(delay, "movie current window", h.cfg.RecordingStartIcon); err != nil {
		return err
	}

	geom, err := sway.GetFocusedWindowGeometry(ctx)
	if err != nil {
		return fmt.Errorf("failed to get window geometry: %w", err)
	}

	sleepWithCountdown(h.state, delay)

	return h.startRecording(ctx, geom, "", opts)
}

// earlyExitWindow is how long a new recorder must stay alive to count as started.
const earlyExitWindow = 1500 * time.Millisecond

func (h *RecordingHandler) startRecording(ctx context.Context, geometry, output string, opts RecordOptions) error {
	h.mu.Lock()
	base := h.cfg.GenerateRecordingBase()
	if _, err := os.Stat(base + ".mp4"); err == nil {
		base = fmt.Sprintf("%s-%d", base, os.Getpid())
	}
	file := base + ".mp4"

	if err := os.WriteFile(h.cfg.CacheFile, []byte(base), 0o600); err != nil {
		h.mu.Unlock()
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	sess := &recordSession{base: base}
	prev := h.current
	h.current = sess
	stderr := &tailBuffer{max: 4096}

	cmd, err := external.StartGpuScreenRecorder(ctx, geometry, output, file, opts.Audio, opts.Camera, stderr)
	if err != nil {
		_ = os.Remove(h.cfg.CacheFile)
		h.current = prev
		h.mu.Unlock()
		return fmt.Errorf("failed to start recording: %w", err)
	}
	sess.pid = cmd.Process.Pid

	// Publish the quiet flag before the recording becomes visible so a
	// concurrent stop never sees the recording without its flag.
	quiet, _ := ctx.Value(quietFinishKey{}).(bool)
	h.state.SetQuietFinish(quiet)
	h.state.SetRecording(true, file, sess.pid)
	h.mu.Unlock()

	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		h.mu.Lock()
		if h.ownsCache(sess) {
			h.state.SetRecording(false, "", 0)
		}
		h.mu.Unlock()
		exited <- err
	}()

	timer := time.NewTimer(earlyExitWindow)
	defer timer.Stop()

	select {
	case err := <-exited:
		if sess.stopping.Load() {
			return nil
		}
		return h.earlyFailure(sess, file, err, stderr)
	case <-timer.C:
	case <-ctx.Done():
		<-exited
		if !sess.stopping.Load() {
			h.releaseSession(sess, false)
			if info, statErr := os.Stat(file); statErr == nil && info.Size() == 0 {
				_ = os.Remove(file)
			}
		}
		return ctx.Err()
	}

	return nil
}

func (h *RecordingHandler) earlyFailure(sess *recordSession, file string, err error, stderr *tailBuffer) error {
	h.releaseSession(sess, false)
	kept := ""
	if info, statErr := os.Stat(file); statErr != nil || info.Size() == 0 {
		_ = os.Remove(file)
	} else {
		kept = fmt.Sprintf(" (partial file kept: %s)", file)
	}
	if err == nil {
		err = fmt.Errorf("exited unexpectedly")
	}
	if msg := stderr.String(); msg != "" {
		return fmt.Errorf("recorder stopped at once: %w: %s%s", err, msg, kept)
	}
	return fmt.Errorf("recorder stopped at once: %w%s", err, kept)
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if len(t.data) > t.max {
		t.data = t.data[len(t.data)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.data))
}

// StopRecording stops the current recording and waits for the MP4 to be finalised.
func (h *RecordingHandler) StopRecording(ctx context.Context) error {
	pid := h.state.GetRecordingPID()
	if pid == 0 {
		// The daemon may have restarted since the recording began.
		out, err := exec.CommandContext(ctx, "pgrep", "-n", "-f", "^gpu-screen-recorder ").Output() //nolint:gosec
		if err != nil {
			return fmt.Errorf("no recording in progress")
		}
		if _, err := fmt.Sscanf(string(out), "%d", &pid); err != nil {
			return fmt.Errorf("failed to parse recorder PID: %w", err)
		}
	}

	// Read before signalling: the monitor goroutine clears it once the recorder exits.
	quiet := h.state.QuietFinish()

	h.mu.Lock()
	var sess *recordSession
	if h.current != nil && h.current.pid == pid {
		sess = h.current
		sess.stopping.Store(true)
	}
	h.mu.Unlock()

	if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
		if sess != nil {
			sess.stopping.Store(false)
		}
		return fmt.Errorf("failed to stop recording: %w", err)
	}
	_ = notify.Send(2000, h.cfg.ScreenshotIcon, "Recording finished, saving")

	if err := waitForExit(pid, 30*time.Second); err != nil {
		return err
	}

	var mp4File string
	if sess != nil {
		mp4File = sess.base + ".mp4"
		h.releaseSession(sess, true)
	} else {
		data, err := os.ReadFile(h.cfg.CacheFile)
		if err != nil {
			return fmt.Errorf("failed to read cache file: %w", err)
		}
		mp4File = string(data) + ".mp4"
		_ = os.Remove(h.cfg.CacheFile)
	}

	if _, err := os.Stat(mp4File); err != nil {
		_ = notify.Send(5000, h.cfg.ScreenshotIcon, fmt.Sprintf("Could not find %s", mp4File))
		return fmt.Errorf("recording file not found: %s", mp4File)
	}

	if sess == nil {
		h.state.SetRecording(false, "", 0)
	}

	if quiet {
		_ = notify.Send(2000, h.cfg.RecordingStopIcon, "Recording saved")
		return nil
	}

	actions := map[string]string{
		"copypath": "Copy path",
		"show":     "Show",
		"rename":   "Rename",
		"delete":   "Delete",
	}

	action, err := notify.SendWithActions(30000, h.cfg.RecordingStopIcon, filepath.Base(mp4File), actions)
	if err != nil {
		_ = notify.Send(5000, h.cfg.RecordingStopIcon, fmt.Sprintf("%s is available", filepath.Base(mp4File)))
		return nil
	}

	action = strings.TrimSpace(action)

	switch action {
	case "copypath":
		return external.WlCopyText(ctx, mp4File)

	case "show":
		return external.Nautilus(ctx, "file://"+mp4File)

	case "rename":
		newname, err := external.Zenity(ctx, "Rename file", filepath.Base(mp4File))
		if err != nil || newname == "" {
			return nil
		}
		if !strings.HasSuffix(newname, ".mp4") {
			newname += ".mp4"
		}
		return os.Rename(mp4File, filepath.Join(filepath.Dir(mp4File), newname))

	case "delete":
		return os.Remove(mp4File)
	}

	return nil
}

// PauseRecording pauses or resumes the current recording.
func (h *RecordingHandler) PauseRecording(ctx context.Context) error {
	pid := h.state.GetRecordingPID()
	if pid == 0 {
		return fmt.Errorf("no recording in progress")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find recording process: %w", err)
	}

	if err := process.Signal(syscall.SIGUSR2); err != nil {
		return fmt.Errorf("failed to pause recording: %w", err)
	}

	// Toggle paused state
	currentState := h.state.GetState()
	newPausedState := !currentState.Paused
	h.state.SetPaused(newPausedState)

	if newPausedState {
		_ = notify.Send(2000, h.cfg.RecordingPauseIcon, "Recording paused")
	} else {
		_ = notify.Send(2000, h.cfg.RecordingStartIcon, "Recording resumed")
	}

	return nil
}

// ToggleRecord toggles recording state: starts if not recording, stops if recording.
func (h *RecordingHandler) ToggleRecord(ctx context.Context, startAction string, delay int, useCurrentScreen bool, cropTop int, opts RecordOptions) error {
	// Check current state
	currentState := h.state.GetState()

	if currentState.Recording {
		// Currently recording, stop it
		return h.StopRecording(ctx)
	}

	// Not recording, validate and start with specified action
	switch startAction {
	case "movie-selection":
		return h.MovieSelection(ctx, delay, opts)

	case "movie-screen":
		return h.MovieScreen(ctx, delay, useCurrentScreen, cropTop, opts)

	case "movie-current-window":
		return h.MovieCurrentWindow(ctx, delay, opts)

	default:
		return fmt.Errorf("invalid start action: %s (valid: movie-selection, movie-screen, movie-current-window)", startAction)
	}
}

// waitForExit polls until pid is gone, giving the recorder time to finalise the file.
func waitForExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("recorder (pid %d) did not exit within %s", pid, timeout)
}
