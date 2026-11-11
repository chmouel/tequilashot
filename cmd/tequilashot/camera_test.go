package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

const testProgram = "tequilashot"

func TestCameraPositionOnlySentWhenExplicit(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tequilashot", "--camera"}, ""},
		{[]string{"tequilashot", "--camera", "--camera-position", "bottom-right"}, "bottom-right"},
	} {
		var opts map[string]interface{}
		cmd := &cli.Command{Name: "tequilashot", Flags: cameraFlags(), Action: func(_ context.Context, c *cli.Command) error {
			opts = withCameraOptions(c, map[string]interface{}{})
			return nil
		}}
		if err := cmd.Run(context.Background(), tc.args); err != nil {
			t.Fatal(err)
		}
		got, exists := opts["camera_position"]
		if (tc.want == "" && exists) || (tc.want != "" && got != tc.want) {
			t.Fatalf("%v: camera position = %v (present %v)", tc.args, got, exists)
		}
	}
}

func TestCameraCycleCompletion(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	var out bytes.Buffer
	root := &cli.Command{Name: "tequilashot", EnableShellCompletion: true, Writer: &out, Commands: []*cli.Command{cameraCommand()}}
	os.Args = []string{"tequilashot", "camera", "c", "--generate-shell-completion"}
	if err := root.Run(context.Background(), os.Args); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cycle") {
		t.Fatalf("camera cycle missing from completion: %q", out.String())
	}
}

func TestCameraMattingModelOnlySentWhenExplicit(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{testProgram, "--camera"}, ""},
		{[]string{testProgram, "--camera", "--camera-matting-model", "u2net"}, "u2net"},
		{[]string{testProgram, "--camera", "--camera-matting-model", "modnet"}, "modnet"},
	} {
		var opts map[string]interface{}
		cmd := &cli.Command{Name: testProgram, Flags: cameraFlags(), Action: func(_ context.Context, c *cli.Command) error {
			opts = withCameraOptions(c, map[string]interface{}{})
			return nil
		}}
		if err := cmd.Run(context.Background(), tc.args); err != nil {
			t.Fatal(err)
		}
		got, exists := opts["camera_matting_model"]
		if (tc.want == "" && exists) || (tc.want != "" && got != tc.want) {
			t.Fatalf("%v: matting model = %v (present %v)", tc.args, got, exists)
		}
	}
}
