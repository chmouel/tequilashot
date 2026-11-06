package external

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chmouel/tequilashot/internal/prefs"
)

func TestRecordingArgsPreferences(t *testing.T) {
	p := prefs.Default()
	p.FrameRate = 30
	p.MaxWidth = 0
	p.VideoCodec = "av1"
	p.Quality = "ultra"
	p.RecordCursor = false
	p.Encoder = "cpu"
	p.RecordingArgs = "-ac opus -cr full"
	args, err := recordingArgs(p, "", "DP-1", "/out.mp4", 3840, 2160, AudioSources{}, CameraOverlay{})
	if err != nil {
		t.Fatal(err)
	}
	want := "-f 30 -c mp4 -o /out.mp4 -w DP-1 -cursor no -encoder cpu -k av1 -q ultra -ac opus -cr full"
	if got := strings.Join(args, " "); got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}

	p.RecordingArgs = "-o /etc/passwd"
	if _, err := recordingArgs(p, "", "DP-1", "/out.mp4", 0, 0, AudioSources{}, CameraOverlay{}); err == nil {
		t.Fatal("accepted a forbidden extra argument")
	}
}

func TestReplayArgsPreferences(t *testing.T) {
	p := prefs.Default()
	p.ReplayBitrate = 8000
	p.ReplayArgs = "-ac opus"
	args, err := replayArgs(p, "DP-1", "/clips", "/run/s", 30, 0, 0, AudioSources{})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(args, "-q"); i < 0 || args[i+1] != "8000" {
		t.Fatalf("bitrate missing from %q", args)
	}
	if !reflect.DeepEqual(args[len(args)-2:], []string{"-ac", "opus"}) {
		t.Fatalf("extras not last: %q", args)
	}
	p.ReplayArgs = "-bm vbr"
	if _, err := replayArgs(p, "DP-1", "/clips", "/run/s", 30, 0, 0, AudioSources{}); err == nil {
		t.Fatal("replay accepted -bm")
	}
}

func TestGsrOutputSizeUsesGivenExecutable(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gpu-screen-recorder")
	script := "#!/bin/sh\necho 'DP-1|3840x2160'\necho 'DP-2|2560x1440'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if w, h := gsrOutputSize(context.Background(), bin, "DP-2"); w != 2560 || h != 1440 {
		t.Fatalf("DP-2 size = %dx%d, want 2560x1440", w, h)
	}
	if w, h := gsrOutputSize(context.Background(), bin, "HDMI-1"); w != 0 || h != 0 {
		t.Fatalf("unknown output size = %dx%d, want 0x0", w, h)
	}
}

func TestGrimArgsCursor(t *testing.T) {
	p := prefs.Default()
	p.ScreenshotCursor = true
	if got := grimArgs(p, "", "DP-1", "/s.png"); !reflect.DeepEqual(got, []string{"-t", "png", "-c", "-o", "DP-1", "/s.png"}) {
		t.Fatalf("grim arguments = %q", got)
	}
}
