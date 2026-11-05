//go:build !nogui

package main

import (
	"context"
	"fmt"
	"time"

	"sway-easyshot/internal/config"
	"sway-easyshot/internal/gui"
	"sway-easyshot/pkg/protocol"

	"github.com/urfave/cli/v3"
)

// guiAction opens the recording dialogue; it is the default action.
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
