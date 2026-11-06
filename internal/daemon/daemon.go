package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chmouel/tequilashot/internal/commands"
	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/state"
	"github.com/chmouel/tequilashot/pkg/protocol"
)

// Daemon manages the socket server for executing screenshot and recording commands.
type Daemon struct {
	cfg               *config.Config
	state             *state.State
	listener          net.Listener
	screenshotHandler *commands.ScreenshotHandler
	recordingHandler  *commands.RecordingHandler
	virtualCamera     *commands.VirtualCameraHandler
	ctx               context.Context
	cancel            context.CancelFunc
	debug             bool
}

// New creates a new daemon instance.
func New(cfg *config.Config, debug bool) *Daemon {
	st := state.NewState()
	ctx, cancel := context.WithCancel(context.Background())

	return &Daemon{
		cfg:               cfg,
		state:             st,
		screenshotHandler: commands.NewScreenshotHandler(cfg, st),
		recordingHandler:  commands.NewRecordingHandler(cfg, st),
		virtualCamera:     commands.NewVirtualCameraHandler(cfg, st),
		ctx:               ctx,
		cancel:            cancel,
		debug:             debug,
	}
}

// Start starts the daemon server listening on the unix socket.
func (d *Daemon) Start() error {
	// Remove existing socket if present
	_ = os.Remove(d.cfg.SocketPath)

	var err error
	d.listener, err = net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to create socket: %w", err)
	}

	// Set socket permissions
	if err := os.Chmod(d.cfg.SocketPath, 0o600); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	log.Printf("Daemon started, listening on %s", d.cfg.SocketPath)

	// Start cleanup routine
	go d.cleanupRoutine()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		d.Stop()
	}()

	// Accept connections
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.ctx.Done():
				return nil
			default:
				log.Printf("Error accepting connection: %v", err)
				continue
			}
		}

		go d.handleConnection(conn)
	}
}

// Stop stops the daemon server.
func (d *Daemon) Stop() {
	log.Println("Stopping daemon")
	d.cancel()
	d.recordingHandler.ShutdownReplay()
	d.virtualCamera.Shutdown()

	if d.listener != nil {
		_ = d.listener.Close()
	}

	_ = os.Remove(d.cfg.SocketPath)
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req protocol.Request
	if err := decoder.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return
		}
		log.Printf("Error decoding request: %v", err)
		_ = encoder.Encode(protocol.Response{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	if req.Action != "waybar-status" || d.debug {
		log.Printf("Received command: %s, action: %s", req.Command, req.Action)
	}

	resp := d.executeCommand(req)
	if err := encoder.Encode(resp); err != nil {
		log.Printf("Error encoding response: %v", err)
	}
}

func (d *Daemon) executeCommand(req protocol.Request) protocol.Response {
	ctx := d.ctx

	// Extract common options
	delay := 0
	useCurrentScreen := false
	cropTop := 0
	var audio external.AudioSources
	var camera cameraRequest
	var output, geometry string

	if req.Options != nil {
		if d, ok := req.Options["delay"].(float64); ok {
			delay = int(d)
		}
		if u, ok := req.Options["use_current_screen"].(bool); ok {
			useCurrentScreen = u
		}
		if ct, ok := req.Options["crop_top"].(float64); ok {
			cropTop = int(ct)
		}
		if o, ok := req.Options["output"].(string); ok {
			output = o
		}
		if g, ok := req.Options["geometry"].(string); ok {
			geometry = g
		}
		if a, ok := req.Options["audio"].(bool); ok {
			audio.Desktop = a
		}
		if m, ok := req.Options["mic"].(bool); ok {
			audio.Mic = m
		}
		var micErr error
		if audio.MicDevice, micErr = parseMicDevice(req.Options); micErr != nil {
			return protocol.Response{Success: false, Message: micErr.Error()}
		}
		var camErr error
		if camera, camErr = parseCameraRequest(req.Options); camErr != nil {
			return protocol.Response{Success: false, Message: camErr.Error()}
		}
		if q, ok := req.Options["quiet_finish"].(bool); ok && q {
			ctx = commands.WithQuietFinish(ctx)
		}
	}

	var err error
	var file string

	opts := commands.RecordOptions{Audio: audio, UseCamera: camera.enabled, Camera: camera.overlay}

	switch req.Action {
	// Screenshot commands
	case "screenshot":
		file, err = d.screenshotHandler.Capture(ctx, delay, output, geometry)

	case "current-window-clipboard":
		err = d.screenshotHandler.CurrentWindowClipboard(ctx, delay)

	case "current-window-file":
		err = d.screenshotHandler.CurrentWindowFile(ctx, delay)

	case "current-screen-clipboard":
		err = d.screenshotHandler.CurrentScreenClipboard(ctx, delay, useCurrentScreen)

	case "selection-file":
		err = d.screenshotHandler.SelectionFile(ctx, delay)

	case "selection-edit":
		err = d.screenshotHandler.SelectionEdit(ctx, delay)

	case "selection-clipboard":
		err = d.screenshotHandler.SelectionClipboard(ctx, delay)

	// Recording commands
	case "movie-selection":
		err = d.recordingHandler.MovieSelection(ctx, delay, opts)

	case "movie-screen":
		if output != "" {
			err = d.recordingHandler.MovieOutput(ctx, delay, output, cropTop, opts)
		} else {
			err = d.recordingHandler.MovieScreen(ctx, delay, useCurrentScreen, cropTop, opts)
		}

	case "movie-geometry":
		err = d.recordingHandler.MovieGeometry(ctx, delay, geometry, opts)

	case "movie-current-window":
		err = d.recordingHandler.MovieCurrentWindow(ctx, delay, opts)

	case "replay-start":
		seconds := 60
		if value, exists := req.Options["seconds"]; exists {
			n, ok := value.(float64)
			if !ok || (n != 30 && n != 60 && n != 120) {
				return protocol.Response{Success: false, Message: "replay duration must be 30, 60 or 120 seconds"}
			}
			seconds = int(n)
		}
		if geometry != "" || camera.enabled {
			err = errors.New("replay supports monitor capture without a webcam")
		} else {
			err = d.recordingHandler.StartReplay(ctx, output, useCurrentScreen, seconds, audio)
		}

	case "replay-save":
		file, err = d.recordingHandler.SaveReplay(ctx)

	case "replay-stop":
		err = d.recordingHandler.StopReplay(ctx)

	case "stop-recording":
		err = d.recordingHandler.StopRecording(ctx)

	case "pause-recording":
		err = d.recordingHandler.PauseRecording(ctx)

	case "virtual-camera-start", "virtual-camera-toggle":
		vopts, vErr := parseVirtualCameraOptions(req.Options)
		if vErr != nil {
			return protocol.Response{Success: false, Message: vErr.Error()}
		}
		vopts.Output, vopts.Geometry = output, geometry
		vopts.UseCamera, vopts.Camera = camera.enabled, camera.overlay
		if req.Action == "virtual-camera-start" {
			err = d.virtualCamera.Start(ctx, vopts)
		} else {
			err = d.virtualCamera.Toggle(ctx, vopts)
		}

	case "virtual-camera-stop":
		err = d.virtualCamera.Stop(ctx)

	case "toggle-record":
		startAction := "movie-selection" // default
		if req.Options != nil {
			if sa, ok := req.Options["start_action"].(string); ok && sa != "" {
				startAction = sa
			}
		}
		err = d.recordingHandler.ToggleRecord(ctx, startAction, delay, useCurrentScreen, cropTop, opts)

	// Waybar status
	case "waybar-status":
		// Check if custom icons were provided in the request
		if req.Options != nil {
			if iconsMap, ok := req.Options["icons"].(map[string]interface{}); ok {
				icons := state.DefaultIcons()
				if idle, ok := iconsMap["Idle"].(string); ok {
					icons.Idle = idle
				}
				if recording, ok := iconsMap["Recording"].(string); ok {
					icons.Recording = recording
				}
				if paused, ok := iconsMap["Paused"].(string); ok {
					icons.Paused = paused
				}
				if countdown, ok := iconsMap["Countdown"].(string); ok {
					icons.Countdown = countdown
				}
				if replay, ok := iconsMap["Replay"].(string); ok {
					icons.Replay = replay
				}
				if live, ok := iconsMap["Live"].(string); ok {
					icons.Live = live
				}
				d.state.SetIcons(icons)
			}
		}
		status := d.state.GetWaybarStatus()
		data, _ := json.Marshal(status)
		return protocol.Response{
			Success: true,
			Message: string(data),
			State:   d.state.GetState(),
		}

	default:
		return protocol.Response{
			Success: false,
			Message: fmt.Sprintf("Unknown action: %s", req.Action),
		}
	}

	if err != nil {
		return protocol.Response{
			Success: false,
			Message: err.Error(),
			State:   d.state.GetState(),
		}
	}

	return protocol.Response{
		Success: true,
		Message: "Command executed successfully",
		File:    file,
		State:   d.state.GetState(),
	}
}

func (d *Daemon) cleanupRoutine() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run immediately on startup
	d.cleanup()

	for {
		select {
		case <-ticker.C:
			d.cleanup()
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *Daemon) cleanup() {
	log.Println("Running cleanup routine")
	if err := external.CleanupOldFiles(d.ctx, d.cfg.SaveLocation, d.cfg.CleanupTime); err != nil {
		log.Printf("Cleanup error: %v", err)
	}
}

// parseMicDevice reads the optional mic_device request option.
func parseMicDevice(options map[string]any) (string, error) {
	v, ok := options["mic_device"]
	if !ok {
		return "", nil
	}
	dev, isString := v.(string)
	if !isString {
		return "", fmt.Errorf("option mic_device must be a string")
	}
	return dev, nil
}

// parseVirtualCameraOptions reads the source and backend options of a
// virtual camera request.
func parseVirtualCameraOptions(options map[string]any) (commands.VirtualCameraOptions, error) {
	var opts commands.VirtualCameraOptions
	for _, f := range []struct {
		key string
		dst any
	}{
		{"selection", &opts.Selection},
		{"current_window", &opts.CurrentWindow},
		{"backend", &opts.Backend},
	} {
		v, ok := options[f.key]
		if !ok {
			continue
		}
		switch dst := f.dst.(type) {
		case *bool:
			b, isBool := v.(bool)
			if !isBool {
				return opts, fmt.Errorf("option %s must be a boolean", f.key)
			}
			*dst = b
		case *string:
			str, isString := v.(string)
			if !isString {
				return opts, fmt.Errorf("option %s must be a string", f.key)
			}
			*dst = str
		}
	}
	return opts, nil
}

// cameraRequest is the webcam overlay part of a start request.
type cameraRequest struct {
	enabled bool
	overlay external.CameraOverlay
}

// parseCameraRequest reads the optional camera_* request options and rejects
// any that are present with the wrong type. Values are validated when the
// recording starts.
func parseCameraRequest(options map[string]any) (cameraRequest, error) {
	var req cameraRequest
	var on, mirror, removeBackground, noRing bool
	var device, position, size, shape string
	for _, f := range []struct {
		key string
		dst any
	}{
		{"camera", &on},
		{"camera_device", &device},
		{"camera_position", &position},
		{"camera_size", &size},
		{"camera_shape", &shape},
		{"camera_mirror", &mirror},
		{"camera_remove_background", &removeBackground},
		{"camera_no_ring", &noRing},
	} {
		v, ok := options[f.key]
		if !ok {
			continue
		}
		switch dst := f.dst.(type) {
		case *bool:
			b, isBool := v.(bool)
			if !isBool {
				return req, fmt.Errorf("option %s must be a boolean", f.key)
			}
			*dst = b
		case *string:
			str, isString := v.(string)
			if !isString {
				return req, fmt.Errorf("option %s must be a string", f.key)
			}
			*dst = str
		}
	}
	if !on {
		return req, nil
	}
	req.enabled = true
	req.overlay = external.CameraOverlay{Device: device, Position: position, Size: size, Shape: shape, Mirror: mirror, RemoveBackground: removeBackground, NoRing: noRing}
	return req, nil
}
