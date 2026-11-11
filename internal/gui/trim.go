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

	"github.com/chmouel/tequilashot/internal/denoise"
	"github.com/chmouel/tequilashot/internal/keylog"
	"github.com/chmouel/tequilashot/internal/prefs"

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
	return strconv.FormatFloat(d.Seconds(), 'f', 9, 64)
}

// snapTrimSelection moves the in and out points onto source frames, so a cut
// removes only whole frames and the two streams are cut at the same instants.
func snapTrimSelection(ctx context.Context, source string, clock *frameClock, selection trimSelection) (trimSelection, error) {
	start, err := snapFrameTime(ctx, source, clock, selection.start, selection.duration)
	if err != nil {
		return selection, err
	}
	end, err := snapFrameTime(ctx, source, clock, selection.end, selection.duration)
	if err != nil {
		return selection, err
	}
	if start >= end {
		if selection.remove {
			return selection, errors.New("the selection is shorter than a frame, so nothing would be removed")
		}
		return selection, errors.New("the selection is shorter than a frame, so nothing would be kept")
	}
	selection.start, selection.end = start, end
	return selection, nil
}

// snapTrimSegments puts both ends of every kept span on a source frame, the
// prefix and suffix a cut leaves aside included: each span starts where its
// first picture starts, so sound and picture stay together even when the
// recording's picture starts late. A span holding no whole frame is dropped.
func snapTrimSegments(ctx context.Context, source string, clock *frameClock, segments []trimSegment, duration time.Duration) ([]trimSegment, error) {
	kept := make([]trimSegment, 0, len(segments))
	for _, segment := range segments {
		start, err := snapFrameTime(ctx, source, clock, segment.start, duration)
		if err != nil {
			return nil, err
		}
		end, err := snapFrameTime(ctx, source, clock, segment.end, duration)
		if err != nil {
			return nil, err
		}
		if start < end {
			kept = append(kept, trimSegment{start, end})
		}
	}
	if len(kept) == 0 {
		return nil, errors.New("the selection leaves no whole frame to save")
	}
	return kept, nil
}

// trimFilters joins the kept segments.
func trimFilters(segments []trimSegment, audio bool) string {
	var filters, inputs []string
	for i, segment := range segments {
		span := "start=" + trimSeconds(segment.start) + ":end=" + trimSeconds(segment.end)
		filters = append(filters, fmt.Sprintf("[0:v:0]select='gte(t,%s)*lt(t,%s)',setpts=PTS-STARTPTS[v%d]", trimSeconds(segment.start), trimSeconds(segment.end), i))
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
	out, err := exec.CommandContext(ctx, prefs.Bin("ffprobe"), "-v", "error", "-select_streams", "a:0", //nolint:gosec
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

// exportTrim saves the selection beside source, with background noise removed
// from the sound when removeNoise is set.
func exportTrim(ctx context.Context, source string, removeNoise bool, selection trimSelection, progress func(float64)) (string, MediaInfo, error) {
	if _, err := selection.segments(); err != nil {
		return "", MediaInfo{}, err
	}
	suffix := "-trimmed"
	if removeNoise {
		if err := denoise.Ready(ctx); err != nil {
			return "", MediaInfo{}, err
		}
		if !selection.remove && selection.start == 0 && selection.end == selection.duration {
			suffix = "-denoised"
		}
	}
	clock, err := probeFrameClock(ctx, source)
	if err != nil {
		return "", MediaInfo{}, err
	}
	selection, err = snapTrimSelection(ctx, source, clock, selection)
	if err != nil {
		return "", MediaInfo{}, err
	}
	segments, err := selection.segments()
	if err != nil {
		return "", MediaInfo{}, err
	}
	segments, err = snapTrimSegments(ctx, source, clock, segments, selection.duration)
	if err != nil {
		return "", MediaInfo{}, err
	}
	audio, err := trimHasAudio(ctx, source)
	if err != nil {
		return "", MediaInfo{}, err
	}
	if removeNoise && !audio {
		return "", MediaInfo{}, denoise.ErrNoAudio
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
	st, err := os.Stat(source)
	if err != nil {
		return "", MediaInfo{}, err
	}
	args := []string{
		"-hide_banner", "-v", "error", "-nostdin", "-y", "-copyts", "-start_at_zero", "-i", source,
		"-filter_complex", trimFilters(segments, audio), "-map", "[video]", "-c:v", "libx264", "-preset", "fast", "-crf", "18", "-pix_fmt", "yuv420p", "-fps_mode", "vfr", "-enc_time_base", "1:1000000",
	}
	if audio {
		args = append(args, "-map", "[audio]", "-c:a", "aac", "-b:a", "192k")
	}
	args = append(args, "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", tmp)
	cmd := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), args...) //nolint:gosec
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
	// Cleaning the sound afterwards takes about as long as the trim.
	share := 1.0
	if removeNoise {
		share = 0.5
	}
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "out_time_us="); ok && progress != nil {
			if us, err := strconv.ParseInt(value, 10, 64); err == nil {
				progress(min(0.99, share*max(0, float64(us)/float64(duration.Microseconds()))))
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
	keys, _ := keylog.Load(source)
	keys = trimKeys(keys, segments)
	if removeNoise {
		// Created private, like tmp, before FFmpeg writes the copy into it.
		f, err := os.CreateTemp(filepath.Dir(source), ".tequilashot-denoise-*.mp4")
		if err != nil {
			return "", MediaInfo{}, err
		}
		cleaned := f.Name()
		defer func() { _ = os.Remove(cleaned) }()
		if err := f.Close(); err != nil {
			return "", MediaInfo{}, err
		}
		var report func(float64)
		if progress != nil {
			report = func(f float64) { progress(min(0.99, share+(1-share)*f)) }
		}
		if err := denoise.Clean(ctx, tmp, cleaned, keys, report); err != nil {
			return "", MediaInfo{}, err
		}
		if err := os.Rename(cleaned, tmp); err != nil {
			return "", MediaInfo{}, err
		}
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
	// FFmpeg must be able to write the private temporary file, so the
	// source's permissions are applied only once it is complete.
	if err := os.Chmod(tmp, st.Mode().Perm()); err != nil {
		return "", MediaInfo{}, err
	}
	base := strings.TrimSuffix(source, filepath.Ext(source)) + suffix
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
		if !removeNoise && len(keys) > 0 {
			_ = keylog.Save(target, keys)
		}
		return target, info, nil
	}
}

// trimKeys moves the key times inside segments onto the timeline of the
// joined segments and drops the rest.
func trimKeys(keys []time.Duration, segments []trimSegment) []time.Duration {
	var out []time.Duration
	var offset time.Duration
	for _, segment := range segments {
		for _, k := range keys {
			if k >= segment.start && k < segment.end {
				out = append(out, k-segment.start+offset)
			}
		}
		offset += segment.end - segment.start
	}
	return out
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

type trimPreviewKind uint8

const (
	trimPreviewFull trimPreviewKind = iota
	trimPreviewStart
	trimPreviewEnd
	trimPreviewCut
)

func (s trimSelection) previewSegments(kind trimPreviewKind) ([]trimSegment, error) {
	segments, err := s.segments()
	if err != nil || kind == trimPreviewFull {
		return segments, err
	}
	const contextLength = 2 * time.Second
	switch {
	case !s.remove && kind == trimPreviewStart:
		return []trimSegment{{s.start, min(s.end, s.start+contextLength)}}, nil
	case !s.remove && kind == trimPreviewEnd:
		return []trimSegment{{max(s.start, s.end-contextLength), s.end}}, nil
	case s.remove && kind == trimPreviewCut:
		for i, span := range segments {
			if span.end <= s.start {
				segments[i].start = max(span.start, span.end-contextLength)
			} else {
				segments[i].end = min(span.end, span.start+contextLength)
			}
		}
		return segments, nil
	default:
		return nil, errors.New("choose a preview for the current selection mode")
	}
}
