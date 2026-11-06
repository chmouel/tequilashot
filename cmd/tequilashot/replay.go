package main

import (
	"context"
	"fmt"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/pkg/protocol"
	"github.com/urfave/cli/v3"
)

func replayStartCommand() *cli.Command {
	return &cli.Command{
		Name: "replay-start", Usage: "Buffer a monitor for instant replay",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "seconds", Value: 60, Usage: "Buffer duration: 30, 60 or 120 seconds"},
			&cli.BoolFlag{Name: "current-screen", Usage: "Use the focused monitor without asking"},
			&cli.BoolFlag{Name: "audio", Usage: "Include desktop sound"},
			&cli.BoolFlag{Name: "mic", Usage: "Include the default microphone"},
			&cli.StringFlag{Name: "mic-device", Usage: "Microphone device id (implies --mic)"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			seconds := c.Int("seconds")
			if !external.ValidReplaySeconds(seconds) {
				return fmt.Errorf("replay duration must be 30, 60 or 120 seconds")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := ensureDaemonRunning(cfg); err != nil {
				return err
			}
			return sendAndHandleStartRequest(cfg.SocketPath, protocol.Request{
				Command: "execute", Action: "replay-start",
				Options: withMicOptions(c, map[string]interface{}{
					"seconds": seconds, "use_current_screen": c.Bool("current-screen"),
					"audio": c.Bool("audio"), "mic": c.Bool("mic"),
				}),
			})
		},
	}
}

func replayControlCommand(action, usage string) *cli.Command {
	return &cli.Command{Name: action, Usage: usage, Action: func(_ context.Context, c *cli.Command) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		resp, err := sendRequestTimeout(cfg.SocketPath, protocol.Request{Command: "execute", Action: action}, external.ReplayExportTimeout+5*time.Second)
		if err := handleResponse(resp, err); err != nil {
			return err
		}
		if action == "replay-save" {
			if resp.File == "" {
				return fmt.Errorf("the daemon did not report a replay file")
			}
			_, err = fmt.Fprintln(c.Root().Writer, resp.File)
		}
		return err
	}}
}
