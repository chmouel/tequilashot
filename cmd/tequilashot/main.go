package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/daemon"
	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/state"
	"github.com/chmouel/tequilashot/pkg/protocol"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:  "tequilashot",
		Usage: "Wayland screenshot and screen recording utility for Sway and Hyprland; without a command, opens the capture dialogue",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Present() {
				return fmt.Errorf("unknown command %q", c.Args().First())
			}
			return guiAction(ctx, c)
		},
		Commands: []*cli.Command{
			daemonCommand(),
			waybarStatusCommand(),
			currentWindowClipboardCommand(),
			currentWindowFileCommand(),
			currentScreenClipboardCommand(),
			selectionFileCommand(),
			selectionEditCommand(),
			selectionClipboardCommand(),
			movieSelectionCommand(),
			movieScreenCommand(),
			movieCurrentWindowCommand(),
			stopRecordingCommand(),
			pauseRecordingCommand(),
			toggleRecordCommand(),
			cameraBubbleCommand(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// Command definitions

func daemonCommand() *cli.Command {
	return &cli.Command{
		Name:  "daemon",
		Usage: "Run in daemon mode (auto-started if needed)",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "Enable debug logging",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			d := daemon.New(cfg, c.Bool("debug"))
			return d.Start()
		},
	}
}

func waybarStatusCommand() *cli.Command {
	return &cli.Command{
		Name:  "waybar-status",
		Usage: "Output waybar status (JSON)",
		Description: "Outputs current recording/screenshot status in Waybar JSON format.\n" +
			"Poll interval: TEQUILASHOT_WAYBAR_POLL_INTERVAL (default: 1s)",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "follow",
				Usage: "Continuously monitor and output on state change",
			},
			&cli.StringFlag{
				Name:  "icon-idle",
				Usage: "Icon for idle/ready state",
				Value: "•",
			},
			&cli.StringFlag{
				Name:  "icon-recording",
				Usage: "Icon for recording state",
				Value: "󰑊",
			},
			&cli.StringFlag{
				Name:  "icon-paused",
				Usage: "Icon for paused recording state",
				Value: "󰏤",
			},
			&cli.StringFlag{
				Name:  "icon-countdown",
				Usage: "Icon for countdown state",
				Value: "⏱",
			},
			&cli.BoolFlag{
				Name:  "no-idle-output",
				Usage: "Output nothing when idle (useful for minimal waybar display)",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			return handleWaybarStatus(cfg, c.Bool("follow"), c.Bool("no-idle-output"), c)
		},
	}
}

func currentWindowClipboardCommand() *cli.Command {
	return createScreenshotCommand("current-window-clipboard", "Capture focused window to clipboard")
}

func currentWindowFileCommand() *cli.Command {
	return createScreenshotCommand("current-window-file", "Capture focused window to file")
}

func currentScreenClipboardCommand() *cli.Command {
	return createScreenshotCommand("current-screen-clipboard", "Capture focused screen to clipboard")
}

func selectionFileCommand() *cli.Command {
	return createScreenshotCommand("selection-file", "Capture selection to file (interactive actions)")
}

func selectionEditCommand() *cli.Command {
	return createScreenshotCommand("selection-edit", "Capture selection and open editor")
}

func selectionClipboardCommand() *cli.Command {
	return createScreenshotCommand("selection-clipboard", "Capture selection to clipboard (optional save/edit)")
}

func movieSelectionCommand() *cli.Command {
	return &cli.Command{
		Name:  "movie-selection",
		Usage: "Record video of selection",
		Flags: append([]cli.Flag{
			&cli.IntFlag{
				Name:    "delay",
				Aliases: []string{"w"},
				Usage:   "Delay capture/recording in seconds",
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "audio",
				Aliases: []string{"A"},
				Usage:   "Record desktop audio (default output device)",
			},
			&cli.BoolFlag{
				Name:    "mic",
				Aliases: []string{"m"},
				Usage:   "Record the microphone (default input device), mixed with --audio when both are set",
			},
			&cli.StringFlag{
				Name:  "mic-device",
				Usage: "Microphone device id from gpu-screen-recorder --list-audio-devices (implies --mic)",
			},
		}, cameraFlags()...),
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := ensureDaemonRunning(cfg); err != nil {
				return err
			}

			req := protocol.Request{
				Command: "execute",
				Action:  "movie-selection",
				Options: withMicOptions(c, withCameraOptions(c, map[string]interface{}{
					"delay": c.Int("delay"),
					"audio": c.Bool("audio"),
					"mic":   c.Bool("mic"),
				})),
			}

			return sendAndHandleStartRequest(cfg.SocketPath, req)
		},
	}
}

func movieScreenCommand() *cli.Command {
	return &cli.Command{
		Name:  "movie-screen",
		Usage: "Record video of screen",
		Flags: append([]cli.Flag{
			&cli.IntFlag{
				Name:    "delay",
				Aliases: []string{"w"},
				Usage:   "Delay capture/recording in seconds",
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "current-screen",
				Aliases: []string{"c"},
				Usage:   "Use current focused screen (skip selection)",
			},
			&cli.IntFlag{
				Name:    "crop-top",
				Aliases: []string{"t"},
				Usage:   "Crop N pixels from the top of the screen",
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "audio",
				Aliases: []string{"A"},
				Usage:   "Record desktop audio (default output device)",
			},
			&cli.BoolFlag{
				Name:    "mic",
				Aliases: []string{"m"},
				Usage:   "Record the microphone (default input device), mixed with --audio when both are set",
			},
			&cli.StringFlag{
				Name:  "mic-device",
				Usage: "Microphone device id from gpu-screen-recorder --list-audio-devices (implies --mic)",
			},
		}, cameraFlags()...),
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := ensureDaemonRunning(cfg); err != nil {
				return err
			}

			req := protocol.Request{
				Command: "execute",
				Action:  "movie-screen",
				Options: withMicOptions(c, withCameraOptions(c, map[string]interface{}{
					"delay":              c.Int("delay"),
					"use_current_screen": c.Bool("current-screen"),
					"crop_top":           c.Int("crop-top"),
					"audio":              c.Bool("audio"),
					"mic":                c.Bool("mic"),
				})),
			}

			return sendAndHandleStartRequest(cfg.SocketPath, req)
		},
	}
}

func movieCurrentWindowCommand() *cli.Command {
	return &cli.Command{
		Name:  "movie-current-window",
		Usage: "Record video of focused window",
		Flags: append([]cli.Flag{
			&cli.IntFlag{
				Name:    "delay",
				Aliases: []string{"w"},
				Usage:   "Delay capture/recording in seconds",
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "audio",
				Aliases: []string{"A"},
				Usage:   "Record desktop audio (default output device)",
			},
			&cli.BoolFlag{
				Name:    "mic",
				Aliases: []string{"m"},
				Usage:   "Record the microphone (default input device), mixed with --audio when both are set",
			},
			&cli.StringFlag{
				Name:  "mic-device",
				Usage: "Microphone device id from gpu-screen-recorder --list-audio-devices (implies --mic)",
			},
		}, cameraFlags()...),
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := ensureDaemonRunning(cfg); err != nil {
				return err
			}

			req := protocol.Request{
				Command: "execute",
				Action:  "movie-current-window",
				Options: withMicOptions(c, withCameraOptions(c, map[string]interface{}{
					"delay": c.Int("delay"),
					"audio": c.Bool("audio"),
					"mic":   c.Bool("mic"),
				})),
			}

			return sendAndHandleStartRequest(cfg.SocketPath, req)
		},
	}
}

func stopRecordingCommand() *cli.Command {
	return createSimpleCommand("stop-recording", "Stop the current recording and save it as MP4")
}

func pauseRecordingCommand() *cli.Command {
	return createSimpleCommand("pause-recording", "Pause/resume current recording")
}

// cameraBubbleCommand is started by the daemon to show the round camera.
func cameraBubbleCommand() *cli.Command {
	return &cli.Command{
		Name:   "camera-bubble",
		Usage:  "Show the round camera on screen (used internally whilst recording)",
		Hidden: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "device", Required: true},
			&cli.IntFlag{Name: "x"},
			&cli.IntFlag{Name: "y"},
			&cli.IntFlag{Name: "diameter", Required: true},
			&cli.IntFlag{Name: "width"},
			&cli.IntFlag{Name: "height"},
			&cli.IntFlag{Name: "fps"},
			&cli.BoolFlag{Name: "mirror"},
		},
		Action: cameraBubbleAction,
	}
}

func toggleRecordCommand() *cli.Command {
	return &cli.Command{
		Name:  "toggle-record",
		Usage: "Toggle recording (start if not recording, stop if recording)",
		Flags: append([]cli.Flag{
			&cli.StringFlag{
				Name:    "start-action",
				Aliases: []string{"a"},
				Usage:   "Action when starting: movie-selection, movie-screen, movie-current-window",
				Value:   "movie-selection",
			},
			&cli.IntFlag{
				Name:    "delay",
				Aliases: []string{"w"},
				Usage:   "Delay before starting recording in seconds",
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "current-screen",
				Aliases: []string{"c"},
				Usage:   "Use current focused screen (for movie-screen action)",
			},
			&cli.IntFlag{
				Name:    "crop-top",
				Aliases: []string{"t"},
				Usage:   "Crop N pixels from the top of the screen (for movie-screen action)",
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "audio",
				Aliases: []string{"A"},
				Usage:   "Record desktop audio (default output device)",
			},
			&cli.BoolFlag{
				Name:    "mic",
				Aliases: []string{"m"},
				Usage:   "Record the microphone (default input device), mixed with --audio when both are set",
			},
			&cli.StringFlag{
				Name:  "mic-device",
				Usage: "Microphone device id from gpu-screen-recorder --list-audio-devices (implies --mic)",
			},
		}, cameraFlags()...),
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := ensureDaemonRunning(cfg); err != nil {
				return err
			}

			req := protocol.Request{
				Command: "execute",
				Action:  "toggle-record",
				Options: withMicOptions(c, withCameraOptions(c, map[string]interface{}{
					"start_action":       c.String("start-action"),
					"delay":              c.Int("delay"),
					"use_current_screen": c.Bool("current-screen"),
					"crop_top":           c.Int("crop-top"),
					"audio":              c.Bool("audio"),
					"mic":                c.Bool("mic"),
				})),
			}

			// No client deadline: stopping may wait up to 30 s for the notification choice and file finalisation.
			return sendAndHandleStartRequest(cfg.SocketPath, req)
		},
	}
}

// Helper functions for command creation

func cameraFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:    "camera",
			Aliases: []string{"C"},
			Usage:   "Overlay the webcam as a picture-in-picture (the camera must not be in use elsewhere)",
		},
		&cli.StringFlag{
			Name:  "camera-device",
			Usage: "Camera device, such as /dev/video0 (default: the first camera found)",
		},
		&cli.StringFlag{
			Name:  "camera-position",
			Usage: "Corner for the camera: top-left, top-right, bottom-left or bottom-right",
			Value: external.CameraBottomRight,
		},
		&cli.StringFlag{
			Name:  "camera-size",
			Usage: "Size of the camera: small, medium or large",
			Value: external.CameraMedium,
		},
		&cli.StringFlag{
			Name:  "camera-shape",
			Usage: "Shape of the camera: square, or circle to show a round camera on screen that is recorded with it",
			Value: external.CameraSquare,
		},
		&cli.BoolFlag{
			Name:  "camera-mirror",
			Usage: "Mirror the camera image horizontally",
		},
	}
}

// withMicOptions adds the microphone device to a start request; naming a
// device turns the microphone on.
func withMicOptions(c *cli.Command, options map[string]interface{}) map[string]interface{} {
	if dev := c.String("mic-device"); dev != "" {
		options["mic"] = true
		options["mic_device"] = dev
	}
	return options
}

// withCameraOptions adds the camera options to a start request when --camera is set.
func withCameraOptions(c *cli.Command, options map[string]interface{}) map[string]interface{} {
	if !c.Bool("camera") {
		return options
	}
	options["camera"] = true
	options["camera_device"] = c.String("camera-device")
	options["camera_position"] = c.String("camera-position")
	options["camera_size"] = c.String("camera-size")
	options["camera_shape"] = c.String("camera-shape")
	options["camera_mirror"] = c.Bool("camera-mirror")
	return options
}

func createSimpleCommand(name, usage string) *cli.Command {
	return &cli.Command{
		Name:  name,
		Usage: usage,
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := ensureDaemonRunning(cfg); err != nil {
				return err
			}

			req := protocol.Request{
				Command: "execute",
				Action:  name,
			}

			return sendAndHandleRequest(cfg.SocketPath, req)
		},
	}
}

func createScreenshotCommand(name, usage string) *cli.Command {
	return &cli.Command{
		Name:  name,
		Usage: usage,
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "delay",
				Aliases: []string{"w"},
				Usage:   "Delay capture/recording in seconds",
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "current-screen",
				Aliases: []string{"c"},
				Usage:   "Use current focused screen (skip selection)",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := ensureDaemonRunning(cfg); err != nil {
				return err
			}

			req := protocol.Request{
				Command: "execute",
				Action:  name,
				Options: map[string]interface{}{
					"delay":              c.Int("delay"),
					"use_current_screen": c.Bool("current-screen"),
				},
			}

			return sendAndHandleRequest(cfg.SocketPath, req)
		},
	}
}

func ensureDaemonRunning(cfg *config.Config) error {
	if !isDaemonRunning(cfg.SocketPath) {
		if err := startDaemon(cfg); err != nil {
			return fmt.Errorf("failed to start daemon: %w", err)
		}

		// Wait for daemon to be ready
		for i := 0; i < 10; i++ {
			if isDaemonRunning(cfg.SocketPath) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}

		return fmt.Errorf("daemon failed to start")
	}
	return nil
}

func sendAndHandleRequest(socketPath string, req protocol.Request) error {
	return handleResponse(sendRequest(socketPath, req))
}

// sendAndHandleStartRequest sends a request that may start a recording, which
// waits on interactive selection and the countdown for an unbounded time.
func sendAndHandleStartRequest(socketPath string, req protocol.Request) error {
	return handleResponse(sendRequestTimeout(socketPath, req, 0))
}

func handleResponse(resp *protocol.Response, err error) error {
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("command failed: %s", resp.Message)
	}

	return nil
}

// Utility functions

func isDaemonRunning(socketPath string) bool {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func startDaemon(cfg *config.Config) error {
	// Get the current executable path
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "daemon") //nolint:gosec
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return err
	}

	// Detach from parent
	_ = cmd.Process.Release()

	return nil
}

func sendRequest(socketPath string, req protocol.Request) (*protocol.Response, error) {
	return sendRequestTimeout(socketPath, req, 30*time.Second)
}

func sendRequestTimeout(socketPath string, req protocol.Request, timeout time.Duration) (*protocol.Response, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(req); err != nil {
		return nil, err
	}

	var resp protocol.Response
	if err := decoder.Decode(&resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func handleWaybarStatus(cfg *config.Config, follow, noIdleOutput bool, c *cli.Command) error {
	icons := state.Icons{
		Idle:      c.String("icon-idle"),
		Recording: c.String("icon-recording"),
		Paused:    c.String("icon-paused"),
		Countdown: c.String("icon-countdown"),
	}
	if follow {
		return followWaybarStatus(cfg, icons, noIdleOutput)
	}
	return outputCurrentStatus(cfg, icons, noIdleOutput)
}

func outputCurrentStatus(cfg *config.Config, icons state.Icons, noIdleOutput bool) error {
	status := getWaybarStatus(cfg, icons)
	if noIdleOutput && status.Class == "idle" {
		status = &protocol.WaybarStatus{Text: "", Tooltip: "", Class: "idle", Alt: "idle"}
	}
	return json.NewEncoder(os.Stdout).Encode(status)
}

func getWaybarStatus(cfg *config.Config, icons state.Icons) *protocol.WaybarStatus {
	if !isDaemonRunning(cfg.SocketPath) {
		// Daemon not running, return idle status
		return &protocol.WaybarStatus{
			Text:    icons.Idle,
			Tooltip: "Ready for screenshot/recording",
			Class:   "idle",
			Alt:     "idle",
		}
	}

	req := protocol.Request{
		Command: "execute",
		Action:  "waybar-status",
		Options: map[string]interface{}{
			"icons": icons,
		},
	}

	resp, err := sendRequest(cfg.SocketPath, req)
	if err != nil {
		// Fallback to idle status on error
		return &protocol.WaybarStatus{
			Text:    icons.Idle,
			Tooltip: "Ready for screenshot/recording",
			Class:   "idle",
			Alt:     "idle",
		}
	}

	// Parse the waybar status from response message
	var status protocol.WaybarStatus
	if err := json.Unmarshal([]byte(resp.Message), &status); err != nil {
		// Fallback to idle status on parse error
		return &protocol.WaybarStatus{
			Text:    icons.Idle,
			Tooltip: "Ready for screenshot/recording",
			Class:   "idle",
			Alt:     "idle",
		}
	}

	return &status
}

func followWaybarStatus(cfg *config.Config, icons state.Icons, noIdleOutput bool) error {
	var previousStatus *protocol.WaybarStatus
	ticker := time.NewTicker(cfg.WaybarPollInterval)
	defer ticker.Stop()

	// Signal handling for Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Output initial status immediately
	currentStatus := getWaybarStatus(cfg, icons)
	outputStatus := currentStatus
	if noIdleOutput && currentStatus.Class == "idle" {
		outputStatus = &protocol.WaybarStatus{Text: "", Tooltip: "", Class: "idle", Alt: "idle"}
	}
	if err := json.NewEncoder(os.Stdout).Encode(outputStatus); err != nil {
		return err
	}
	previousStatus = currentStatus

	for {
		select {
		case <-ticker.C:
			currentStatus := getWaybarStatus(cfg, icons)
			if !statusEqual(previousStatus, currentStatus) {
				outputStatus := currentStatus
				if noIdleOutput && currentStatus.Class == "idle" {
					outputStatus = &protocol.WaybarStatus{Text: "", Tooltip: "", Class: "idle", Alt: "idle"}
				}
				if err := json.NewEncoder(os.Stdout).Encode(outputStatus); err != nil {
					return err
				}
				previousStatus = currentStatus
			}
		case <-sigChan:
			return nil
		}
	}
}

func statusEqual(a, b *protocol.WaybarStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Text == b.Text &&
		a.Tooltip == b.Tooltip &&
		a.Class == b.Class &&
		a.Alt == b.Alt
}
