package external

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Virtual camera backends and the picture it publishes.
const (
	VirtualCameraAuto     = "auto"
	VirtualCameraV4L2     = "v4l2loopback"
	VirtualCameraPipeWire = "pipewire"

	VirtualCameraName   = "tequilashot"
	VirtualCameraWidth  = 1280
	VirtualCameraHeight = 720
	VirtualCameraFPS    = 30

	virtualCameraStartWindow = 1500 * time.Millisecond
	virtualCameraStopLimit   = 3 * time.Second
)

// ValidVirtualCameraBackend reports whether backend is a known backend name.
func ValidVirtualCameraBackend(backend string) bool {
	switch backend {
	case VirtualCameraAuto, VirtualCameraV4L2, VirtualCameraPipeWire:
		return true
	}
	return false
}

// VirtualCameraSpec describes what the virtual camera shows and where it goes.
type VirtualCameraSpec struct {
	// Geometry is an "X,Y WxH" area; when empty, Output names a monitor.
	Geometry string
	Output   string
	Backend  string
	// Device is the v4l2loopback device for the v4l2loopback backend.
	Device string
}

var (
	loopbackDevices = LoopbackDevices
	lookPath        = exec.LookPath
)

// ResolveVirtualCameraBackend turns auto into a concrete backend, preferring a
// v4l2loopback device, and checks that the chosen backend can work.
func ResolveVirtualCameraBackend(backend string) (resolved, device string, err error) {
	if backend == "" {
		backend = VirtualCameraAuto
	}
	if !ValidVirtualCameraBackend(backend) {
		return "", "", fmt.Errorf("unknown virtual camera backend %q, expected auto, v4l2loopback or pipewire", backend)
	}
	if _, err := lookPath("wf-recorder"); err != nil {
		return "", "", errors.New("the virtual camera needs wf-recorder")
	}
	if backend != VirtualCameraPipeWire {
		if devices := loopbackDevices(); len(devices) > 0 {
			return VirtualCameraV4L2, devices[0], nil
		}
		if backend == VirtualCameraV4L2 {
			return "", "", errors.New("no v4l2loopback device found; load the v4l2loopback module first")
		}
	}
	if _, err := lookPath("gst-launch-1.0"); err != nil {
		return "", "", errors.New("the PipeWire virtual camera needs gst-launch-1.0 with the PipeWire plugin")
	}
	return VirtualCameraPipeWire, "", nil
}

// wfRecorderArgs captures the source, scaled and letterboxed to the virtual
// camera size, as raw frames. Without -D wf-recorder sends no frames for a
// still screen and ignores SIGINT until something changes.
func wfRecorderArgs(spec VirtualCameraSpec) []string {
	w, h := strconv.Itoa(VirtualCameraWidth), strconv.Itoa(VirtualCameraHeight)
	args := []string{"-D", "-y"}
	if spec.Geometry != "" {
		args = append(args, "-g", spec.Geometry)
	} else {
		args = append(args, "-o", spec.Output)
	}
	args = append(
		args,
		"-r", strconv.Itoa(VirtualCameraFPS),
		"-F", "scale="+w+":"+h+":force_original_aspect_ratio=decrease:force_divisible_by=2,pad="+w+":"+h+":(ow-iw)/2:(oh-ih)/2",
		"-c", "rawvideo",
		"-x", "yuv420p",
	)
	if spec.Backend == VirtualCameraV4L2 {
		return append(args, "-m", "v4l2", "-f", spec.Device)
	}
	return append(args, "-m", "rawvideo", "-f", "/dev/stdout")
}

// gstArgs publishes raw frames from stdin as a PipeWire camera.
func gstArgs() []string {
	return []string{
		"-q", "fdsrc", "fd=0", "!",
		"rawvideoparse",
		"width=" + strconv.Itoa(VirtualCameraWidth),
		"height=" + strconv.Itoa(VirtualCameraHeight),
		"format=i420",
		"framerate=" + strconv.Itoa(VirtualCameraFPS) + "/1", "!",
		"videoconvert", "!",
		"pipewiresink", "mode=provide",
		"stream-properties=props,media.class=Video/Source,media.role=Camera",
		"client-name=" + VirtualCameraName,
	}
}

// VirtualCamera is a running capture feeding a virtual camera.
type VirtualCamera struct {
	recorder *exec.Cmd
	sink     *exec.Cmd
	done     chan struct{}
	stderr   *tail

	stopping bool
	mu       sync.Mutex
}

// StartVirtualCamera starts the capture and returns once it has stayed up
// briefly, so that a bad source or backend fails here with its message.
func StartVirtualCamera(spec VirtualCameraSpec) (*VirtualCamera, error) {
	if spec.Geometry == "" && spec.Output == "" {
		return nil, errors.New("the virtual camera needs an output or a geometry")
	}
	if spec.Backend == VirtualCameraV4L2 && !ValidCameraDevice(spec.Device) {
		return nil, fmt.Errorf("invalid v4l2loopback device %q", spec.Device)
	}

	v := &VirtualCamera{done: make(chan struct{}), stderr: &tail{max: 2048}}
	v.recorder = exec.Command("wf-recorder", wfRecorderArgs(spec)...) //nolint:gosec // arguments are built above
	v.recorder.Stderr = v.stderr
	v.recorder.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGINT}

	var pipeR, pipeW *os.File
	if spec.Backend != VirtualCameraV4L2 {
		var err error
		if pipeR, pipeW, err = os.Pipe(); err != nil {
			return nil, err
		}
		v.recorder.Stdout = pipeW
		v.sink = exec.Command("gst-launch-1.0", gstArgs()...) //nolint:gosec // fixed arguments
		v.sink.Stdin = pipeR
		v.sink.Stderr = v.stderr
		v.sink.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
		if err := v.sink.Start(); err != nil {
			_ = pipeR.Close()
			_ = pipeW.Close()
			return nil, fmt.Errorf("failed to start gst-launch-1.0: %w", err)
		}
	}
	err := v.recorder.Start()
	if pipeR != nil {
		_ = pipeR.Close()
		_ = pipeW.Close()
	}
	if err != nil {
		if v.sink != nil {
			_ = v.sink.Process.Kill()
			_ = v.sink.Wait()
		}
		return nil, fmt.Errorf("failed to start wf-recorder: %w", err)
	}

	recorderDone := make(chan struct{})
	go func() {
		_ = v.recorder.Wait()
		close(recorderDone)
	}()
	go func() {
		if v.sink == nil {
			<-recorderDone
			close(v.done)
			return
		}
		sinkDone := make(chan struct{})
		go func() {
			_ = v.sink.Wait()
			close(sinkDone)
		}()
		// Either side ending brings the other down: the sink sees the end of
		// its input, and wf-recorder fails writing to a closed pipe.
		select {
		case <-recorderDone:
			stopWithin(v.sink, sinkDone)
		case <-sinkDone:
			stopWithin(v.recorder, recorderDone)
		}
		<-recorderDone
		<-sinkDone
		close(v.done)
	}()

	select {
	case <-v.done:
		return nil, fmt.Errorf("the virtual camera stopped at once: %s", v.Failure())
	case <-time.After(virtualCameraStartWindow):
		return v, nil
	}
}

// stopWithin gives cmd a moment to exit on its own, then kills it.
func stopWithin(cmd *exec.Cmd, exited <-chan struct{}) {
	select {
	case <-exited:
	case <-time.After(virtualCameraStopLimit):
		_ = cmd.Process.Kill()
	}
}

// Done is closed once the capture has ended, whether stopped or failed.
func (v *VirtualCamera) Done() <-chan struct{} { return v.done }

// Stopped reports whether Stop ended the capture.
func (v *VirtualCamera) Stopped() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stopping
}

// Failure returns the last error output, for a capture that ended on its own.
func (v *VirtualCamera) Failure() string {
	if msg := v.stderr.String(); msg != "" {
		return msg
	}
	return "exited unexpectedly"
}

// Stop ends the capture and waits for the camera device to be released.
func (v *VirtualCamera) Stop() {
	v.mu.Lock()
	v.stopping = true
	v.mu.Unlock()
	_ = v.recorder.Process.Signal(syscall.SIGINT)
	select {
	case <-v.done:
	case <-time.After(virtualCameraStopLimit):
		_ = v.recorder.Process.Kill()
		<-v.done
	}
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if len(t.data) > t.max {
		t.data = t.data[len(t.data)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return lastLines(string(t.data), 3)
}

// lastLines returns the last n non-empty lines of s, joined by "; ". Progress
// output rewritten with carriage returns counts as separate lines.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "; ")
}
