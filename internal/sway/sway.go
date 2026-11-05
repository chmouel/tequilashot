package sway

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/chmouel/tequilashot/internal/external"
)

type swayRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type swayNode struct {
	Focused       bool       `json:"focused"`
	Rect          swayRect   `json:"rect"`
	Type          string     `json:"type"`
	Nodes         []swayNode `json:"nodes"`
	FloatingNodes []swayNode `json:"floating_nodes"`
}

type swayOutput struct {
	Name    string   `json:"name"`
	Active  bool     `json:"active"`
	Focused bool     `json:"focused"`
	Make    string   `json:"make"`
	Model   string   `json:"model"`
	Rect    swayRect `json:"rect"`
}

// GetFocusedWindowGeometry returns the geometry of the focused window
func GetFocusedWindowGeometry(ctx context.Context) (string, error) {
	if isHyprland() {
		return hyprFocusedWindowGeometry(ctx)
	}
	return swayFocusedWindowGeometry(ctx)
}

func swayFocusedWindowGeometry(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "swaymsg", "-t", "get_tree")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get sway tree: %w", err)
	}

	var tree swayNode
	if err := json.Unmarshal(output, &tree); err != nil {
		return "", fmt.Errorf("failed to parse sway tree: %w", err)
	}

	focused := findFocused(&tree)
	if focused == nil {
		return "", fmt.Errorf("no focused window found")
	}

	rect := focused.Rect
	return fmt.Sprintf("%d,%d %dx%d", rect.X, rect.Y, rect.Width, rect.Height), nil
}

// GetOutputGeometry returns the geometry string for the named output.
func GetOutputGeometry(ctx context.Context, outputName string) (string, error) {
	if isHyprland() {
		return hyprOutputGeometry(ctx, outputName)
	}
	return swayOutputGeometry(ctx, outputName)
}

func swayOutputGeometry(ctx context.Context, outputName string) (string, error) {
	cmd := exec.CommandContext(ctx, "swaymsg", "-t", "get_outputs")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get sway outputs: %w", err)
	}

	var outputs []swayOutput
	if err := json.Unmarshal(output, &outputs); err != nil {
		return "", fmt.Errorf("failed to parse sway outputs: %w", err)
	}

	for _, o := range outputs {
		if o.Name == outputName {
			r := o.Rect
			return fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.Width, r.Height), nil
		}
	}

	return "", fmt.Errorf("output %q not found", outputName)
}

// GetFocusedOutputName returns the name of the focused output
func GetFocusedOutputName(ctx context.Context) (string, error) {
	if isHyprland() {
		return hyprFocusedOutputName(ctx)
	}
	return swayFocusedOutputName(ctx)
}

func swayFocusedOutputName(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "swaymsg", "-t", "get_outputs")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get sway outputs: %w", err)
	}

	var outputs []swayOutput
	if err := json.Unmarshal(output, &outputs); err != nil {
		return "", fmt.Errorf("failed to parse sway outputs: %w", err)
	}

	for _, output := range outputs {
		if output.Focused {
			return output.Name, nil
		}
	}

	return "", fmt.Errorf("no focused output found")
}

// SelectOutput provides interactive output selection
func SelectOutput(ctx context.Context, useCurrentScreen bool) (string, error) {
	if isHyprland() {
		return hyprSelectOutput(ctx, useCurrentScreen)
	}
	return swaySelectOutput(ctx, useCurrentScreen)
}

func swaySelectOutput(ctx context.Context, useCurrentScreen bool) (string, error) {
	if useCurrentScreen {
		return GetFocusedOutputName(ctx)
	}

	cmd := exec.CommandContext(ctx, "swaymsg", "-t", "get_outputs")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get sway outputs: %w", err)
	}

	var outputs []swayOutput
	if err := json.Unmarshal(output, &outputs); err != nil {
		return "", fmt.Errorf("failed to parse sway outputs: %w", err)
	}

	var activeOutputs []string
	outputMap := make(map[string]string)

	for _, output := range outputs {
		if output.Active {
			label := fmt.Sprintf("%s - %s %s", output.Name, output.Make, output.Model)
			activeOutputs = append(activeOutputs, label)
			outputMap[label] = output.Name
		}
	}

	if len(activeOutputs) == 0 {
		return "", fmt.Errorf("no active outputs found")
	}

	if len(activeOutputs) == 1 {
		return outputMap[activeOutputs[0]], nil
	}

	selected, err := external.Wofi(ctx, "Select output", activeOutputs)
	if err != nil {
		return "", err
	}

	name, ok := outputMap[selected]
	if !ok {
		// Fallback: extract first word
		parts := strings.Fields(selected)
		if len(parts) > 0 {
			return parts[0], nil
		}
		return "", fmt.Errorf("invalid output selection")
	}

	return name, nil
}

func findFocused(node *swayNode) *swayNode {
	if node.Focused {
		return node
	}

	for i := range node.Nodes {
		if found := findFocused(&node.Nodes[i]); found != nil {
			return found
		}
	}

	for i := range node.FloatingNodes {
		if found := findFocused(&node.FloatingNodes[i]); found != nil {
			return found
		}
	}

	return nil
}
