package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"sway-easyshot/internal/config"
	"sway-easyshot/internal/external"
	"sway-easyshot/internal/notify"
	"sway-easyshot/internal/state"
)

// OBSHandler provides methods to interact with OBS.
type OBSHandler struct {
	cfg   *config.Config
	state *state.State
}

// NewOBSHandler creates a new OBS handler instance.
func NewOBSHandler(cfg *config.Config, st *state.State) *OBSHandler {
	return &OBSHandler{
		cfg:   cfg,
		state: st,
	}
}

// ToggleRecording toggles OBS recording state (start/stop).
func (h *OBSHandler) ToggleRecording(ctx context.Context) error {
	client, err := external.NewOBSClient(ctx)
	if err != nil {
		_ = notify.Send(2000, h.cfg.ScreenshotIcon, "Failed to connect to OBS")
		return fmt.Errorf("failed to connect to OBS: %w", err)
	}
	defer client.Disconnect() //nolint:errcheck

	status, err := client.Record.GetRecordStatus()
	if err != nil {
		_ = notify.Send(2000, h.cfg.ScreenshotIcon, "Failed to get OBS status")
		return fmt.Errorf("failed to get OBS recording status: %w", err)
	}

	if !status.OutputActive {
		time.Sleep(1 * time.Second)
		if _, err := client.Record.StartRecord(); err != nil {
			return fmt.Errorf("failed to start OBS recording: %w", err)
		}
		h.state.SetOBSState(true, false)
		return nil
	}

	resp, err := client.Record.StopRecord()
	if err != nil {
		return fmt.Errorf("failed to stop OBS recording: %w", err)
	}
	h.state.SetOBSState(false, false)

	if resp.OutputPath != "" {
		go h.remuxToMP4(ctx, resp.OutputPath)
	} else {
		_ = notify.Send(2000, h.cfg.RecordingStopIcon, "Recording has stopped")
	}
	return nil
}

// TogglePause toggles OBS pause state (paused/resumed).
func (h *OBSHandler) TogglePause(ctx context.Context) error {
	client, err := external.NewOBSClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to OBS: %w", err)
	}
	defer client.Disconnect() //nolint:errcheck

	status, err := client.Record.GetRecordStatus()
	if err != nil {
		return fmt.Errorf("failed to get OBS recording status: %w", err)
	}
	log.Printf("OBS status: active=%v paused=%v", status.OutputActive, status.OutputPaused)

	// OBS accepts PauseRecord on an inactive output without reporting an error,
	// so trusting it would leave the indicator stuck on paused forever.
	if !status.OutputActive {
		h.state.SetOBSState(false, false)
		_ = notify.Send(2000, h.cfg.ScreenshotIcon, "OBS is not recording")
		return fmt.Errorf("OBS is not recording")
	}

	if status.OutputPaused {
		if _, err := client.Record.ResumeRecord(); err != nil {
			return fmt.Errorf("failed to resume OBS recording: %w", err)
		}
		log.Printf("OBS: resumed recording")
		_ = notify.Send(2000, h.cfg.RecordingStartIcon, "Recording resumed")
		h.state.SetOBSState(true, false)
	} else {
		if _, err := client.Record.PauseRecord(); err != nil {
			return fmt.Errorf("failed to pause OBS recording: %w", err)
		}
		log.Printf("OBS: paused recording")
		_ = notify.Send(2000, h.cfg.RecordingPauseIcon, "Recording paused")
		h.state.SetOBSState(true, true)
	}

	return nil
}

func (h *OBSHandler) remuxToMP4(ctx context.Context, mkvPath string) {
	if !strings.HasSuffix(mkvPath, ".mkv") {
		_ = notify.Send(2000, h.cfg.RecordingStopIcon, "Recording has stopped")
		return
	}

	mp4Path := strings.TrimSuffix(mkvPath, ".mkv") + ".mp4"
	_ = notify.Send(2000, h.cfg.RecordingStopIcon, "Converting recording to MP4…")

	// Wait for OBS to finish writing the file before remuxing.
	if err := waitForFile(mkvPath, 10*time.Second); err != nil {
		log.Printf("OBS: timed out waiting for MKV to be written: %v", err)
		_ = notify.Send(2000, h.cfg.ScreenshotIcon, "Failed to convert: recording file not ready")
		return
	}

	if err := external.FfmpegRemux(ctx, mkvPath, mp4Path); err != nil {
		log.Printf("OBS remux failed: %v", err)
		_ = notify.Send(2000, h.cfg.ScreenshotIcon, "Failed to convert recording to MP4")
		return
	}
	if err := os.Remove(mkvPath); err != nil {
		log.Printf("OBS: failed to delete MKV after remux: %v", err)
	}
	_ = notify.Send(3000, h.cfg.RecordingStopIcon, fmt.Sprintf("Recording saved: %s", mp4Path))
}

// waitForFile polls until the file size stops growing, meaning the writer has finished.
func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var prevSize int64 = -1
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() > 0 && info.Size() == prevSize {
			return nil
		}
		prevSize = info.Size()
	}
	return fmt.Errorf("timeout waiting for %s", path)
}
