package gui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

type frameClock struct {
	base   *big.Rat
	origin time.Duration
}

func probeFrameClock(ctx context.Context, file string) (*frameClock, error) {
	out, err := exec.CommandContext(ctx, prefs.Bin("ffprobe"), "-v", "error", "-select_streams", "v:0", "-show_entries", "format=start_time:stream=time_base", "-of", "json", file).Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("inspect frame timing: %w", err)
	}
	var data struct {
		Streams []struct {
			Base string `json:"time_base"`
		} `json:"streams"`
		Format struct {
			Start string `json:"start_time"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, err
	}
	if len(data.Streams) == 0 {
		return nil, errors.New("no video frame timing found")
	}
	base, ok := new(big.Rat).SetString(data.Streams[0].Base)
	if !ok || base.Sign() <= 0 {
		return nil, errors.New("invalid video time base")
	}
	var origin time.Duration
	if data.Format.Start != "" && data.Format.Start != "N/A" {
		origin, err = time.ParseDuration(data.Format.Start + "s")
		if err != nil {
			return nil, err
		}
	}
	return &frameClock{base, origin}, nil
}

func (c *frameClock) timestamp(pts int64) time.Duration {
	value := new(big.Rat).Mul(new(big.Rat).SetInt64(pts), c.base)
	value.Mul(value, new(big.Rat).SetInt64(int64(time.Second)))
	return time.Duration(new(big.Int).Quo(value.Num(), value.Denom()).Int64()) - c.origin
}

type frameWindow struct {
	times       []time.Duration
	first, last bool
}

func (w frameWindow) index(at time.Duration) int {
	return max(0, sort.Search(len(w.times), func(i int) bool { return w.times[i] > at })-1)
}

func (w frameWindow) snap(at time.Duration) (time.Duration, bool) {
	i := sort.Search(len(w.times), func(i int) bool { return w.times[i] >= at })
	if i == len(w.times) {
		return 0, false
	}
	return w.times[i], true
}

// snapFrameTime returns the timestamp of the first frame at or after at, and
// duration when no frame starts before the recording ends. A boundary at the
// very start still asks for the first frame, so a recording whose picture
// starts after its sound keeps the two together.
func snapFrameTime(ctx context.Context, file string, clock *frameClock, at, duration time.Duration) (time.Duration, error) {
	if at >= duration {
		return duration, nil
	}
	window, err := loadFrameWindowLimit(ctx, file, clock, max(0, at), duration, 1)
	if err != nil {
		return 0, err
	}
	if snapped, ok := window.snap(at); ok {
		return snapped, nil
	}
	return duration, nil
}

func loadFrameWindow(ctx context.Context, file string, clock *frameClock, at, duration time.Duration) (frameWindow, error) {
	return loadFrameWindowLimit(ctx, file, clock, at, duration, 256)
}

// loadFrameWindowLimit reads frames around at, stopping once limit frames at
// or after it have been read; the trim preview wants many, snapping wants one.
func loadFrameWindowLimit(ctx context.Context, file string, clock *frameClock, at, duration time.Duration, limit int) (frameWindow, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	start, end := max(0, at-time.Second), duration
	interval := trimSeconds(start+clock.origin) + "%" + trimSeconds(end+clock.origin)
	cmd := exec.CommandContext(ctx, prefs.Bin("ffprobe"), "-v", "error", "-select_streams", "v:0", "-read_intervals", interval, "-show_frames", "-show_entries", "frame=best_effort_timestamp", "-of", "csv=p=0", file) //nolint:gosec
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return frameWindow{}, err
	}
	if err := cmd.Start(); err != nil {
		return frameWindow{}, err
	}
	window := frameWindow{first: start == 0, last: end == duration}
	scan := bufio.NewScanner(pipe)
	following := 0
	stopped := false
	for scan.Scan() {
		value, _, _ := strings.Cut(scan.Text(), ",")
		pts, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		stamp := clock.timestamp(pts)
		if stamp < 0 || stamp >= duration {
			continue
		}
		if len(window.times) > 0 && stamp <= window.times[len(window.times)-1] {
			continue
		}
		if stamp < at && len(window.times) == 256 {
			copy(window.times, window.times[1:])
			window.times = window.times[:255]
			window.first = false
		}
		window.times = append(window.times, stamp)
		if stamp >= at {
			following++
		}
		if following == limit {
			stopped = true
			window.last = false
			cancel()
			break
		}
	}
	err = cmd.Wait()
	if scan.Err() != nil {
		return frameWindow{}, scan.Err()
	}
	if err != nil && !stopped {
		return frameWindow{}, fmt.Errorf("read frame timing: %w", err)
	}
	if len(window.times) == 0 {
		return frameWindow{}, errors.New("no frames found at this position")
	}
	return window, nil
}

func extractTrimFrame(ctx context.Context, file string, at time.Duration) ([]byte, error) {
	// Input seeking keeps the original timeline with copyts and start_at_zero
	filter := "select='gte(t," + trimSeconds(at) + ")',scale=960:-2"
	data, err := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), "-v", "error", "-nostdin", "-copyts", "-start_at_zero", "-ss", trimSeconds(max(0, at-time.Second)), "-i", file, "-vf", filter, "-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "-").Output() //nolint:gosec
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("no frame was decoded")
	}
	return data, nil
}
