//go:build nogui

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func guiAction(_ context.Context, _ *cli.Command) error {
	return errors.New("the capture dialogue is unavailable: built without GUI support")
}

func cameraBubbleAction(_ context.Context, _ *cli.Command) error {
	return errors.New("the round camera is unavailable: built without GUI support")
}

func entryAction(_ context.Context, _ *cli.Command) error {
	fmt.Fprintln(os.Stderr, "the naming window is unavailable: built without GUI support")
	os.Exit(entryExitError)
	return nil
}
