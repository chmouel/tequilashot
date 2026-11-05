package external

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCameraGsrSource(t *testing.T) {
	tests := []struct {
		name string
		c    CameraOverlay
		want string
	}{
		{"default", CameraOverlay{Device: "/dev/video0"}, "/dev/video0;width=25%;halign=end;valign=end;x=-2%;y=-3%"},
		{"top-left small", CameraOverlay{Device: "/dev/video0", Position: CameraTopLeft, Size: CameraSmall}, "/dev/video0;width=15%;halign=start;valign=start;x=2%;y=3%"},
		{"top-right large", CameraOverlay{Device: "/dev/video0", Position: CameraTopRight, Size: CameraLarge}, "/dev/video0;width=35%;halign=end;valign=start;x=-2%;y=3%"},
		{"bottom-left medium", CameraOverlay{Device: "/dev/video2", Position: CameraBottomLeft, Size: CameraMedium}, "/dev/video2;width=25%;halign=start;valign=end;x=2%;y=-3%"},
		{"bottom-right mirror mode", CameraOverlay{Device: "/dev/video0", Position: CameraBottomRight, Mirror: true, Width: 1280, Height: 720, FPS: 30},
			"/dev/video0;width=25%;halign=end;valign=end;x=-2%;y=-3%;hflip=true;camera_width=1280;camera_height=720;camera_fps=30"},
		{"mode without fps", CameraOverlay{Device: "/dev/video0", Width: 640, Height: 480}, "/dev/video0;width=25%;halign=end;valign=end;x=-2%;y=-3%;camera_width=640;camera_height=480"},
	}
	for _, tt := range tests {
		if got := tt.c.gsrSource(); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCameraValidate(t *testing.T) {
	good := CameraOverlay{Device: "/dev/video0", Position: CameraTopLeft, Size: CameraLarge, Width: 1, Height: 1, FPS: 1}
	if err := good.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := (CameraOverlay{Device: "/dev/video12"}).Validate(); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	bad := []CameraOverlay{
		{},
		{Device: "/dev/video0|DP-1"},
		{Device: "/dev/video0;hflip=true"},
		{Device: "/dev/video0\n"},
		{Device: "/dev/videoX"},
		{Device: "x/dev/video0"},
		{Device: "/dev/video0", Position: "middle"},
		{Device: "/dev/video0", Size: "huge"},
		{Device: "/dev/video0", Width: -1},
		{Device: "/dev/video0", FPS: -1},
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
	check("/dev/video0", 1280, 720, 30)
	check("/dev/video3", 800, 600, 15)
	check("/dev/video9", 0, 0, 0)

	only := ParseCameraModes("/dev/video0|3840x2160@30hz|mjpeg\n/dev/video0|1920x1080@60hz|mjpeg\n/dev/video0|1920x1080@30hz|mjpeg\n")
	if w, h, f := BestCameraMode(only, "/dev/video0"); w != 1920 || h != 1080 || f != 30 {
		t.Errorf("fallback got %dx%d@%d", w, h, f)
	}

	frac := ParseCameraModes("/dev/video0|1280x720@29.97hz|mjpeg\n")
	if w, h, f := BestCameraMode(frac, "/dev/video0"); w != 1280 || h != 720 || f != 0 {
		t.Errorf("fractional got %dx%d@%d", w, h, f)
	}
}

func TestGsrArgs(t *testing.T) {
	cam := CameraOverlay{Device: "/dev/video0", Position: CameraTopLeft}
	args, err := gsrArgs("10,20 800x600", "", "o.mp4", 0, 0, AudioSources{Desktop: true}, cam)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "-f 60 -c mp4 -o o.mp4 -w region|/dev/video0;width=25%;halign=start;valign=start;x=2%;y=3% -region 800x600+10+20 -a default_output"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	args, _ = gsrArgs("", "DP-2", "o.mp4", 3840, 2160, AudioSources{}, cam)
	got = strings.Join(args, " ")
	if !strings.Contains(got, "-w DP-2|/dev/video0;") || !strings.Contains(got, "-s 1920x2160") {
		t.Errorf("output args: %q", got)
	}

	args, _ = gsrArgs("", "DP-1", "o.mp4", 1920, 1080, AudioSources{}, CameraOverlay{})
	if got = strings.Join(args, " "); got != "-f 60 -c mp4 -o o.mp4 -w DP-1" {
		t.Errorf("plain args: %q", got)
	}

	if _, err := gsrArgs("", "DP-1", "o.mp4", 0, 0, AudioSources{}, CameraOverlay{Device: "/dev/video0|DP-1"}); err == nil {
		t.Error("expected validation error")
	}
}

func TestCameraNameIn(t *testing.T) {
	root := t.TempDir()
	write := func(dir, name string) {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "name"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("video0", "Integrated Camera\n")
	write("video2", "  \n")

	for device, want := range map[string]string{
		"/dev/video0":  "Integrated Camera",
		"/dev/video2":  "/dev/video2",
		"/dev/video9":  "/dev/video9",
		"/dev/../evil": "/dev/../evil",
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
	if w, h := PreviewMode(modes, "/dev/video0"); w != 640 || h != 360 {
		t.Errorf("same aspect: %dx%d", w, h)
	}
	if w, h := PreviewMode(modes[:2], "/dev/video0"); w != 640 || h != 480 {
		t.Errorf("fallback to largest: %dx%d", w, h)
	}
	if w, h := PreviewMode(modes, "/dev/video7"); w != 640 || h != 480 {
		t.Errorf("unknown device: %dx%d", w, h)
	}
	if w, h := PreviewMode(ParseCameraModes("/dev/video0|1920x1080@30hz|mjpeg\n"), "/dev/video0"); w != 640 || h != 480 {
		t.Errorf("no small mode: %dx%d", w, h)
	}
}

func TestPreviewArgs(t *testing.T) {
	native := strings.Join(PreviewArgs("/dev/video0", 640, 360, true), " ")
	if !strings.Contains(native, "-input_format mjpeg") || !strings.Contains(native, "-c:v copy") || !strings.Contains(native, "-video_size 640x360") || !strings.HasSuffix(native, "-f mjpeg -") {
		t.Errorf("native: %s", native)
	}
	raw := strings.Join(PreviewArgs("/dev/video0", 640, 480, false), " ")
	if !strings.Contains(raw, "-input_format yuyv422") || !strings.Contains(raw, "-c:v mjpeg -q:v 5") {
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
	devs := []string{"/dev/video0", "/dev/video2", "/dev/video4"}
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
