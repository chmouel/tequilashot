package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/gui"
	"github.com/chmouel/tequilashot/internal/notify"
	"github.com/chmouel/tequilashot/internal/state"
	"github.com/chmouel/tequilashot/internal/sway"
)

// ScreenshotHandler provides methods for screenshot operations.
type ScreenshotHandler struct {
	cfg   *config.Config
	state *state.State
}

// NewScreenshotHandler creates a new screenshot handler instance.
func NewScreenshotHandler(cfg *config.Config, st *state.State) *ScreenshotHandler {
	return &ScreenshotHandler{cfg: cfg, state: st}
}

var screenshotGeometry = regexp.MustCompile(`^-?\d+,-?\d+ [1-9]\d*x[1-9]\d*$`)

// Capture returns a saved PNG without offering notification actions
func (h *ScreenshotHandler) Capture(ctx context.Context, delay int, output, geometry string) (string, error) {
	if output != "" && geometry != "" {
		return "", fmt.Errorf("select either an output or a geometry")
	}
	if geometry != "" && !screenshotGeometry.MatchString(geometry) {
		return "", fmt.Errorf("invalid geometry %q, expected \"X,Y WxH\"", geometry)
	}
	if output == "" && geometry == "" {
		var err error
		geometry, err = external.Slurp(ctx, "")
		if err != nil {
			return "", fmt.Errorf("selection cancelled or failed: %w", err)
		}
		if geometry == "" {
			return "", fmt.Errorf("selection cancelled or failed")
		}
		if !screenshotGeometry.MatchString(geometry) {
			return "", fmt.Errorf("invalid selection geometry %q", geometry)
		}
	}
	if err := notify.CaptureDelay(delay, "screenshot", h.cfg.ScreenshotIcon); err != nil {
		return "", err
	}
	sleepWithCountdown(h.state, delay)

	dir := h.cfg.SaveDir()
	file := config.GenerateFilename(dir)
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec
	if os.IsExist(err) {
		f, err = os.CreateTemp(dir, strings.TrimSuffix(filepath.Base(file), ".png")+"-*.png")
	}
	if err != nil {
		return "", err
	}
	file = f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(file)
		return "", err
	}
	if _, err := external.Grim(ctx, geometry, output, file); err != nil {
		_ = os.Remove(file)
		return "", fmt.Errorf("failed to capture screenshot: %w", err)
	}
	return file, nil
}

// sleepWithCountdown sleeps for the given delay while updating the countdown state
func sleepWithCountdown(st *state.State, delay int) {
	if delay <= 0 {
		return
	}
	for i := delay; i > 0; i-- {
		st.SetCountdown(i)
		time.Sleep(time.Second)
	}
	st.ClearCountdown()
}

// CurrentWindowClipboard captures the focused window and copies it to clipboard.
func (h *ScreenshotHandler) CurrentWindowClipboard(ctx context.Context, delay int) error {
	if err := notify.CaptureDelay(delay, "window to clipboard", h.cfg.ScreenshotIcon); err != nil {
		return err
	}

	geom, err := sway.GetFocusedWindowGeometry(ctx)
	if err != nil {
		return fmt.Errorf("failed to get window geometry: %w", err)
	}

	sleepWithCountdown(h.state, delay)

	data, err := external.Grim(ctx, geom, "", "")
	if err != nil {
		return fmt.Errorf("failed to capture screenshot: %w", err)
	}

	return external.WlCopy(ctx, data, "image/png")
}

// CurrentWindowFile captures the focused window and saves it to a file.
func (h *ScreenshotHandler) CurrentWindowFile(ctx context.Context, delay int) error {
	if err := notify.CaptureDelay(delay, "window to file", h.cfg.ScreenshotIcon); err != nil {
		return err
	}

	geom, err := sway.GetFocusedWindowGeometry(ctx)
	if err != nil {
		return fmt.Errorf("failed to get window geometry: %w", err)
	}

	file := h.cfg.GenerateFilename()
	sleepWithCountdown(h.state, delay)

	_, err = external.Grim(ctx, geom, "", file)
	if err != nil {
		return fmt.Errorf("failed to capture screenshot: %w", err)
	}

	return notify.Send(3000, h.cfg.ScreenshotIcon, "Screenshot saved", filepath.Base(file))
}

// CurrentScreenClipboard captures the current screen and copies it to clipboard.
func (h *ScreenshotHandler) CurrentScreenClipboard(ctx context.Context, delay int, useCurrentScreen bool) error {
	output, err := sway.SelectOutput(ctx, useCurrentScreen)
	if err != nil || output == "" {
		return fmt.Errorf("failed to select output: %w", err)
	}

	if err := notify.CaptureDelay(delay, "screen to clipboard", h.cfg.ScreenshotIcon); err != nil {
		return err
	}

	sleepWithCountdown(h.state, delay)

	data, err := external.Grim(ctx, "", output, "")
	if err != nil {
		return fmt.Errorf("failed to capture screenshot: %w", err)
	}

	return external.WlCopy(ctx, data, "image/png")
}

// SelectionFile captures a selected region and saves it to a file.
func (h *ScreenshotHandler) SelectionFile(ctx context.Context, delay int) error {
	if err := notify.CaptureDelay(delay, "selection to file", h.cfg.ScreenshotIcon); err != nil {
		return err
	}

	geom, err := external.Slurp(ctx, "")
	if err != nil || geom == "" {
		return fmt.Errorf("selection cancelled or failed: %w", err)
	}

	file := h.cfg.GenerateFilename()
	sleepWithCountdown(h.state, delay)

	_, err = external.Grim(ctx, geom, "", file)
	if err != nil {
		return fmt.Errorf("failed to capture screenshot: %w", err)
	}

	actions := []notify.Action{
		{ID: "copyclip", Label: "Copy image"},
		{ID: "copypath", Label: "Copy path"},
	}
	if external.EntrySupported() {
		actions = append(actions,
			notify.Action{ID: "edit", Label: "Edit"},
			notify.Action{ID: "rename", Label: "Rename"})
	}

	action, err := notify.SendWithActions(30000, h.cfg.ScreenshotIcon, "Screenshot saved", filepath.Base(file), actions)
	if err != nil {
		// Action selection failed, but screenshot was saved
		return notify.Send(5000, h.cfg.ScreenshotIcon, "Screenshot saved", filepath.Base(file))
	}

	action = strings.TrimSpace(action)

	switch action {
	case "copyclip":
		data, err := os.ReadFile(file) //nolint:gosec
		if err != nil {
			return err
		}
		return external.WlCopy(ctx, data, "image/png")

	case "copypath":
		return external.WlCopyText(ctx, file)

	case "rename":
		ext := filepath.Ext(file)
		return askName(ctx, h.cfg.ScreenshotIcon, external.EntryRequest{
			Action: "rename", Dir: filepath.Dir(file), Ext: ext,
			Name: strings.TrimSuffix(filepath.Base(file), ext), File: file,
		}, func(target string, overwrite bool) error {
			if target == file {
				return nil
			}
			return gui.CommitFile(file, target, overwrite)
		})

	case "edit":
		// The original is kept unless its own name is chosen on purpose.
		ext := filepath.Ext(file)
		return askName(ctx, h.cfg.ScreenshotIcon, external.EntryRequest{
			Action: "edit", Dir: filepath.Dir(file), Ext: ext,
			Name: strings.TrimSuffix(filepath.Base(file), ext) + "-edited", File: file,
		}, func(target string, overwrite bool) error {
			return editInto(ctx, file, target, overwrite)
		})
	}

	return nil
}

// SelectionEdit captures a selected region, opens an editor, and saves the result.
func (h *ScreenshotHandler) SelectionEdit(ctx context.Context, delay int) error {
	if err := notify.CaptureDelay(delay, "selection edit", h.cfg.ScreenshotIcon); err != nil {
		return err
	}

	geom, err := external.Slurp(ctx, "#ff0000ff")
	if err != nil || geom == "" {
		return fmt.Errorf("selection cancelled or failed: %w", err)
	}

	sleepWithCountdown(h.state, delay)

	data, err := external.Grim(ctx, geom, "", "")
	if err != nil {
		return fmt.Errorf("failed to capture screenshot: %w", err)
	}

	// Write to temporary file for satty
	tmpFile := fmt.Sprintf("/tmp/screenshot-%d.png", time.Now().Unix())
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpFile) }()

	outputFile := filepath.Join(h.cfg.SaveDir(), fmt.Sprintf("screenshot-%s.png", time.Now().Format("20060102-15:04:05")))
	return external.Satty(ctx, tmpFile, outputFile, true)
}

// SelectionClipboard captures a selected region and copies it to clipboard.
func (h *ScreenshotHandler) SelectionClipboard(ctx context.Context, delay int) error {
	if err := notify.CaptureDelay(delay, "selection to clipboard", h.cfg.ScreenshotIcon); err != nil {
		return err
	}

	geom, err := external.Slurp(ctx, "")
	if err != nil || geom == "" {
		return fmt.Errorf("selection cancelled or failed: %w", err)
	}

	sleepWithCountdown(h.state, delay)

	data, err := external.Grim(ctx, geom, "", "")
	if err != nil {
		return fmt.Errorf("failed to capture screenshot: %w", err)
	}

	if err := external.WlCopy(ctx, data, "image/png"); err != nil {
		return err
	}

	if !external.EntrySupported() {
		return notify.Send(5000, h.cfg.ScreenshotIcon, "Screenshot copied", "The image is ready to paste from the clipboard.")
	}

	actions := []notify.Action{
		{ID: "save", Label: "Save"},
		{ID: "saveai", Label: "Save with AI"},
		{ID: "edit", Label: "Edit"},
	}

	action, err := notify.SendWithActions(30000, h.cfg.ScreenshotIcon, "Screenshot copied", "The image is ready to paste from the clipboard.", actions)
	if err != nil {
		return nil // Clipboard copy succeeded, ignore action error
	}

	action = strings.TrimSpace(action)

	if action != "save" && action != "saveai" && action != "edit" {
		return nil
	}

	dir := h.cfg.SaveDir()
	snapshot, err := writeSnapshot(dir, data)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(snapshot) }()

	req := external.EntryRequest{
		Action: "save", Dir: dir, Ext: ".png", File: snapshot,
		Name: strings.TrimSuffix(filepath.Base(config.GenerateFilename(dir)), ".png"),
	}
	if action == "edit" {
		req.Action = "edit"
	}
	if action == "saveai" {
		req.AIModel = h.cfg.AIModel()
	}

	return askName(ctx, h.cfg.ScreenshotIcon, req, func(target string, overwrite bool) error {
		if action == "edit" {
			return editInto(ctx, snapshot, target, overwrite)
		}
		if err := gui.WriteNewFile(target, data, overwrite); err != nil {
			return err
		}
		// Open in file manager
		return external.Nautilus(ctx, "file://"+target)
	})
}
