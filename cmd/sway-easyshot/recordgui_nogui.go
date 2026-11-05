//go:build nogui

package main

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

func recordGUICommand() *cli.Command {
	return &cli.Command{
		Name:  "record-gui",
		Usage: "Open a dialogue to configure and start a screen recording",
		Action: func(_ context.Context, _ *cli.Command) error {
			return errors.New("record-gui is unavailable: built without GUI support")
		},
	}
}
