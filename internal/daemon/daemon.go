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

	"sway-easyshot/internal/commands"
	"sway-easyshot/internal/config"
	"sway-easyshot/internal/external"
	"sway-easyshot/internal/state"
	"sway-easyshot/pkg/protocol"
)

// Daemon manages the socket server for executing screenshot and recording commands.
type Daemon struct {
	cfg               *config.Config
	state             *state.State
	listener          net.Listener
	screenshotHandler *commands.ScreenshotHandler
	recordingHandler  *commands.RecordingHandler
	obsHandler        *commands.OBSHandler
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
		obsHandler:        commands.NewOBSHandler(cfg, st),
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
		if q, ok := req.Options["quiet_finish"].(bool); ok && q {
			ctx = commands.WithQuietFinish(ctx)
		}
	}

	var err error

	switch req.Action {
	// Screenshot commands
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
		err = d.recordingHandler.MovieSelection(ctx, delay, audio)

	case "movie-screen":
		if output != "" {
			err = d.recordingHandler.MovieOutput(ctx, delay, output, cropTop, audio)
		} else {
			err = d.recordingHandler.MovieScreen(ctx, delay, useCurrentScreen, cropTop, audio)
		}

	case "movie-geometry":
		err = d.recordingHandler.MovieGeometry(ctx, delay, geometry, audio)

	case "movie-current-window":
		err = d.recordingHandler.MovieCurrentWindow(ctx, delay, audio)

	case "stop-recording":
		err = d.recordingHandler.StopRecording(ctx)

	case "pause-recording":
		err = d.recordingHandler.PauseRecording(ctx)

	case "toggle-record":
		startAction := "movie-selection" // default
		if req.Options != nil {
			if sa, ok := req.Options["start_action"].(string); ok && sa != "" {
				startAction = sa
			}
		}
		err = d.recordingHandler.ToggleRecord(ctx, startAction, delay, useCurrentScreen, cropTop, audio)

	// OBS commands
	case "obs-toggle-recording":
		err = d.obsHandler.ToggleRecording(ctx)

	case "obs-toggle-pause":
		err = d.obsHandler.TogglePause(ctx)

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
				if obsRecording, ok := iconsMap["ObsRecording"].(string); ok {
					icons.ObsRecording = obsRecording
				}
				if obsPaused, ok := iconsMap["ObsPaused"].(string); ok {
					icons.ObsPaused = obsPaused
				}
				if countdown, ok := iconsMap["Countdown"].(string); ok {
					icons.Countdown = countdown
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
