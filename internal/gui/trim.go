package gui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type trimSegment struct {
	start, end time.Duration
}

type trimSelection struct {
	start, end, duration time.Duration
	remove               bool
}

func (s trimSelection) segments() ([]trimSegment, error) {
	if s.duration <= 0 || s.start < 0 || s.end > s.duration || s.start >= s.end {
		return nil, errors.New("choose a start time before the end time, within the recording")
	}
	if !s.remove {
		return []trimSegment{{s.start, s.end}}, nil
	}
	var kept []trimSegment
	if s.start > 0 {
		kept = append(kept, trimSegment{0, s.start})
	}
	if s.end < s.duration {
		kept = append(kept, trimSegment{s.end, s.duration})
	}
	if len(kept) == 0 {
		return nil, errors.New("leave some footage to save")
	}
	return kept, nil
}

func trimLength(segments []trimSegment) time.Duration {
	var d time.Duration
	for _, segment := range segments {
		d += segment.end - segment.start
	}
	return d
}

func trimSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 6, 64)
}

func trimFilters(segments []trimSegment, audio bool) string {
	var filters, inputs []string
	for i, segment := range segments {
		span := "start=" + trimSeconds(segment.start) + ":end=" + trimSeconds(segment.end)
		filters = append(filters, fmt.Sprintf("[0:v:0]trim=%s,setpts=PTS-STARTPTS[v%d]", span, i))
		inputs = append(inputs, fmt.Sprintf("[v%d]", i))
		if audio {
			// Keep audio on the requested timeline, including an initial delay or a short track
			filters = append(filters, fmt.Sprintf("[0:a:0]atrim=%s,asetpts=PTS-%s/TB,aresample=async=1:first_pts=0,apad=whole_dur=%s,atrim=duration=%s[a%d]",
				span, trimSeconds(segment.start), trimSeconds(segment.end-segment.start), trimSeconds(segment.end-segment.start), i))
			inputs = append(inputs, fmt.Sprintf("[a%d]", i))
		}
	}
	a := 0
	outputs := "[video]"
	if audio {
		a = 1
		outputs += "[audio]"
	}
	filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=1:a=%d%s", strings.Join(inputs, ""), len(segments), a, outputs))
	return strings.Join(filters, ";")
}

func trimHasAudio(ctx context.Context, file string) (bool, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", //nolint:gosec
		"-show_entries", "stream=index", "-of", "json", file).Output()
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("inspect audio: %w", err)
	}
	var probe struct {
		Streams []json.RawMessage `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return false, err
	}
	return len(probe.Streams) > 0, nil
}

func exportTrim(ctx context.Context, source string, selection trimSelection, progress func(float64)) (string, MediaInfo, error) {
	segments, err := selection.segments()
	if err != nil {
		return "", MediaInfo{}, err
	}
	audio, err := trimHasAudio(ctx, source)
	if err != nil {
		return "", MediaInfo{}, err
	}
	f, err := os.CreateTemp(filepath.Dir(source), ".tequilashot-trim-*.mp4")
	if err != nil {
		return "", MediaInfo{}, err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Close(); err != nil {
		return "", MediaInfo{}, err
	}
	args := []string{
		"-hide_banner", "-v", "error", "-nostdin", "-y", "-copyts", "-start_at_zero", "-i", source,
		"-filter_complex", trimFilters(segments, audio), "-map", "[video]", "-c:v", "libx264", "-preset", "fast", "-crf", "18", "-pix_fmt", "yuv420p",
	}
	if audio {
		args = append(args, "-map", "[audio]", "-c:a", "aac", "-b:a", "192k")
	}
	args = append(args, "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", tmp)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", MediaInfo{}, err
	}
	if err := cmd.Start(); err != nil {
		return "", MediaInfo{}, fmt.Errorf("start FFmpeg: %w", err)
	}
	duration := trimLength(segments)
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "out_time_us="); ok && progress != nil {
			if us, err := strconv.ParseInt(value, 10, 64); err == nil {
				progress(min(0.99, max(0, float64(us)/float64(duration.Microseconds()))))
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", MediaInfo{}, ctx.Err()
		}
		return "", MediaInfo{}, fmt.Errorf("FFmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := scanner.Err(); err != nil {
		return "", MediaInfo{}, err
	}
	info, err := ProbeMedia(ctx, tmp)
	if err != nil {
		if ctx.Err() != nil {
			return "", MediaInfo{}, ctx.Err()
		}
		return "", MediaInfo{}, fmt.Errorf("check the trimmed recording: %w", err)
	}
	if info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		return "", MediaInfo{}, errors.New("the trimmed recording contains no video")
	}
	base := strings.TrimSuffix(source, filepath.Ext(source)) + "-trimmed"
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return "", MediaInfo{}, err
		}
		target := base + ".mp4"
		if n > 1 {
			target = fmt.Sprintf("%s-%d.mp4", base, n)
		}
		if err := unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", MediaInfo{}, err
		}
		return target, info, nil
	}
}

type trimHistory struct {
	states []trimSelection
	index  int
}

func (h *trimHistory) reset(s trimSelection) { h.states = []trimSelection{s}; h.index = 0 }

func (h *trimHistory) record(s trimSelection) {
	if len(h.states) == 0 {
		h.reset(s)
		return
	}
	if h.states[h.index] == s {
		return
	}
	h.states = append(h.states[:h.index+1], s)
	if len(h.states) > 100 {
		h.states = h.states[1:]
	}
	h.index = len(h.states) - 1
}

func (h *trimHistory) restore(redo bool) (trimSelection, bool) {
	next := h.index - 1
	if redo {
		next = h.index + 1
	}
	if next < 0 || next >= len(h.states) {
		return trimSelection{}, false
	}
	h.index = next
	return h.states[next], true
}
