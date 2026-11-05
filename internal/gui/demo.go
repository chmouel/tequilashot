//go:build demo && !nogui

package gui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"sway-easyshot/internal/sway"
)

// The demo build stages the dialogue for documentation screenshots:
//
//	SWAY_EASYSHOT_DEMO=monitor|window|region|result
//	SWAY_EASYSHOT_DEMO_DIR=dir holding DP-1.jpg, HDMI-A-1.jpg and the demoClip recording
//
// Nothing is read from or written to the real desktop or settings.
const demoClip = "recording-20261105-13h04.mp4"

func init() {
	page := os.Getenv("SWAY_EASYSHOT_DEMO")
	if page == "" {
		return
	}
	dir := os.Getenv("SWAY_EASYSHOT_DEMO_DIR")

	listMonitors = func(context.Context) ([]sway.Monitor, error) {
		return []sway.Monitor{
			{Name: "DP-1", Width: 3840, Height: 2160, Focused: true},
			{Name: "HDMI-A-1", X: 3840, Width: 1920, Height: 1080},
		}, nil
	}
	listWindows = func(context.Context) ([]sway.Window, error) {
		return []sway.Window{
			{ID: "1", Title: "sway-easyshot — README.md", Class: "com.mitchellh.ghostty", Width: 1906, Height: 2115},
			{ID: "2", Title: "Release notes — Mozilla Firefox", Class: "firefox", Width: 1906, Height: 2115},
			{ID: "3", Title: "Videos", Class: "org.gnome.Nautilus", Width: 1280, Height: 800},
			{ID: "4", Title: "Weekly notes — Obsidian", Class: "obsidian", Width: 1906, Height: 1035},
		}, nil
	}
	grabThumbnail = func(_ context.Context, output string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, output+".jpg"))
	}

	demoSettings = func(s *Settings) bool {
		*s = Settings{Source: SourceMonitor, Monitor: "DP-1", Desktop: true, Mic: true, Countdown: 3}
		switch page {
		case SourceWindow, SourceRegion:
			s.Source = page
		}
		return true
	}

	demoStage = func(a *app) {
		glib.TimeoutAdd(800, func() bool {
			switch page {
			case SourceWindow:
				if row := a.winList.RowAtIndex(1); row != nil {
					a.winList.SelectRow(row)
				}
			case "result":
				clip := filepath.Join(dir, demoClip)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				info, err := ProbeMedia(ctx, clip)
				cancel()
				if err == nil {
					a.showResult(clip, info)
				}
			}
			return false
		})
	}
}
