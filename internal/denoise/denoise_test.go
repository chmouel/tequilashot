package denoise

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
}

const ffmpegTimeout = time.Minute

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", append([]string{"-hide_banner", "-v", "error", "-y"}, args...)...) //nolint:gosec
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
}

// needModel skips unless noise removal can run: tests never download the
// model, which TEQUILASHOT_DENOISE_MODEL can point at.
func needModel(t *testing.T) {
	t.Helper()
	if err := Ready(context.Background()); err != nil {
		t.Skip(err.Error())
	}
}

func TestExportKeepsOtherStreams(t *testing.T) {
	needTools(t)
	needModel(t)
	dir := t.TempDir()
	subs := filepath.Join(dir, "subs.srt")
	if err := os.WriteFile(subs, []byte("1\n00:00:00,000 --> 00:00:01,000\nhello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chapters := filepath.Join(dir, "chapters.txt")
	if err := os.WriteFile(chapters, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "clip.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=s=160x96:r=10:d=2", "-f", "lavfi", "-i", "anoisesrc=d=2:a=0.2",
		"-i", subs, "-i", chapters, "-map", "0:v", "-map", "1:a", "-map", "2:s", "-map_chapters", "3",
		"-c:v", "libx264", "-c:a", "aac", "-c:s", "srt", "-t", "2", source)

	out, err := Export(context.Background(), source, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	streams, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", out).Output() //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if got := string(streams); got != "video\naudio\nsubtitle\n" {
		t.Fatalf("streams = %q, want the picture, sound and subtitle", got)
	}
	chaptersOut, err := exec.Command("ffprobe", "-v", "error", "-show_chapters", "-of", "csv=p=0", out).Output() //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chaptersOut), "one") {
		t.Fatalf("chapters = %q, want the source's chapter", chaptersOut)
	}
}

func TestExportRefusesSilentRecording(t *testing.T) {
	needTools(t)
	needModel(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "silent.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "color=red:s=64x64:r=10:d=1", "-c:v", "libx264", video)
	if _, err := Export(context.Background(), video, "", nil); !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want ErrNoAudio", err)
	}
}

func TestExportKeepsPictureAndOriginal(t *testing.T) {
	needTools(t)
	needModel(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "clip.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=s=160x96:r=10:d=2", "-f", "lavfi", "-i", "anoisesrc=d=2:a=0.2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", source)
	if err := os.Chmod(source, 0o640); err != nil { //nolint:gosec // a readable mode distinct from CreateTemp's 0600
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clip-denoised.mp4"), []byte("taken"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := Export(context.Background(), source, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "clip-denoised-2.mp4"); out != want {
		t.Fatalf("output = %s, want %s", out, want)
	}
	if st, err := os.Stat(out); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm() != 0o640 {
		t.Fatalf("output mode = %v, want the source's -rw-r-----", st.Mode())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "clip-denoised.mp4")); string(b) != "taken" { //nolint:gosec
		t.Fatal("existing file was overwritten")
	}
	streams, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name", "-of", "csv=p=0", out).Output() //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if got := string(streams); got != "h264,video\naac,audio\n" {
		t.Fatalf("streams = %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatalf("unexpected files left: %v", entries)
	}
}

func TestExportHandlesReadOnlySource(t *testing.T) {
	needTools(t)
	needModel(t)
	source := filepath.Join(t.TempDir(), "clip.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=s=160x96:r=10:d=1", "-f", "lavfi", "-i", "anoisesrc=d=1:a=0.2",
		"-c:v", "libx264", "-c:a", "aac", "-t", "1", source)
	if err := os.Chmod(source, 0o444); err != nil { //nolint:gosec // a deliberately read-only source
		t.Fatal(err)
	}
	out, err := Export(context.Background(), source, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(out); err != nil || st.Mode().Perm() != 0o444 {
		t.Fatalf("output %s: %v, %v; want mode 0444", out, st, err)
	}
}

func TestExportKeepsSoundTimingAndFlags(t *testing.T) {
	needTools(t)
	needModel(t)
	source := filepath.Join(t.TempDir(), "clip.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=s=160x96:r=10:d=2", "-itsoffset", "0.3", "-f", "lavfi", "-i", "anoisesrc=d=1.5:a=0.2",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-c:a", "aac",
		"-metadata:s:a:0", "language=fra", "-disposition:a:0", "default+visual_impaired", source)

	out, err := Export(context.Background(), source, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	describe := func(file string) (float64, string) {
		t.Helper()
		got, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", //nolint:gosec
			"-show_entries", "stream=start_time:stream_disposition=visual_impaired:stream_tags=language",
			"-of", "default=nw=1", file).Output()
		if err != nil {
			t.Fatal(err)
		}
		var start float64
		for line := range strings.Lines(string(got)) {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "start_time="); ok {
				start, _ = strconv.ParseFloat(v, 64)
			}
		}
		return start, string(got)
	}
	wantStart, _ := describe(source)
	start, flags := describe(out)
	// Encoding again adds one AAC priming frame ahead of the same sound.
	if math.Abs(start-wantStart) > 0.03 || wantStart < 0.25 {
		t.Fatalf("sound starts at %.3fs, want %.3fs as in the source", start, wantStart)
	}
	if !strings.Contains(flags, "DISPOSITION:visual_impaired=1") || !strings.Contains(flags, "TAG:language=fra") {
		t.Fatalf("sound track = %q, want its language and visual_impaired flag kept", flags)
	}
}
