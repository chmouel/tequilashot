package gui

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func frameFixture(t *testing.T, rate string, variable bool) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg is required")
	}
	file := filepath.Join(t.TempDir(), "frames.mp4")
	filter := "nullsrc=s=160x96:r=" + rate + ":d=1.2,geq=lum='16+219*mod(floor(N/pow(2,floor(X/20))),2)':cb=128:cr=128"
	if variable {
		filter += ",select='not(mod(n,3))+not(mod(n,5))'"
	}
	trimTestCommand(t, "-f", "lavfi", "-i", filter, "-c:v", "libx264", "-crf", "12", "-g", "12", "-fps_mode", "vfr", file)
	return file
}

func assertTrimFrameRange(t *testing.T, source, target string, first, end int) {
	t.Helper()
	original := trimTestCommand(t, "-i", source, "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "gray", "-")
	saved := trimTestCommand(t, "-i", target, "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "gray", "-")
	const pixels = 160 * 96
	if len(saved) != (end-first)*pixels {
		t.Fatalf("saved %d frames; expected %d", len(saved)/pixels, end-first)
	}
	for _, pair := range [][2]int{{0, first}, {end - first - 1, end - 1}} {
		var diff float64
		for i := range pixels {
			diff += math.Abs(float64(saved[pair[0]*pixels+i]) - float64(original[pair[1]*pixels+i]))
		}
		if diff/pixels > 1.5 {
			t.Fatalf("output frame %d does not match source frame %d (difference %.2f)", pair[0], pair[1], diff/pixels)
		}
	}
}

func TestFrameTimingAndExport(t *testing.T) {
	for _, tc := range []struct {
		name, rate       string
		variable, offset bool
	}{
		{"24", "24", false, false},
		{"30", "30", false, false},
		{"60", "60", false, false},
		{"fractional", "30000/1001", false, false},
		{"variable", "60", true, false},
		{"offset", "30000/1001", false, true},
		{"offset-mkv", "30000/1001", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := frameFixture(t, tc.rate, tc.variable)
			if tc.offset {
				ext := ".mp4"
				if tc.name == "offset-mkv" {
					ext = ".mkv"
				}
				target := filepath.Join(t.TempDir(), "offset"+ext)
				trimTestCommand(t, "-i", file, "-c", "copy", "-output_ts_offset", "5", target)
				file = target
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			info, err := ProbeMedia(ctx, file)
			if err != nil {
				t.Fatal(err)
			}
			clock, err := probeFrameClock(ctx, file)
			if err != nil {
				t.Fatal(err)
			}
			w, err := loadFrameWindow(ctx, file, clock, 400*time.Millisecond, info.Duration)
			if err != nil || len(w.times) < 10 || !w.first || !w.last || w.times[0] != 0 {
				t.Fatalf("frame window %+v: %v", w, err)
			}
			if tc.offset && clock.origin != 5*time.Second {
				t.Fatalf("source origin %s", clock.origin)
			}
			if tc.name == "fractional" && (w.times[1] != 33366666*time.Nanosecond || w.times[30] != 1001*time.Millisecond) {
				t.Fatalf("fractional frame times drifted: %v", w.times)
			}
			for i, stamp := range w.times {
				if w.index(stamp) != i {
					t.Fatalf("frame %d is not reversible", i)
				}
			}
			for _, span := range [][2]int{{3, 8}, {len(w.times) - 1, len(w.times)}} {
				end := info.Duration
				if span[1] < len(w.times) {
					end = w.times[span[1]]
				}
				target, _, err := exportTrim(ctx, file, false, trimSelection{w.times[span[0]], end, info.Duration, false}, nil)
				if err != nil {
					t.Fatal(err)
				}
				assertTrimFrameRange(t, file, target, span[0], span[1])
			}
		})
	}
}

func TestFrameWindowBounded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "long.mp4")
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg is required")
	}
	trimTestCommand(t, "-f", "lavfi", "-i", "color=red:s=160x96:r=120:d=16", "-c:v", "libx264", "-g", "1920", file)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	clock, err := probeFrameClock(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	w, err := loadFrameWindow(ctx, file, clock, 8*time.Second, 16*time.Second)
	if err != nil || len(w.times) > 512 || w.first || w.last || w.times[w.index(8*time.Second)] != 8*time.Second {
		t.Fatalf("bounded cache: %d frames, %+v, %v", len(w.times), w, err)
	}
}
