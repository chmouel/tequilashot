//go:build nogui

package main

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

func guiAction(_ context.Context, _ *cli.Command) error {
	return errors.New("the capture dialogue is unavailable: built without GUI support")
}

func cameraBubbleAction(_ context.Context, _ *cli.Command) error {
	return errors.New("the round camera is unavailable: built without GUI support")
}
