package gui

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTrimSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		selectn trimSelection
		want    []trimSegment
	}{
		{"keep", trimSelection{time.Second, 3 * time.Second, 4 * time.Second, false}, []trimSegment{{time.Second, 3 * time.Second}}},
		{"remove middle", trimSelection{time.Second, 3 * time.Second, 4 * time.Second, true}, []trimSegment{{0, time.Second}, {3 * time.Second, 4 * time.Second}}},
		{"remove start", trimSelection{0, time.Second, 4 * time.Second, true}, []trimSegment{{time.Second, 4 * time.Second}}},
		{"remove end", trimSelection{time.Second, 4 * time.Second, 4 * time.Second, true}, []trimSegment{{0, time.Second}}},
		{"empty", trimSelection{time.Second, time.Second, 4 * time.Second, false}, nil},
		{"reversed", trimSelection{3 * time.Second, time.Second, 4 * time.Second, false}, nil},
		{"negative", trimSelection{-time.Second, time.Second, 4 * time.Second, false}, nil},
		{"past end", trimSelection{0, 5 * time.Second, 4 * time.Second, false}, nil},
		{"unknown duration", trimSelection{0, time.Second, 0, false}, nil},
		{"remove everything", trimSelection{0, 4 * time.Second, 4 * time.Second, true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.selectn.segments()
			if (err != nil) != (tc.want == nil) || !slices.Equal(got, tc.want) {
				t.Fatalf("segments() = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func trimTestCommand(t *testing.T, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg", append([]string{"-hide_banner", "-v", "error", "-nostdin"}, args...)...).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
	return out
}

func TestExportTrim(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "recording with spaces.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "color=red:s=160x96:r=20:d=1[r];color=lime:s=160x96:r=20:d=2[g];color=blue:s=160x96:r=20:d=1[b];[r][g][b]concat=n=3:v=1:a=0",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:v", "libx264", "-g", "80", "-c:a", "aac", source)
	original, err := os.ReadFile(source) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	taken := strings.TrimSuffix(source, ".mp4") + "-trimmed.mp4"
	if err := os.WriteFile(taken, []byte("existing recording"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name         string
		start, end   time.Duration
		remove, mute bool
		duration     time.Duration
		colours      []int
	}{
		{"accurate keep", 350 * time.Millisecond, 850 * time.Millisecond, false, false, 500 * time.Millisecond, []int{0}},
		{"remove middle", time.Second, 3 * time.Second, true, false, 2 * time.Second, []int{0, 2}},
		{"remove beginning", 0, time.Second, true, false, 3 * time.Second, []int{1, 2}},
		{"remove end", time.Second, 4 * time.Second, true, false, time.Second, []int{0}},
		{"silent", 350 * time.Millisecond, 850 * time.Millisecond, false, true, 500 * time.Millisecond, []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := source
			if tc.mute {
				input = filepath.Join(dir, "silent.mp4")
				trimTestCommand(t, "-i", source, "-an", "-c:v", "copy", input)
			}
			path, info, err := exportTrim(ctx, input, trimSelection{tc.start, tc.end, 4 * time.Second, tc.remove}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs((info.Duration-tc.duration).Seconds()) > 0.075 || info.Width != 160 || info.Height != 96 {
				t.Fatalf("unexpected output: %+v; wanted %s", info, tc.duration)
			}
			if !tc.mute && path == taken {
				t.Fatal("overwrote the existing output")
			}
			hasAudio, err := trimHasAudio(ctx, path)
			if err != nil || hasAudio == tc.mute {
				t.Fatalf("audio = %v, %v", hasAudio, err)
			}
			for i, colour := range tc.colours {
				at := 0.1
				if i > 0 {
					at = tc.duration.Seconds() - 0.1
				}
				pixel := trimTestCommand(t, "-ss", trimSeconds(time.Duration(at*float64(time.Second))), "-i", path,
					"-frames:v", "1", "-vf", "scale=1:1", "-pix_fmt", "rgb24", "-f", "rawvideo", "-")
				if len(pixel) != 3 || pixel[colour] < 200 {
					t.Fatalf("unexpected frame colour at %.2fs: %v; expected channel %d", at, pixel, colour)
				}
			}
			if hasAudio {
				audio := trimTestCommand(t, "-i", path, "-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "-")
				if math.Abs(float64(len(audio))/16000-tc.duration.Seconds()) > 0.08 {
					t.Fatalf("audio/video duration differs: %d bytes, video %s", len(audio), info.Duration)
				}
			}
		})
	}

	t.Run("cancel cleans partial output", func(t *testing.T) {
		before, err := filepath.Glob(filepath.Join(dir, "*-trimmed*.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		cancelCtx, stop := context.WithCancel(ctx)
		defer stop()
		_, _, err = exportTrim(cancelCtx, source, trimSelection{0, 4 * time.Second, 4 * time.Second, false}, func(float64) { stop() })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		after, _ := filepath.Glob(filepath.Join(dir, "*-trimmed*.mp4"))
		partial, _ := filepath.Glob(filepath.Join(dir, ".tequilashot-trim-*"))
		if !slices.Equal(before, after) || len(partial) != 0 {
			t.Fatalf("cancel left files: before %v, after %v, partial %v", before, after, partial)
		}
	})

	got, _ := os.ReadFile(source) //nolint:gosec
	if !slices.Equal(original, got) {
		t.Fatal("source recording changed")
	}
	got, _ = os.ReadFile(taken) //nolint:gosec
	if string(got) != "existing recording" {
		t.Fatal("existing output changed")
	}
}

func TestTrimPreservesAudioDelay(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	source := filepath.Join(t.TempDir(), "delayed.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "color=red:s=160x96:r=20:d=2",
		"-itsoffset", "0.3", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.7", "-c:v", "libx264", "-c:a", "aac", source)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path, _, err := exportTrim(ctx, source, trimSelection{0, time.Second, 2 * time.Second, false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pcm := trimTestCommand(t, "-i", path, "-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "-")
	energy := func(start, end int) float64 {
		t.Helper()
		if len(pcm) < end*2 {
			t.Fatalf("audio too short: %d bytes", len(pcm))
		}
		var sum float64
		for i := start; i < end; i++ {
			v := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) //nolint:gosec
			sum += v * v
		}
		return math.Sqrt(sum / float64(end-start))
	}
	if energy(0, 1600) > 10 || energy(3200, 5600) < 500 {
		t.Fatal("the original audio delay was lost")
	}
}
