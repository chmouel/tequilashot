package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestReplayCompletion(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, tc := range []struct {
		command *cli.Command
		prefix  string
		want    []string
	}{
		{replayStartCommand(), "--m", []string{"--mic", "--mic-device"}},
		{replayStartCommand(), "--s", []string{"--seconds"}},
		{waybarStatusCommand(), "--icon-r", []string{"--icon-replay", "--icon-recording"}},
	} {
		var out bytes.Buffer
		root := &cli.Command{Name: "tequilashot", EnableShellCompletion: true, Writer: &out, Commands: []*cli.Command{tc.command}}
		os.Args = []string{"tequilashot", tc.command.Name, tc.prefix, "--generate-shell-completion"}
		if err := root.Run(context.Background(), os.Args); err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s %s: missing %s in %q", tc.command.Name, tc.prefix, want, out.String())
			}
		}
	}
}

func TestCompletionScriptsAreWellFormed(t *testing.T) {
	for _, shell := range []string{"fish", "bash", "zsh"} {
		var out bytes.Buffer
		root := &cli.Command{Name: "tequilashot", EnableShellCompletion: true, Writer: &out, Commands: []*cli.Command{replayStartCommand(), waybarStatusCommand()}}
		if err := root.Run(context.Background(), []string{"tequilashot", "completion", shell}); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if out.Len() == 0 || strings.Contains(out.String(), "%!") {
			t.Errorf("%s completion script is empty or contains formatting artefacts:\n%s", shell, out.String())
		}
	}
}
