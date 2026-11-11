package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/chmouel/tequilashot/internal/denoise"
	"github.com/chmouel/tequilashot/internal/prefs"

	"github.com/urfave/cli/v3"
)

func denoiseCommand() *cli.Command {
	return &cli.Command{
		Name:      "denoise",
		Usage:     "Remove background noise and keyboard clicks from a recording's sound, saving a copy beside it",
		ArgsUsage: "FILE",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Save the copy at this path instead of FILE-denoised"},
			&cli.BoolFlag{Name: "download-model", Usage: "Download the noise-removal model first if it is missing"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() != 1 {
				return errors.New("give exactly one file to clean")
			}
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			_ = prefs.Reload()
			if !denoise.ModelInstalled() {
				if !c.Bool("download-model") {
					return fmt.Errorf("the noise-removal model is not installed at %s; run again with --download-model", denoise.ModelPath())
				}
				fmt.Fprintf(os.Stderr, "Downloading the noise-removal model to %s\n", denoise.ModelPath())
				if err := denoise.DownloadModel(ctx, nil); err != nil {
					return err
				}
			}
			out, err := denoise.Export(ctx, c.Args().First(), c.String("output"), nil)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
}
