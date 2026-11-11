package external

import (
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chmouel/tequilashot/internal/prefs"
)

const testCameraDevice = "/dev/video0"

func TestCameraGsrSource(t *testing.T) {
	tests := []struct {
		name string
		c    CameraOverlay
		want string
	}{
		{"default", CameraOverlay{Device: testCameraDevice}, "/dev/video0;width=25%;halign=end;valign=end;x=-2%;y=-3%"},
		{"top-left small", CameraOverlay{Device: testCameraDevice, Position: CameraTopLeft, Size: CameraSmall}, "/dev/video0;width=15%;halign=start;valign=start;x=2%;y=3%"},
		{"top-right large", CameraOverlay{Device: testCameraDevice, Position: CameraTopRight, Size: CameraLarge}, "/dev/video0;width=35%;halign=end;valign=start;x=-2%;y=3%"},
		{"bottom-left medium", CameraOverlay{Device: "/dev/video2", Position: CameraBottomLeft, Size: CameraMedium}, "/dev/video2;width=25%;halign=start;valign=end;x=2%;y=-3%"},
		{
			"bottom-right mirror mode",
			CameraOverlay{Device: testCameraDevice, Position: CameraBottomRight, Mirror: true, Width: 1280, Height: 720, FPS: 30},
			"/dev/video0;width=25%;halign=end;valign=end;x=-2%;y=-3%;hflip=true;camera_width=1280;camera_height=720;camera_fps=30",
		},
		{"mode without fps", CameraOverlay{Device: testCameraDevice, Width: 640, Height: 480}, "/dev/video0;width=25%;halign=end;valign=end;x=-2%;y=-3%;camera_width=640;camera_height=480"},
	}
	for _, tt := range tests {
		if got := tt.c.gsrSource(); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCameraValidate(t *testing.T) {
	good := CameraOverlay{Device: testCameraDevice, Position: CameraTopLeft, Size: CameraLarge, Width: 1, Height: 1, FPS: 1}
	if err := good.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := (CameraOverlay{Device: "/dev/video12"}).Validate(); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	round := CameraOverlay{Device: testCameraDevice, Shape: CameraCircle, RemoveBackground: true, NoRing: true}
	if err := round.Validate(); err != nil {
		t.Fatalf("round camera without background rejected: %v", err)
	}
	if args := round.bubbleArgs(0, 0, 100, image.Rect(0, 0, 1920, 1080)); !slices.Contains(args, "--remove-background") || !slices.Contains(args, "--no-ring") {
		t.Errorf("bubble arguments miss the cut-out flags: %v", args)
	}
	bg := filepath.Join(t.TempDir(), "bg.png")
	if err := os.WriteFile(bg, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := []CameraOverlay{
		{},
		{Device: "/dev/video0|DP-1"},
		{Device: "/dev/video0;hflip=true"},
		{Device: "/dev/video0\n"},
		{Device: "/dev/videoX"},
		{Device: "x/dev/video0"},
		{Device: testCameraDevice, Position: "middle"},
		{Device: testCameraDevice, Size: "huge"},
		{Device: testCameraDevice, Shape: "oval"},
		{Device: testCameraDevice, Width: -1},
		{Device: testCameraDevice, FPS: -1},
		{Device: testCameraDevice, RemoveBackground: true},
		{Device: testCameraDevice, Shape: CameraSquare, RemoveBackground: true},
		{Device: testCameraDevice, Shape: CameraCircle, NoRing: true},
		{Device: testCameraDevice, Shape: CameraCircle, Background: "/nonexistent/bg.png"},
		{Device: testCameraDevice, Shape: CameraSquare, Background: os.DevNull},
		{Device: testCameraDevice, Shape: CameraCircle, Background: t.TempDir()},
		{Device: testCameraDevice, Shape: CameraCircle, Background: bg, RemoveBackground: true},
	}
	for _, c := range bad {
		if c.Validate() == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}

const sampleListing = `/dev/video0|1920x1080@30hz|mjpeg
/dev/video0|1920x1080@5hz|yuyv
/dev/video0|1280x720@30hz|mjpeg
/dev/video0|1280x720@25hz|mjpeg
garbage
/dev/video0|axb@30hz|mjpeg
/dev/video0|640x480@xhz|mjpeg
/dev/video0|640x480@29.97hz|mjpeg
/dev/video3|640x480@29.97hz|yuyv
/dev/video3|800x600@15hz|yuyv
/dev/video3;x|800x600@15hz|yuyv

`

func TestParseCameraModes(t *testing.T) {
	modes := ParseCameraModes(sampleListing)
	if len(modes) != 7 {
		t.Fatalf("got %d modes: %+v", len(modes), modes)
	}
	if modes[6].FPS != 15 || modes[5].FPS != 29.97 {
		t.Errorf("unexpected rates: %+v", modes)
	}
	if got := strings.Join(CameraDevices(modes), ","); got != "/dev/video0,/dev/video3" {
		t.Errorf("devices = %q", got)
	}
}

func TestParseCameraModesRejectsMalformedModes(t *testing.T) {
	for _, line := range []string{
		"/dev/video0|1280x720junk@30hz|mjpeg",
		"/dev/video0|1280x720@30|mjpeg",
		"/dev/video0|1280x@30hz|mjpeg",
		"/dev/video0|1280@30hz|mjpeg",
	} {
		if modes := ParseCameraModes(line); len(modes) != 0 {
			t.Errorf("%q was accepted: %+v", line, modes)
		}
	}
}

func TestBestCameraMode(t *testing.T) {
	modes := ParseCameraModes(sampleListing)
	check := func(device string, w, h, fps int) {
		t.Helper()
		gw, gh, gf := BestCameraMode(modes, device)
		if gw != w || gh != h || gf != fps {
			t.Errorf("%s: got %dx%d@%d, want %dx%d@%d", device, gw, gh, gf, w, h, fps)
		}
	}
	check(testCameraDevice, 1280, 720, 30)
	check("/dev/video3", 800, 600, 15)
	check("/dev/video9", 0, 0, 0)

	only := ParseCameraModes("/dev/video0|3840x2160@30hz|mjpeg\n/dev/video0|1920x1080@60hz|mjpeg\n/dev/video0|1920x1080@30hz|mjpeg\n")
	if w, h, f := BestCameraMode(only, testCameraDevice); w != 1920 || h != 1080 || f != 30 {
		t.Errorf("fallback got %dx%d@%d", w, h, f)
	}

	frac := ParseCameraModes("/dev/video0|1280x720@29.97hz|mjpeg\n")
	if w, h, f := BestCameraMode(frac, testCameraDevice); w != 1280 || h != 720 || f != 0 {
		t.Errorf("fractional got %dx%d@%d", w, h, f)
	}
}

func TestGsrArgs(t *testing.T) {
	cam := CameraOverlay{Device: testCameraDevice, Position: CameraTopLeft}
	args, err := gsrArgs(prefs.Default(), "10,20 800x600", "", "o.mp4", 0, 0, AudioSources{Desktop: true}, cam)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "-f 60 -c mp4 -o o.mp4 -w region|/dev/video0;width=25%;halign=start;valign=start;x=2%;y=3% -region 800x600+10+20 -a default_output"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	args, _ = gsrArgs(prefs.Default(), "", "DP-2", "o.mp4", 3840, 2160, AudioSources{}, cam)
	got = strings.Join(args, " ")
	if !strings.Contains(got, "-w DP-2|/dev/video0;") || !strings.Contains(got, "-s 1920x2160") {
		t.Errorf("output args: %q", got)
	}

	args, _ = gsrArgs(prefs.Default(), "", "DP-1", "o.mp4", 1920, 1080, AudioSources{}, CameraOverlay{})
	if got = strings.Join(args, " "); got != "-f 60 -c mp4 -o o.mp4 -w DP-1" {
		t.Errorf("plain args: %q", got)
	}

	if _, err := gsrArgs(prefs.Default(), "", "DP-1", "o.mp4", 0, 0, AudioSources{}, CameraOverlay{Device: "/dev/video0|DP-1"}); err == nil {
		t.Error("expected validation error")
	}

	round := CameraOverlay{Device: testCameraDevice, Shape: CameraCircle}
	args, _ = gsrArgs(prefs.Default(), "", "DP-1", "o.mp4", 1920, 1080, AudioSources{}, round)
	if got = strings.Join(args, " "); got != "-f 60 -c mp4 -o o.mp4 -w DP-1" {
		t.Errorf("a round camera must be left to the bubble: %q", got)
	}
}

func TestCircleLayout(t *testing.T) {
	// A medium 16:9 overlay in a 1600x900 frame is 400x225 at 1168,648.
	if x, y, d := CircleLayout(1600, 900, CameraMedium, CameraBottomRight); x != 1168+400-225 || y != 648 || d != 225 {
		t.Errorf("bottom-right: %v %v %v", x, y, d)
	}
	if x, y, d := CircleLayout(1600, 900, CameraMedium, CameraTopLeft); x != 32 || y != 27 || d != 225 {
		t.Errorf("top-left: %v %v %v", x, y, d)
	}
}

func TestBubbleFailure(t *testing.T) {
	for stderr, want := range map[string]string{
		"noise\n2026/11/05 16:40:38 camera /dev/video0 could not be opened\n": "camera /dev/video0 could not be opened",
		"": "exited unexpectedly",
	} {
		if got := bubbleFailure(stderr); got != want {
			t.Errorf("bubbleFailure(%q) = %q, want %q", stderr, got, want)
		}
	}
}

func TestCameraNameIn(t *testing.T) {
	root := t.TempDir()
	write := func(dir, name string) {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "name"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("video0", "Integrated Camera\n")
	write("video2", "  \n")

	for device, want := range map[string]string{
		testCameraDevice: "Integrated Camera",
		"/dev/video2":    "/dev/video2",
		"/dev/video9":    "/dev/video9",
		"/dev/../evil":   "/dev/../evil",
	} {
		if got := CameraNameIn(root, device); got != want {
			t.Errorf("CameraNameIn(%q) = %q, want %q", device, got, want)
		}
	}
}

func TestCameraLayout(t *testing.T) {
	x, y, w, h := CameraLayout(1000, 500, 2, CameraMedium, CameraBottomRight)
	if x != 730 || y != 500-125-15 || w != 250 || h != 125 {
		t.Errorf("bottom-right medium: %v %v %v %v", x, y, w, h)
	}
	x, y, w, _ = CameraLayout(1000, 500, 2, CameraSmall, CameraTopLeft)
	if x != 20 || y != 15 || w != 150 {
		t.Errorf("top-left small: %v %v %v", x, y, w)
	}
	x, y, w, h = CameraLayout(1000, 500, 0, CameraLarge, CameraTopRight)
	if x != 630 || y != 15 || w != 350 || h != 350*9/16.0 {
		t.Errorf("top-right large: %v %v %v %v", x, y, w, h)
	}
	x, y, _, _ = CameraLayout(1000, 500, 2, "", CameraBottomLeft)
	if x != 20 || y != 360 {
		t.Errorf("bottom-left default size: %v %v", x, y)
	}
}

func TestPreviewMode(t *testing.T) {
	modes := ParseCameraModes("/dev/video0|1280x720@30hz|mjpeg\n/dev/video0|640x480@30hz|mjpeg\n/dev/video0|640x360@30hz|mjpeg\n/dev/video0|320x240@30hz|mjpeg\n/dev/video1|160x120@30hz|mjpeg\n")
	if w, h := PreviewMode(modes, testCameraDevice); w != 640 || h != 360 {
		t.Errorf("same aspect: %dx%d", w, h)
	}
	if w, h := PreviewMode(modes[:2], testCameraDevice); w != 640 || h != 480 {
		t.Errorf("fallback to largest: %dx%d", w, h)
	}
	if w, h := PreviewMode(modes, "/dev/video7"); w != 640 || h != 480 {
		t.Errorf("unknown device: %dx%d", w, h)
	}
	if w, h := PreviewMode(ParseCameraModes("/dev/video0|1920x1080@30hz|mjpeg\n"), testCameraDevice); w != 640 || h != 480 {
		t.Errorf("no small mode: %dx%d", w, h)
	}
}

func TestPreviewArgs(t *testing.T) {
	native := strings.Join(PreviewArgs(testCameraDevice, 640, 360, 15, true), " ")
	if !strings.Contains(native, "-input_format mjpeg") || !strings.Contains(native, "-c:v copy") || !strings.Contains(native, "-video_size 640x360") || !strings.HasSuffix(native, "-f mjpeg -") {
		t.Errorf("native: %s", native)
	}
	raw := strings.Join(PreviewArgs(testCameraDevice, 640, 480, 30, false), " ")
	if !strings.Contains(raw, "-input_format yuyv422") || !strings.Contains(raw, "-c:v mjpeg -q:v 5") || !strings.Contains(raw, "-framerate 30") {
		t.Errorf("raw: %s", raw)
	}
}

func TestContainRect(t *testing.T) {
	near := func(a, b float64) bool { return a-b < 0.01 && b-a < 0.01 }
	x, y, w, h := ContainRect(460, 259, 1920.0/1200)
	if !near(x, 22.8) || !near(y, 0) || !near(w, 414.4) || !near(h, 259) {
		t.Fatalf("1920x1200 in 460x259: %v %v %v %v", x, y, w, h)
	}
	x, y, w, h = ContainRect(460, 259, 21.0/9)
	if !near(x, 0) || !near(w, 460) || !near(h, 460*9.0/21) || !near(y, (259-h)/2) {
		t.Fatalf("21:9: %v %v %v %v", x, y, w, h)
	}
	if _, _, w, h = ContainRect(460, 259, 0); w != 460 || h != 259 {
		t.Fatalf("no aspect: %v %v", w, h)
	}
}

func TestCameraIndex(t *testing.T) {
	devs := []string{testCameraDevice, "/dev/video2", "/dev/video4"}
	if i := CameraIndex(devs, "/dev/video4"); i != 2 {
		t.Fatalf("got %d", i)
	}
	if i := CameraIndex(devs, "/dev/video9"); i != 0 {
		t.Fatalf("got %d", i)
	}
	if i := CameraIndex(nil, "x"); i != 0 {
		t.Fatalf("got %d", i)
	}
}

func TestCameraBackgroundImage(t *testing.T) {
	bg := filepath.Join(t.TempDir(), "bg.png")
	if err := os.WriteFile(bg, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := CameraOverlay{Device: testCameraDevice, Shape: CameraCircle, Background: bg, NoRing: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("background image rejected: %v", err)
	}
	args := c.bubbleArgs(0, 0, 100, image.Rect(0, 0, 1920, 1080))
	i := slices.Index(args, "--background")
	if i < 0 || args[i+1] != bg {
		t.Errorf("bubble arguments miss the background: %v", args)
	}
	if slices.Contains(args, "--remove-background") {
		t.Errorf("background image must not request removal: %v", args)
	}
}
