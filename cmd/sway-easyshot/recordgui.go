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

func recordGUICommand() *cli.Command {
	return &cli.Command{
		Name:  "record-gui",
		Usage: "Open a dialogue to configure and start a screen recording",
		Action: func(ctx context.Context, c *cli.Command) error {
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
		},
	}
}
