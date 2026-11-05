package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MediaInfo describes a saved recording or screenshot
type MediaInfo struct {
	Duration      time.Duration
	Width, Height int
	Size          int64
}

// ProbeImage reads PNG dimensions and file size without external tools
func ProbeImage(file string) (MediaInfo, error) {
	f, err := os.Open(file) //nolint:gosec
	if err != nil {
		return MediaInfo{}, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return MediaInfo{}, err
	}
	info := MediaInfo{Size: st.Size()}
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return info, err
	}
	info.Width, info.Height = cfg.Width, cfg.Height
	return info, nil
}

type probeOutput struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// ParseProbe reads the output of ffprobe -of json; size is left for the caller.
func ParseProbe(data []byte) (MediaInfo, error) {
	var p probeOutput
	if err := json.Unmarshal(data, &p); err != nil {
		return MediaInfo{}, fmt.Errorf("parsing ffprobe output: %w", err)
	}
	var info MediaInfo
	if len(p.Streams) > 0 {
		info.Width, info.Height = p.Streams[0].Width, p.Streams[0].Height
	}
	if p.Format.Duration != "" {
		secs, err := strconv.ParseFloat(p.Format.Duration, 64)
		if err != nil || secs < 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
			return MediaInfo{}, fmt.Errorf("invalid duration %q", p.Format.Duration)
		}
		info.Duration = time.Duration(secs * float64(time.Second))
	}
	return info, nil
}

// FormatDuration renders d as m:ss, or h:mm:ss from one hour onwards.
func FormatDuration(d time.Duration) string {
	total := int(d.Round(time.Second) / time.Second)
	if total < 0 {
		total = 0
	}
	h, m, s := total/3600, total%3600/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// FormatSize renders a byte count using binary units.
func FormatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		f /= unit
		if f < unit || u == "TB" {
			if f < 10 {
				return fmt.Sprintf("%.1f %s", f, u)
			}
			return fmt.Sprintf("%.0f %s", f, u)
		}
	}
	return ""
}

// Caption summarises the clip as "duration · width×height · size", omitting unknown parts.
func (m MediaInfo) Caption() string {
	var parts []string
	if m.Duration > 0 {
		parts = append(parts, FormatDuration(m.Duration))
	}
	if m.Width > 0 && m.Height > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", m.Width, m.Height))
	}
	parts = append(parts, FormatSize(m.Size))
	return strings.Join(parts, " · ")
}

// ProbeMedia inspects file with ffprobe and os.Stat.
func ProbeMedia(ctx context.Context, file string) (MediaInfo, error) {
	st, err := os.Stat(file)
	if err != nil {
		return MediaInfo{}, err
	}
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", //nolint:gosec
		"format=duration:stream=width,height", "-select_streams", "v:0", "-of", "json", file).Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("ffprobe: %w", err)
	}
	info, err := ParseProbe(out)
	if err != nil {
		return MediaInfo{}, err
	}
	info.Size = st.Size()
	return info, nil
}

// FrameOffset picks the seek point for the preview: one second in, or the
// midpoint of a shorter clip.
func FrameOffset(d time.Duration) time.Duration {
	return min(time.Second, d/2)
}

// ExtractFrame returns a JPEG frame from file, falling back to the first
// frame when seeking yields nothing (very short clips).
func ExtractFrame(ctx context.Context, file string, d time.Duration) ([]byte, error) {
	for _, at := range []time.Duration{FrameOffset(d), 0} {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64), //nolint:gosec
			"-i", file, "-frames:v", "1", "-vf", "scale=640:-2", "-f", "image2pipe", "-c:v", "mjpeg", "-")
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			return out, nil
		}
		if at == 0 {
			if err == nil {
				err = errors.New("no frame produced")
			}
			return nil, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	return nil, errors.New("unreachable")
}

// RenameTarget validates a new base name and returns the resulting path,
// always carrying the original extension and never an existing file.
func RenameTarget(oldPath, name string) (string, error) {
	ext := filepath.Ext(oldPath)
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), ext))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return "", errors.New("that is not a suitable file name")
	}
	target := filepath.Join(filepath.Dir(oldPath), name+ext)
	if target == oldPath {
		return oldPath, nil
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("%s already exists", name+ext)
	}
	return target, nil
}
