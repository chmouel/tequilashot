package denoise

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
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

// installedModel returns a usable model, or skips: tests never download it.
func installedModel(t *testing.T) string {
	t.Helper()
	model := prefs.DefaultDenoiseModelPath()
	if _, err := os.Stat(model); err != nil {
		t.Skip("no noise-removal model at " + model)
	}
	return model
}

func TestFilterSurvivesAwkwardPaths(t *testing.T) {
	needTools(t)
	data, err := os.ReadFile(installedModel(t))
	if err != nil {
		t.Fatal(err)
	}
	const awkward = `a:b,c'd [x];e\f=g.rnnn`
	model := filepath.Join(t.TempDir(), awkward)
	if err := os.WriteFile(model, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-af", Filter(model)},
		{"-filter_complex", "[0:a]" + Filter(model) + "[out]", "-map", "[out]"},
	} {
		ffmpeg(t, append(append([]string{"-f", "lavfi", "-i", "sine=duration=0.2"}, args...), "-f", "null", "-")...)
	}
}

// arnndn's NaN tail is random, so several runs are needed to catch it.
func TestFilterEndsWithoutNaN(t *testing.T) {
	needTools(t)
	model := installedModel(t)
	for range 6 {
		cmd := exec.Command("ffmpeg", "-hide_banner", "-nostdin", "-f", "lavfi", "-i", "anoisesrc=d=1.2037:r=44100", //nolint:gosec
			"-af", Filter(model)+",astats=measure_perchannel=none:measure_overall=Number_of_NaNs", "-f", "null", "-")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v: %s", err, out)
		}
		if !strings.Contains(string(out), "Number of NaNs: 0.000000") {
			t.Fatalf("filter produced NaN samples:\n%s", out)
		}
	}
}

func TestExportKeepsOtherStreams(t *testing.T) {
	needTools(t)
	t.Setenv(prefs.EnvDenoiseModel, installedModel(t))
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
	dir := t.TempDir()
	model := filepath.Join(dir, "model.rnnn")
	if err := os.WriteFile(model, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(prefs.EnvDenoiseModel, model)
	if err := Ready(context.Background()); err != nil {
		t.Skip(err.Error())
	}
	video := filepath.Join(dir, "silent.mp4")
	ffmpeg(t, "-f", "lavfi", "-i", "color=red:s=64x64:r=10:d=1", "-c:v", "libx264", video)
	if _, err := Export(context.Background(), video, "", nil); !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want ErrNoAudio", err)
	}
}

func TestExportKeepsPictureAndOriginal(t *testing.T) {
	needTools(t)
	t.Setenv(prefs.EnvDenoiseModel, installedModel(t))
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

const testRate = 48000

var testClickTimes = []float64{2.0, 2.75, 3.5, 4.25, 5.0}

// testSpeech is a harmonic stack with syllabic amplitude modulation, peaking
// at 0.5.
func testSpeech() []float64 {
	out := make([]float64, 6*testRate)
	peak := 0.0
	for i := range out {
		t := float64(i) / testRate
		var v float64
		for k := 1; k <= 8; k++ {
			v += math.Sin(2*math.Pi*float64(k)*(140*t+0.3*math.Sin(2*math.Pi*0.7*t))) / float64(k)
		}
		out[i] = v * (0.5 + 0.5*math.Pow(math.Sin(2*math.Pi*2.7*t), 2))
		peak = math.Max(peak, math.Abs(out[i]))
	}
	for i := range out {
		out[i] *= 0.5 / peak
	}
	return out
}

func withKeyboardClicks(speech []float64) []float64 {
	out := append([]float64(nil), speech...)
	seed := uint64(12345)
	const length = testRate * 6 / 1000
	for _, at := range testClickTimes {
		start := int(at * testRate)
		for i := range length {
			seed = (1103515245*seed + 12345) % (1 << 31)
			noise := float64(seed)/float64(uint64(1)<<31)*2 - 1
			out[start+i] += 0.6 * math.Exp(-float64(i)/(0.0015*testRate)) * noise
		}
	}
	return out
}

// renderChain runs samples through af, exchanging raw 48 kHz mono samples so
// the tests need no container.
func renderChain(t *testing.T, af string, samples []float64) []float64 {
	t.Helper()
	raw := make([]byte, 4*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(float32(s)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-v", "error", "-f", "f32le", //nolint:gosec
		"-ar", strconv.Itoa(testRate), "-ac", "1", "-i", "pipe:0", "-af", af, "-f", "f32le", "pipe:1")
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, stderr.String())
	}
	if len(out)%4 != 0 {
		t.Fatalf("ffmpeg returned %d bytes, not whole samples", len(out))
	}
	got := make([]float64, len(out)/4)
	for i := range got {
		got[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(out[4*i:])))
		if math.IsNaN(got[i]) {
			t.Fatalf("filter produced a NaN sample at %d", i)
		}
	}
	return got
}

// clickResidual measures what survives of a burst: the difference between the
// same signal with and without its clicks, around one click time.
func clickResidual(noisy, clean []float64, at float64) float64 {
	start := int(at*testRate) - testRate/100
	end := start + 3*testRate/100
	var sum float64
	for i := start; i < end; i++ {
		sum += (noisy[i] - clean[i]) * (noisy[i] - clean[i])
	}
	return math.Sqrt(sum / float64(end-start))
}

func TestFilterAttenuatesKeyboardClicks(t *testing.T) {
	needTools(t)
	model := installedModel(t)
	speech := testSpeech()
	clicky := withKeyboardClicks(speech)
	arnndnOnly := strings.TrimSuffix(Filter(model), ","+clickFilter)

	cleanOut := renderChain(t, Filter(model), speech)
	clickyOut := renderChain(t, Filter(model), clicky)
	cleanBase := renderChain(t, arnndnOnly, speech)
	clickyBase := renderChain(t, arnndnOnly, clicky)
	if len(clickyOut) != len(clicky) {
		t.Fatalf("filtered sound is %d samples, want the %d it was given", len(clickyOut), len(clicky))
	}

	var withFilter, withoutFilter float64
	for _, at := range testClickTimes {
		withFilter += clickResidual(clickyOut, cleanOut, at)
		withoutFilter += clickResidual(clickyBase, cleanBase, at)
	}
	if withFilter >= 0.8*withoutFilter {
		t.Fatalf("clicks left %0.5f of residual against %0.5f for arnndn alone, want a fifth less", withFilter, withoutFilter)
	}
}

func TestFilterPreservesCleanSpeech(t *testing.T) {
	needTools(t)
	model := installedModel(t)
	speech := testSpeech()
	arnndnOnly := strings.TrimSuffix(Filter(model), ","+clickFilter)

	before := renderChain(t, arnndnOnly, speech)
	after := renderChain(t, Filter(model), speech)
	if len(after) != len(before) {
		t.Fatalf("filtered sound is %d samples, want the %d arnndn alone gives", len(after), len(before))
	}
	var diff, ref float64
	for i := range after {
		diff += (after[i] - before[i]) * (after[i] - before[i])
		ref += before[i] * before[i]
	}
	if rel := math.Sqrt(diff / ref); rel > 0.1 {
		t.Fatalf("click removal moved the clean sound by %0.3f of its level, want under 0.1", rel)
	}
}

func TestFilterWithClickReducerRunsInBothGraphs(t *testing.T) {
	needTools(t)
	model := installedModel(t)
	p := prefs.Current().Clone()
	p.ReduceClicks = true
	prefs.Set(p)
	t.Cleanup(func() { prefs.Set(prefs.Default()) })
	for _, args := range [][]string{
		{"-af", Filter(model)},
		{"-filter_complex", "[0:a]" + Filter(model) + "[out]", "-map", "[out]"},
	} {
		ffmpeg(t, append(append([]string{"-f", "lavfi", "-i", "sine=duration=0.5"}, args...), "-f", "null", "-")...)
	}
}

func TestExportHandlesReadOnlySource(t *testing.T) {
	needTools(t)
	t.Setenv(prefs.EnvDenoiseModel, installedModel(t))
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
