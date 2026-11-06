//go:build !nogui

package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/gui"
	"github.com/chmouel/tequilashot/internal/prefs"
	"github.com/chmouel/tequilashot/pkg/protocol"

	"github.com/urfave/cli/v3"
)

func guiAction(_ context.Context, _ *cli.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	return gui.Run(gui.Backend{
		SaveLocation: cfg.SaveDir(),
		EnsureDaemon: func() error { return ensureDaemonRunning(cfg) },
		Send: func(req protocol.Request) (*protocol.Response, error) {
			timeout := 10 * time.Second
			if req.Action == "replay-save" || req.Action == "replay-stop" {
				timeout = external.ReplayExportTimeout + 5*time.Second
			}
			return sendRequestTimeout(cfg.SocketPath, req, timeout)
		},
		// Interactive selection waits on the user for as long as needed.
		SendStart: func(req protocol.Request) (*protocol.Response, error) {
			return sendRequestTimeout(cfg.SocketPath, req, 0)
		},
	})
}

func cameraBubbleAction(_ context.Context, c *cli.Command) error {
	_ = prefs.Reload()
	return gui.RunCameraBubble(gui.BubbleOptions{
		Device:           c.String("device"),
		X:                c.Int("x"),
		Y:                c.Int("y"),
		Diameter:         c.Int("diameter"),
		Area:             image.Rect(c.Int("area-x"), c.Int("area-y"), c.Int("area-x")+c.Int("area-width"), c.Int("area-y")+c.Int("area-height")),
		Size:             c.String("size"),
		Width:            c.Int("width"),
		Height:           c.Int("height"),
		FPS:              c.Int("fps"),
		Mirror:           c.Bool("mirror"),
		RemoveBackground: c.Bool("remove-background"),
		NoRing:           c.Bool("no-ring"),
	})
}

// entryAction answers the daemon's request for a file name. The result is
// carried by the exit status: 0 for a name, 3 for a name that replaces an
// existing file, 1 when declined and 2 on failure.
func entryAction(_ context.Context, c *cli.Command) error {
	_ = prefs.Reload()
	if c.Bool("probe") {
		if !gui.ProbeEntry() {
			os.Exit(entryExitError)
		}
		return nil
	}
	res, err := gui.RunEntry(gui.EntryOptions{
		Action:  gui.NameAction(c.String("action")),
		Dir:     c.String("dir"),
		Ext:     c.String("ext"),
		Name:    c.String("name"),
		File:    c.String("file"),
		Problem: c.String("problem"),
		AIModel: c.String("ai-model"),
	})
	switch {
	case errors.Is(err, gui.ErrEntryCancelled):
		os.Exit(entryExitCancel)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(entryExitError)
	}
	fmt.Println(res.Name)
	if res.Overwrite {
		os.Exit(entryExitOverwrite)
	}
	return nil
}
