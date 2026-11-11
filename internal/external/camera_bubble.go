package external

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/chmouel/tequilashot/internal/notify"
)

const (
	layerShellLibrary = "libgtk4-layer-shell.so.0"
	bubbleStartLimit  = 10 * time.Second
	bubbleStopLimit   = 3 * time.Second
)

// CameraBubble is the round camera shown on screen whilst recording.
type CameraBubble struct {
	cmd      *exec.Cmd
	done     chan struct{}
	stopping atomic.Bool
	input    io.WriteCloser
	replies  chan string
	mu       sync.Mutex
	failed   bool
}

// bubbleArgs returns the camera-bubble arguments for a circle of the given
// diameter whose top-left corner is at x, y in layout coordinates.
func (c CameraOverlay) bubbleArgs(x, y, diameter int, area image.Rectangle) []string {
	args := []string{
		"camera-bubble",
		"--device", c.Device,
		"--x", strconv.Itoa(x),
		"--y", strconv.Itoa(y),
		"--diameter", strconv.Itoa(diameter),
		"--area-x", strconv.Itoa(area.Min.X),
		"--area-y", strconv.Itoa(area.Min.Y),
		"--area-width", strconv.Itoa(area.Dx()),
		"--area-height", strconv.Itoa(area.Dy()),
		"--size", c.Size,
		"--width", strconv.Itoa(c.Width),
		"--height", strconv.Itoa(c.Height),
		"--fps", strconv.Itoa(c.FPS),
	}
	if c.Mirror {
		args = append(args, "--mirror")
	}
	if c.RemoveBackground {
		args = append(args, "--remove-background")
	}
	if c.Background != "" {
		args = append(args, "--background", c.Background)
	}
	if c.NoRing {
		args = append(args, "--no-ring")
	}
	if c.MattingModel != "" {
		args = append(args, "--matting-model", c.MattingModel)
	}
	return args
}

// StartCameraBubble shows the camera as a circle at x, y and waits until its
// first picture is on screen. gtk4-layer-shell has to be loaded before the
// Wayland client library, so it is preloaded into the bubble process.
func StartCameraBubble(camera CameraOverlay, x, y, diameter int, area image.Rectangle) (*CameraBubble, error) {
	if err := camera.Validate(); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to find tequilashot: %w", err)
	}

	cmd := exec.Command(exe, camera.bubbleArgs(x, y, diameter, area)...) //nolint:gosec // arguments are validated above
	preload := layerShellLibrary
	if cur := os.Getenv("LD_PRELOAD"); cur != "" {
		preload += ":" + cur
	}
	cmd.Env = append(os.Environ(), "LD_PRELOAD="+preload)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start the round camera: %w", err)
	}

	b := &CameraBubble{cmd: cmd, done: make(chan struct{}), input: in, replies: make(chan string, 1)}
	ready := make(chan bool, 1)
	go func() {
		ok := false
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			if scanner.Text() == "ready" {
				ok = true
				ready <- true
			} else if ok {
				b.replies <- scanner.Text()
			}
		}
		if !ok {
			ready <- false
		}
		_ = cmd.Wait()
		close(b.done)
		// The camera hides itself when it fails, so the user is told why.
		if ok && !b.stopping.Load() {
			_ = notify.Send(5000, "", "Round camera stopped", bubbleFailure(stderr.String()))
		}
	}()

	timer := time.NewTimer(bubbleStartLimit)
	defer timer.Stop()
	select {
	case ok := <-ready:
		if ok {
			return b, nil
		}
		<-b.done
		return nil, fmt.Errorf("the round camera failed: %s", bubbleFailure(stderr.String()))
	case <-timer.C:
		b.Stop()
		return nil, fmt.Errorf("the round camera did not appear within %s", bubbleStartLimit)
	}
}

// Cycle waits for the bubble to move and returns the chosen corner.
func (b *CameraBubble) Cycle() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed || b.stopping.Load() {
		return "", fmt.Errorf("the round camera is no longer available")
	}
	select {
	case <-b.done:
		return "", fmt.Errorf("the round camera is no longer available")
	default:
	}
	if _, err := fmt.Fprintln(b.input, "cycle"); err != nil {
		return "", fmt.Errorf("failed to move the round camera: %w", err)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case reply := <-b.replies:
		if ValidCameraPosition(reply) {
			return reply, nil
		}
		return "", fmt.Errorf("failed to move the round camera: %s", reply)
	case <-b.done:
		return "", fmt.Errorf("the round camera stopped")
	case <-timer.C:
		b.failed = true
		return "", fmt.Errorf("the round camera did not respond")
	}
}

var logTimestamp = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)

// bubbleFailure returns the last line the bubble wrote to stderr, which holds
// its error once it has exited, without the log timestamp.
func bubbleFailure(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if last := logTimestamp.ReplaceAllString(strings.TrimSpace(lines[len(lines)-1]), ""); last != "" {
		return last
	}
	return "exited unexpectedly"
}

// Stop removes the bubble and waits for it to release the camera.
func (b *CameraBubble) Stop() {
	if b == nil {
		return
	}
	b.stopping.Store(true)
	_ = b.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-b.done:
	case <-time.After(bubbleStopLimit):
		_ = b.cmd.Process.Kill()
		<-b.done
	}
}
