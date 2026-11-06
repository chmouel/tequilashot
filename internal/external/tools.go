package external

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

// Grim captures a screenshot
func Grim(ctx context.Context, geometry, output, filename string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, prefs.Bin("grim"), grimArgs(prefs.Current(), geometry, output, filename)...) //nolint:gosec

	if filename == "" {
		return cmd.Output()
	}

	return nil, cmd.Run()
}

func grimArgs(p prefs.Prefs, geometry, output, filename string) []string {
	args := []string{"-t", "png"}
	if p.ScreenshotCursor {
		args = append(args, "-c")
	}

	if geometry != "" {
		args = append(args, "-g", geometry)
	}
	if output != "" {
		args = append(args, "-o", output)
	}

	if filename == "" {
		return append(args, "-")
	}
	return append(args, filename)
}

// Slurp performs interactive region selection; an empty color uses the
// preferred selection colour.
func Slurp(ctx context.Context, color string) (string, error) {
	if color == "" {
		color = prefs.Current().SelectionColour
	}
	args := []string{}
	if color != "" {
		args = append(args, "-c", color)
	}

	cmd := exec.CommandContext(ctx, prefs.Bin("slurp"), args...) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// WlCopy copies data to clipboard
func WlCopy(ctx context.Context, data []byte, mimeType string) error {
	cmd := exec.CommandContext(ctx, prefs.Bin("wl-copy"), "-t", mimeType) //nolint:gosec
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}

// WlCopyText copies text to clipboard
func WlCopyText(ctx context.Context, text string) error {
	return WlCopy(ctx, []byte(text), "text/plain")
}

// WlPaste pastes from clipboard
func WlPaste(ctx context.Context, mimeType string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, prefs.Bin("wl-paste"), "--type", mimeType) //nolint:gosec
	return cmd.Output()
}

// AudioSources selects which audio devices a recording captures.
type AudioSources struct {
	Desktop bool
	Mic     bool
	// MicDevice is a device id from gpu-screen-recorder --list-audio-devices;
	// empty selects the system default input.
	MicDevice string
}

// gsrSource returns the gpu-screen-recorder -a value, mixing both devices into one track.
func (a AudioSources) gsrSource() string {
	var sources []string
	if a.Desktop {
		sources = append(sources, "default_output")
	}
	if a.Mic {
		if a.MicDevice != "" {
			sources = append(sources, "device:"+a.MicDevice)
		} else {
			sources = append(sources, "default_input")
		}
	}
	return strings.Join(sources, "|")
}

// StartGpuScreenRecorder starts recording an output or a slurp-style
// "X,Y WxH" geometry straight to an MP4 file, optionally with a webcam overlay.
// The recorder's stderr is copied to stderr as well as to the daemon's own.
func StartGpuScreenRecorder(ctx context.Context, geometry, output, filename string, audio AudioSources, camera CameraOverlay, stderr io.Writer) (*exec.Cmd, error) {
	p := prefs.Current()
	bin := p.Program("gpu-screen-recorder")
	var outW, outH int
	if geometry == "" && output != "" {
		outW, outH = gsrOutputSize(ctx, bin, output)
	}

	args, err := recordingArgs(p, geometry, output, filename, outW, outH, audio, camera)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if stderr != nil {
		cmd.Stderr = io.MultiWriter(os.Stderr, stderr)
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return cmd, nil
}

// recordingArgs adds the recording-only preferences and extra arguments to gsrArgs.
func recordingArgs(p prefs.Prefs, geometry, output, filename string, outW, outH int, audio AudioSources, camera CameraOverlay) ([]string, error) {
	args, err := gsrArgs(p, geometry, output, filename, outW, outH, audio, camera)
	if err != nil {
		return nil, err
	}
	if p.VideoCodec != "" {
		args = append(args, "-k", p.VideoCodec)
	}
	if p.Quality != "" {
		args = append(args, "-q", p.Quality)
	}
	extra, err := prefs.RecorderArgs(p.RecordingArgs, false)
	if err != nil {
		return nil, fmt.Errorf("extra recording arguments: %w", err)
	}
	return append(args, extra...), nil
}

// gsrArgs builds the gpu-screen-recorder arguments from the recording
// preferences; outW and outH are the known size of the recorded output, or zero.
func gsrArgs(p prefs.Prefs, geometry, output, filename string, outW, outH int, audio AudioSources, camera CameraOverlay) ([]string, error) {
	if err := ValidateMicDevice(audio.MicDevice); err != nil {
		return nil, err
	}
	if camera.Enabled() {
		if err := camera.Validate(); err != nil {
			return nil, err
		}
	}

	args := []string{"-f", strconv.Itoa(p.FrameRate), "-c", "mp4", "-o", filename}

	var base string
	var x, y int
	w, h := outW, outH
	switch {
	case geometry != "":
		if _, err := fmt.Sscanf(geometry, "%d,%d %dx%d", &x, &y, &w, &h); err != nil {
			return nil, fmt.Errorf("invalid geometry %q: %w", geometry, err)
		}
		base = "region"
	case output != "":
		base = output
	default:
		return nil, fmt.Errorf("no geometry or output to record")
	}

	source := base
	if camera.Enabled() && !camera.Round() {
		source += "|" + camera.gsrSource()
	}
	args = append(args, "-w", source)
	if geometry != "" {
		args = append(args, "-region", fmt.Sprintf("%dx%d+%d+%d", w, h, x, y))
	}

	// Smaller sources are left alone because -s would upscale them.
	if p.MaxWidth > 0 && w > p.MaxWidth {
		args = append(args, "-s", fmt.Sprintf("%dx%d", p.MaxWidth, h))
	}
	if !p.RecordCursor {
		args = append(args, "-cursor", "no")
	}
	if p.Encoder != "" {
		args = append(args, "-encoder", p.Encoder)
	}

	if src := audio.gsrSource(); src != "" {
		args = append(args, "-a", src)
	}

	return args, nil
}

// gsrOutputSize returns the size gpu-screen-recorder reports for a monitor, or zeros if unknown.
func gsrOutputSize(ctx context.Context, bin, output string) (int, int) {
	out, err := exec.CommandContext(ctx, bin, "--list-capture-options").Output() //nolint:gosec
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		name, size, ok := strings.Cut(line, "|")
		if !ok || name != output {
			continue
		}
		var w, h int
		if _, err := fmt.Sscanf(size, "%dx%d", &w, &h); err == nil {
			return w, h
		}
	}
	return 0, 0
}

// Satty opens the satty image editor
func Satty(ctx context.Context, inputFile, outputFile string, earlyExit bool) error {
	args := []string{
		"--filename", inputFile,
		"--output-filename", outputFile,
	}
	if earlyExit {
		args = append(args, "--early-exit")
	}

	cmd := exec.CommandContext(ctx, prefs.Bin("satty"), args...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Zenity shows a text entry dialog
func Zenity(ctx context.Context, text, entryText string) (string, error) {
	args := []string{
		"--entry",
		"--text", text,
		"--entry-text", entryText,
	}

	cmd := exec.CommandContext(ctx, prefs.Bin("zenity"), args...) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// AIChat uses aichat to generate a filename
func AIChat(ctx context.Context, model, imagePath, prompt string) (string, error) {
	args := []string{
		"--model", model,
		"--file", imagePath,
		prompt,
	}

	cmd := exec.CommandContext(ctx, prefs.Bin("aichat"), args...) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// Wofi shows a selection menu
func Wofi(ctx context.Context, prompt string, options []string) (string, error) {
	args := []string{
		"--dmenu",
		"--prompt", prompt,
	}

	cmd := exec.CommandContext(ctx, prefs.Bin("wofi"), args...) //nolint:gosec
	cmd.Stdin = strings.NewReader(strings.Join(options, "\n"))

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// Nautilus opens a file in nautilus
func Nautilus(ctx context.Context, fileURI string) error {
	cmd := exec.CommandContext(ctx, prefs.Bin("nautilus"), fileURI) //nolint:gosec
	return cmd.Start()
}

// CleanupOldFiles removes regular files directly inside directory whose
// modification time is older than olderThan. Hidden files, symlinks and
// subdirectories are left alone.
func CleanupOldFiles(ctx context.Context, directory string, olderThan time.Duration) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	cutoff := time.Now().Add(-olderThan)
	var errs []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		log.Printf("Removed old file %s", path)
	}
	return errors.Join(errs...)
}
