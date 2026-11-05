//go:build nogui

package main

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

func guiAction(_ context.Context, _ *cli.Command) error {
	return errors.New("the recording dialogue is unavailable: built without GUI support")
}
