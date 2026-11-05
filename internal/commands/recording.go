package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
}

// NewRecordingHandler creates a new recording handler instance.
func NewRecordingHandler(cfg *config.Config, st *state.State) *RecordingHandler {
	return &RecordingHandler{
		cfg:   cfg,
		state: st,
	}
}

// MovieSelection records a video of a selected region.
func (h *RecordingHandler) MovieSelection(ctx context.Context, delay int, audio external.AudioSources) error {
	if err := notify.CaptureDelay(delay, "movie selection", h.cfg.RecordingStartIcon); err != nil {
		return err
	}

	geom, err := external.Slurp(ctx, "")
	if err != nil || geom == "" {
		return fmt.Errorf("selection cancelled or failed: %w", err)
	}

	sleepWithCountdown(h.state, delay)

	return h.startRecording(ctx, geom, "", audio)
}

// MovieScreen records a video of the screen (or current screen if useCurrentScreen is true).
func (h *RecordingHandler) MovieScreen(ctx context.Context, delay int, useCurrentScreen bool, cropTop int, audio external.AudioSources) error {
	output, err := sway.SelectOutput(ctx, useCurrentScreen)
	if err != nil || output == "" {
		return fmt.Errorf("failed to select output: %w", err)
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
		return h.startRecording(ctx, croppedGeom, "", audio)
	}

	return h.startRecording(ctx, "", output, audio)
}

// MovieCurrentWindow records a video of the currently focused window.
func (h *RecordingHandler) MovieCurrentWindow(ctx context.Context, delay int, audio external.AudioSources) error {
	if err := notify.CaptureDelay(delay, "movie current window", h.cfg.RecordingStartIcon); err != nil {
		return err
	}

	geom, err := sway.GetFocusedWindowGeometry(ctx)
	if err != nil {
		return fmt.Errorf("failed to get window geometry: %w", err)
	}

	sleepWithCountdown(h.state, delay)

	return h.startRecording(ctx, geom, "", audio)
}

func (h *RecordingHandler) startRecording(ctx context.Context, geometry, output string, audio external.AudioSources) error {
	base := h.cfg.GenerateRecordingBase()
	if _, err := os.Stat(base + ".mp4"); err == nil {
		base = fmt.Sprintf("%s-%d", base, os.Getpid())
	}
	file := base + ".mp4"

	if err := os.WriteFile(h.cfg.CacheFile, []byte(base), 0o600); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	cmd, err := external.StartGpuScreenRecorder(ctx, geometry, output, file, audio)
	if err != nil {
		return fmt.Errorf("failed to start recording: %w", err)
	}

	// Update state
	h.state.SetRecording(true, file, cmd.Process.Pid)

	// Monitor process in background
	go func() {
		_ = cmd.Wait()
		h.state.SetRecording(false, "", 0)
	}()

	return nil
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

	if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
		return fmt.Errorf("failed to stop recording: %w", err)
	}
	_ = notify.Send(2000, h.cfg.ScreenshotIcon, "Recording finished, saving")

	if err := waitForExit(pid, 30*time.Second); err != nil {
		return err
	}

	data, err := os.ReadFile(h.cfg.CacheFile)
	if err != nil {
		return fmt.Errorf("failed to read cache file: %w", err)
	}
	mp4File := string(data) + ".mp4"
	_ = os.Remove(h.cfg.CacheFile)

	if _, err := os.Stat(mp4File); err != nil {
		_ = notify.Send(5000, h.cfg.ScreenshotIcon, fmt.Sprintf("Could not find %s", mp4File))
		return fmt.Errorf("recording file not found: %s", mp4File)
	}

	// Update state
	h.state.SetRecording(false, "", 0)

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
func (h *RecordingHandler) ToggleRecord(ctx context.Context, startAction string, delay int, useCurrentScreen bool, cropTop int, audio external.AudioSources) error {
	// Check current state
	currentState := h.state.GetState()

	if currentState.Recording {
		// Currently recording, stop it
		return h.StopRecording(ctx)
	}

	// Not recording, validate and start with specified action
	switch startAction {
	case "movie-selection":
		return h.MovieSelection(ctx, delay, audio)

	case "movie-screen":
		return h.MovieScreen(ctx, delay, useCurrentScreen, cropTop, audio)

	case "movie-current-window":
		return h.MovieCurrentWindow(ctx, delay, audio)

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
