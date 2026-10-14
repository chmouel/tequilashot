package sway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"sway-easyshot/internal/external"
)

// The compositor queries are implemented for both sway and Hyprland; the
// exported functions dispatch on the session environment.

// isHyprland reports whether we are running inside a Hyprland session.
func isHyprland() bool {
	_, ok := os.LookupEnv("HYPRLAND_INSTANCE_SIGNATURE")
	return ok
}

type hyprWindow struct {
	At   [2]int `json:"at"`
	Size [2]int `json:"size"`
}

type hyprMonitor struct {
	Name    string `json:"name"`
	Make    string `json:"make"`
	Model   string `json:"model"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Focused bool   `json:"focused"`
}

func hyprctlJSON(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "hyprctl", args...) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run hyprctl %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

func parseHyprActiveWindow(data []byte) (string, error) {
	var w hyprWindow
	if err := json.Unmarshal(data, &w); err != nil {
		return "", fmt.Errorf("failed to parse hyprland active window: %w", err)
	}
	return fmt.Sprintf("%d,%d %dx%d", w.At[0], w.At[1], w.Size[0], w.Size[1]), nil
}

func parseHyprMonitors(data []byte) ([]hyprMonitor, error) {
	var monitors []hyprMonitor
	if err := json.Unmarshal(data, &monitors); err != nil {
		return nil, fmt.Errorf("failed to parse hyprland monitors: %w", err)
	}
	return monitors, nil
}

func hyprMonitors(ctx context.Context) ([]hyprMonitor, error) {
	output, err := hyprctlJSON(ctx, "-j", "monitors")
	if err != nil {
		return nil, err
	}
	return parseHyprMonitors(output)
}

func hyprFocusedWindowGeometry(ctx context.Context) (string, error) {
	output, err := hyprctlJSON(ctx, "-j", "activewindow")
	if err != nil {
		return "", err
	}
	return parseHyprActiveWindow(output)
}

func hyprOutputGeometry(ctx context.Context, outputName string) (string, error) {
	monitors, err := hyprMonitors(ctx)
	if err != nil {
		return "", err
	}
	for _, m := range monitors {
		if m.Name == outputName {
			return fmt.Sprintf("%d,%d %dx%d", m.X, m.Y, m.Width, m.Height), nil
		}
	}
	return "", fmt.Errorf("output %q not found", outputName)
}

func hyprFocusedOutputName(ctx context.Context) (string, error) {
	monitors, err := hyprMonitors(ctx)
	if err != nil {
		return "", err
	}
	for _, m := range monitors {
		if m.Focused {
			return m.Name, nil
		}
	}
	return "", fmt.Errorf("no focused output found")
}

func hyprSelectOutput(ctx context.Context, useCurrentScreen bool) (string, error) {
	if useCurrentScreen {
		return hyprFocusedOutputName(ctx)
	}

	monitors, err := hyprMonitors(ctx)
	if err != nil {
		return "", err
	}

	var labels []string
	labelToName := make(map[string]string)
	for _, m := range monitors {
		label := fmt.Sprintf("%s - %s %s", m.Name, m.Make, m.Model)
		labels = append(labels, label)
		labelToName[label] = m.Name
	}

	if len(labels) == 0 {
		return "", fmt.Errorf("no active outputs found")
	}

	if len(labels) == 1 {
		return labelToName[labels[0]], nil
	}

	selected, err := external.Wofi(ctx, "Select output", labels)
	if err != nil {
		return "", err
	}

	if name, ok := labelToName[selected]; ok {
		return name, nil
	}
	// Fallback: extract first word
	parts := strings.Fields(selected)
	if len(parts) > 0 {
		return parts[0], nil
	}
	return "", fmt.Errorf("invalid output selection")
}
