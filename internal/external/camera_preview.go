package external

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

// previewReleaseTimeout bounds how long a stopped preview may take to exit.
const previewReleaseTimeout = 2 * time.Second

// PreviewArgs returns the ffmpeg arguments that stream device as MJPEG on
// stdout at fps frames a second. Without native MJPEG the raw YUYV picture is
// encoded instead.
func PreviewArgs(device string, width, height, fps int, nativeMJPEG bool) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "v4l2"}
	if nativeMJPEG {
		args = append(args, "-input_format", "mjpeg")
	} else {
		args = append(args, "-input_format", "yuyv422")
	}
	args = append(args,
		"-video_size", fmt.Sprintf("%dx%d", width, height),
		"-framerate", strconv.Itoa(fps),
		"-i", device)
	if nativeMJPEG {
		return append(args, "-c:v", "copy", "-f", "mjpeg", "-")
	}
	return append(args, "-c:v", "mjpeg", "-q:v", "5", "-f", "mjpeg", "-")
}

// Preview is one running live-preview session.
type Preview struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// PreviewGate tracks sessions that have been told to stop but may still hold
// the camera. Anything about to open the device must first wait on the gate.
// The zero value is ready to use.
type PreviewGate struct {
	mu       sync.Mutex
	draining map[*Preview]struct{}
}

// Retire stops p and keeps it in the gate until its process has exited.
func (g *PreviewGate) Retire(p *Preview) {
	if p == nil {
		return
	}
	p.cancel()
	g.mu.Lock()
	if g.draining == nil {
		g.draining = map[*Preview]struct{}{}
	}
	g.draining[p] = struct{}{}
	g.mu.Unlock()
	go func() {
		<-p.done
		g.mu.Lock()
		delete(g.draining, p)
		g.mu.Unlock()
	}()
}

// Wait blocks until every retired session has exited, up to timeout, and
// reports whether they all did.
func (g *PreviewGate) Wait(timeout time.Duration) bool {
	return g.wait(context.Background(), timeout)
}

func (g *PreviewGate) wait(ctx context.Context, timeout time.Duration) bool {
	g.mu.Lock()
	pending := make([]*Preview, 0, len(g.draining))
	for p := range g.draining {
		pending = append(pending, p)
	}
	g.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, p := range pending {
		select {
		case <-p.done:
		case <-timer.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// Start runs run in the background once the gate is clear. If the gate does
// not clear within timeout, run is skipped and onFail is called instead.
func (g *PreviewGate) Start(timeout time.Duration, run func(ctx context.Context), onFail func()) *Preview {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Preview{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		if !g.wait(ctx, timeout) {
			if ctx.Err() == nil {
				onFail()
			}
			return
		}
		if ctx.Err() == nil {
			run(ctx)
		}
	}()
	return p
}

// StartPreview streams the camera in the background, calling onFrame from a
// separate goroutine for every JPEG frame and onFail once if the camera cannot
// be opened or stops delivering. It first waits for g to release the device.
// Retire the result on g to end the session.
func StartPreview(g *PreviewGate, device string, width, height, fps int, onFrame func([]byte), onFail func()) *Preview {
	return g.Start(previewReleaseTimeout, func(ctx context.Context) {
		for _, native := range []bool{true, false} {
			if ctx.Err() != nil {
				return
			}
			parsed := runPreview(ctx, PreviewArgs(device, width, height, fps, native), onFrame)
			if ctx.Err() != nil {
				return
			}
			if parsed > 0 {
				break
			}
		}
		if ctx.Err() == nil {
			onFail()
		}
	}, onFail)
}

// runPreview runs one ffmpeg and returns the number of JPEG frames it emitted.
func runPreview(ctx context.Context, args []string, onFrame func([]byte)) int {
	cmd := exec.CommandContext(ctx, prefs.Bin("ffmpeg"), args...) //nolint:gosec
	cmd.WaitDelay = time.Second
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return 0
	}

	var split JPEGSplitter
	buf := make([]byte, 64<<10)
	parsed := 0
	for {
		n, err := out.Read(buf)
		for _, f := range split.Feed(buf[:n]) {
			parsed++
			onFrame(f)
		}
		if err != nil {
			if err != io.EOF {
				_ = cmd.Process.Kill()
			}
			break
		}
	}
	_ = cmd.Wait()
	return parsed
}
