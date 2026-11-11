package gui

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/denoise"
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
			path, info, err := exportTrim(ctx, input, false, trimSelection{tc.start, tc.end, 4 * time.Second, tc.remove}, nil)
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
		_, _, err = exportTrim(cancelCtx, source, false, trimSelection{0, 4 * time.Second, 4 * time.Second, false}, func(float64) { stop() })
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
	path, _, err := exportTrim(ctx, source, false, trimSelection{0, time.Second, 2 * time.Second, false}, nil)
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

func trimStreamDurations(t *testing.T, file string) map[string]time.Duration {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,duration", "-of", "json", file).Output() //nolint:gosec
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var data struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatal(err)
	}
	durations := make(map[string]time.Duration, len(data.Streams))
	for _, stream := range data.Streams {
		if stream.Duration == "" || stream.Duration == "N/A" {
			continue
		}
		value, err := time.ParseDuration(stream.Duration + "s")
		if err != nil {
			t.Fatal(err)
		}
		durations[stream.Type] = value
	}
	return durations
}

func trimLastFrameDuration(t *testing.T, file string) time.Duration {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "frame=duration_time", "-of", "csv=p=0", file).Output() //nolint:gosec
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		value, _, _ := strings.Cut(strings.TrimSpace(lines[i]), ",")
		if value == "" || value == "N/A" {
			continue
		}
		duration, err := time.ParseDuration(value + "s")
		if err == nil {
			return duration
		}
	}
	t.Fatal("no frame duration was reported")
	return 0
}

func TestExportTrimSnapsToFrames(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	source := filepath.Join(t.TempDir(), "unaligned.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "color=red:s=160x96:r=20:d=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:v", "libx264", "-g", "80", "-c:a", "aac", source)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path, _, err := exportTrim(ctx, source, false, trimSelection{375 * time.Millisecond, 850 * time.Millisecond, 4 * time.Second, false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	durations := trimStreamDurations(t, path)
	video, audio := durations["video"], durations["audio"]
	const want = 450 * time.Millisecond
	if video == 0 || audio == 0 {
		t.Fatalf("missing stream durations: %v", durations)
	}
	if math.Abs((video - want).Seconds()) > 0.02 {
		t.Fatalf("video lasts %s; expected the frames from 0.400s for %s", video, want)
	}
	if math.Abs((audio - video).Seconds()) > 0.01 {
		t.Fatalf("video lasts %s but audio lasts %s; the streams do not share a start", video, audio)
	}
}

func TestExportTrimSparseFrameDuration(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	source := filepath.Join(t.TempDir(), "sparse.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "nullsrc=s=160x96:r=60:d=2,select='not(mod(n,2))*not(mod(n,5))'",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:v", "libx264", "-crf", "18", "-g", "12", "-bf", "0", "-fps_mode", "vfr", "-c:a", "aac", source)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	info, err := ProbeMedia(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := probeFrameClock(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	window, err := loadFrameWindow(ctx, source, clock, 0, info.Duration)
	if err != nil {
		t.Fatal(err)
	}
	var start, requestedEnd, snappedEnd time.Duration
	for i := 4; i < len(window.times); i++ {
		if gap := window.times[i] - window.times[i-1]; gap >= 60*time.Millisecond {
			start = window.times[i-1]
			requestedEnd = start + gap/2
			snappedEnd = window.times[i]
			break
		}
	}
	if snappedEnd == 0 {
		t.Fatal("the sparse fixture has no gap to trim across")
	}
	path, _, err := exportTrim(ctx, source, false, trimSelection{start, requestedEnd, info.Duration, false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	span := snappedEnd - start
	durations := trimStreamDurations(t, path)
	video, audio := durations["video"], durations["audio"]
	if math.Abs((video - span).Seconds()) > 0.02 {
		t.Fatalf("video lasts %s; expected the single held frame for %s", video, span)
	}
	if math.Abs((audio - video).Seconds()) > 0.01 {
		t.Fatalf("video lasts %s but audio lasts %s at the frame-snapped end", video, audio)
	}
	if last := trimLastFrameDuration(t, path); math.Abs((last - span).Seconds()) > 0.02 {
		t.Fatalf("the last frame is displayed for %s; expected %s", last, span)
	}
}

func TestExportTrimDelayedPicture(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	source := filepath.Join(t.TempDir(), "delayed.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-itsoffset", "0.3", "-f", "lavfi", "-i", "color=red:s=160x96:r=20:d=4",
		"-map", "1:v", "-map", "0:a", "-c:v", "libx264", "-g", "80", "-c:a", "aac", source)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path, _, err := exportTrim(ctx, source, false, trimSelection{0, 2 * time.Second, 4 * time.Second, false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	durations := trimStreamDurations(t, path)
	video, audio := durations["video"], durations["audio"]
	const want = 1700 * time.Millisecond
	if math.Abs((video - want).Seconds()) > 0.03 {
		t.Fatalf("video lasts %s; expected the pictures from 0.300s for %s", video, want)
	}
	if math.Abs((audio - video).Seconds()) > 0.02 {
		t.Fatalf("video lasts %s but audio lasts %s; the delayed picture was not held to the sound", video, audio)
	}

	// Cutting a middle section keeps a prefix whose own start is implicit, and
	// that prefix must be held together just as the selection is.
	path, _, err = exportTrim(ctx, source, false, trimSelection{time.Second, 2 * time.Second, 4 * time.Second, true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	durations = trimStreamDurations(t, path)
	video, audio = durations["video"], durations["audio"]
	if math.Abs((video - 2700*time.Millisecond).Seconds()) > 0.03 {
		t.Fatalf("cut video lasts %s; expected the retained pictures for 2.7s", video)
	}
	if math.Abs((audio - video).Seconds()) > 0.02 {
		t.Fatalf("cut video lasts %s but its audio lasts %s; the retained prefix lost its sync", video, audio)
	}

	// Removing everything the picture covers leaves only the tail, as the
	// retained beginning holds no frame at all.
	path, _, err = exportTrim(ctx, source, false, trimSelection{100 * time.Millisecond, 2 * time.Second, 4 * time.Second, true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	durations = trimStreamDurations(t, path)
	video, audio = durations["video"], durations["audio"]
	if math.Abs((video - 2*time.Second).Seconds()) > 0.03 {
		t.Fatalf("tail video lasts %s; expected 2s with no frameless prefix", video)
	}
	if math.Abs((audio - video).Seconds()) > 0.02 {
		t.Fatalf("tail video lasts %s but its audio lasts %s", video, audio)
	}
}

func TestExportTrimCollapsedToFrame(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	source := filepath.Join(t.TempDir(), "narrow.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "color=red:s=160x96:r=20:d=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:v", "libx264", "-g", "80", "-c:a", "aac", source)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, remove := range []bool{false, true} {
		selection := trimSelection{375 * time.Millisecond, 390 * time.Millisecond, 4 * time.Second, remove}
		if _, _, err := exportTrim(ctx, source, false, selection, nil); err == nil {
			t.Fatalf("remove=%v: a selection shorter than a frame was exported", remove)
		}
	}
}

func TestExportTrimRemovesNoise(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	if err := denoise.Ready(context.Background()); err != nil {
		t.Skip(err.Error())
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "noisy.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "color=red:s=160x96:r=20:d=4", "-f", "lavfi", "-i", "anoisesrc=d=4:a=0.3",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", source)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	path, _, err := exportTrim(ctx, source, true, trimSelection{time.Second, 2 * time.Second, 4 * time.Second, true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "noisy-trimmed.mp4" {
		t.Fatalf("cut saved as %s", path)
	}
	durations := trimStreamDurations(t, path)
	if math.Abs((durations["audio"] - durations["video"]).Seconds()) > 0.05 {
		t.Fatalf("video lasts %s but audio lasts %s", durations["video"], durations["audio"])
	}
	if before, after := trimMeanVolume(t, source), trimMeanVolume(t, path); after > before-6 {
		t.Fatalf("mean volume %.1f dB after cleaning, %.1f dB before; the noise was not removed", after, before)
	}

	path, _, err = exportTrim(ctx, source, true, trimSelection{0, 4 * time.Second, 4 * time.Second, false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "noisy-denoised.mp4" {
		t.Fatalf("whole recording saved as %s", path)
	}
}

func trimMeanVolume(t *testing.T, file string) float64 {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-i", file, "-af", "volumedetect", "-f", "null", "-").CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("volumedetect: %v: %s", err, out)
	}
	_, rest, ok := strings.Cut(string(out), "mean_volume: ")
	if !ok {
		t.Fatalf("no mean volume in %s", out)
	}
	value, _, _ := strings.Cut(rest, " dB")
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTrimKeysFollowTheKeptSegments(t *testing.T) {
	s := time.Second
	keys := []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 2500 * time.Millisecond, 3500 * time.Millisecond}
	got := trimKeys(keys, []trimSegment{{0, s}, {3 * s, 4 * s}})
	want := []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}
	if !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}
