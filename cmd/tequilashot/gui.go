//go:build !nogui

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/gui"
	"github.com/chmouel/tequilashot/pkg/protocol"

	"github.com/urfave/cli/v3"
)

func guiAction(_ context.Context, _ *cli.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	return gui.Run(gui.Backend{
		SaveLocation: cfg.SaveLocation,
		EnsureDaemon: func() error { return ensureDaemonRunning(cfg) },
		Send: func(req protocol.Request) (*protocol.Response, error) {
			return sendRequestTimeout(cfg.SocketPath, req, 10*time.Second)
		},
		// Interactive selection waits on the user for as long as needed.
		SendStart: func(req protocol.Request) (*protocol.Response, error) {
			return sendRequestTimeout(cfg.SocketPath, req, 0)
		},
	})
}

func cameraBubbleAction(_ context.Context, c *cli.Command) error {
	return gui.RunCameraBubble(gui.BubbleOptions{
		Device:   c.String("device"),
		X:        c.Int("x"),
		Y:        c.Int("y"),
		Diameter: c.Int("diameter"),
		Width:    c.Int("width"),
		Height:   c.Int("height"),
		FPS:      c.Int("fps"),
		Mirror:   c.Bool("mirror"),
	})
}
