package external

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Grim captures a screenshot
func Grim(ctx context.Context, geometry, output, filename string) ([]byte, error) {
	args := []string{"-t", "png"}

	if geometry != "" {
		args = append(args, "-g", geometry)
	}
	if output != "" {
		args = append(args, "-o", output)
	}

	if filename == "" {
		args = append(args, "-")
	} else {
		args = append(args, filename)
	}

	cmd := exec.CommandContext(ctx, "grim", args...)

	if filename == "" {
		return cmd.Output()
	}

	return nil, cmd.Run()
}

// Slurp performs interactive region selection
func Slurp(ctx context.Context, color string) (string, error) {
	args := []string{}
	if color != "" {
		args = append(args, "-c", color)
	}

	cmd := exec.CommandContext(ctx, "slurp", args...) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// WlCopy copies data to clipboard
func WlCopy(ctx context.Context, data []byte, mimeType string) error {
	cmd := exec.CommandContext(ctx, "wl-copy", "-t", mimeType) //nolint:gosec
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}

// WlCopyText copies text to clipboard
func WlCopyText(ctx context.Context, text string) error {
	return WlCopy(ctx, []byte(text), "text/plain")
}

// WlPaste pastes from clipboard
func WlPaste(ctx context.Context, mimeType string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "wl-paste", "--type", mimeType) //nolint:gosec
	return cmd.Output()
}

// maxRecordingWidth caps the video width; taller content keeps its height
// ratio. Smaller sources are left alone because -s would upscale them.
const maxRecordingWidth = 1920

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
	var outW, outH int
	if geometry == "" && output != "" {
		outW, outH = gsrOutputSize(ctx, output)
	}

	args, err := gsrArgs(geometry, output, filename, outW, outH, audio, camera)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "gpu-screen-recorder", args...) //nolint:gosec
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

// gsrArgs builds the gpu-screen-recorder arguments; outW and outH are the
// known size of the recorded output, or zero.
func gsrArgs(geometry, output, filename string, outW, outH int, audio AudioSources, camera CameraOverlay) ([]string, error) {
	if err := ValidateMicDevice(audio.MicDevice); err != nil {
		return nil, err
	}
	if camera.Enabled() {
		if err := camera.Validate(); err != nil {
			return nil, err
		}
	}

	args := []string{"-f", "60", "-c", "mp4", "-o", filename}

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
	if camera.Enabled() {
		source += "|" + camera.gsrSource()
	}
	args = append(args, "-w", source)
	if geometry != "" {
		args = append(args, "-region", fmt.Sprintf("%dx%d+%d+%d", w, h, x, y))
	}

	if w > maxRecordingWidth {
		args = append(args, "-s", fmt.Sprintf("%dx%d", maxRecordingWidth, h))
	}

	if src := audio.gsrSource(); src != "" {
		args = append(args, "-a", src)
	}

	return args, nil
}

// gsrOutputSize returns the size gpu-screen-recorder reports for a monitor, or zeros if unknown.
func gsrOutputSize(ctx context.Context, output string) (int, int) {
	out, err := exec.CommandContext(ctx, "gpu-screen-recorder", "--list-capture-options").Output() //nolint:gosec
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

	cmd := exec.CommandContext(ctx, "satty", args...) //nolint:gosec
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

	cmd := exec.CommandContext(ctx, "zenity", args...) //nolint:gosec
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

	cmd := exec.CommandContext(ctx, "aichat", args...) //nolint:gosec
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

	cmd := exec.CommandContext(ctx, "wofi", args...) //nolint:gosec
	cmd.Stdin = strings.NewReader(strings.Join(options, "\n"))

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// Nautilus opens a file in nautilus
func Nautilus(ctx context.Context, fileURI string) error {
	cmd := exec.CommandContext(ctx, "nautilus", fileURI) //nolint:gosec
	return cmd.Start()
}

// CleanupOldFiles removes files older than the specified duration
func CleanupOldFiles(ctx context.Context, directory string, olderThan time.Duration) error {
	beforeTime := fmt.Sprintf("%dd", int(olderThan.Hours()/24))

	cmd := exec.CommandContext(
		ctx, "fd", //nolint:gosec
		"-t", "f",
		"--full-path", directory,
		"--changed-before", beforeTime,
		"--exec-batch", "rm", "-vf",
	)

	return cmd.Run()
}
