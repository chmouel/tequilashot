package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// sendTimeout bounds notify-send so a stalled notification daemon cannot block callers.
const sendTimeout = 5 * time.Second

// Send sends a desktop notification with a timeout, optional icon, and message.
func Send(timeout int, icon, message string) error {
	args := []string{
		"-t", strconv.Itoa(timeout),
	}
	if icon != "" {
		args = append(args, "-i", icon)
	}
	args = append(args, message)

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "notify-send", args...) //nolint:gosec
	return cmd.Run()
}

// SendWithActions sends a notification with action buttons and returns the selected action.
func SendWithActions(timeout int, icon, message string, actions map[string]string) (string, error) {
	args := []string{
		"-t", strconv.Itoa(timeout),
	}
	if icon != "" {
		args = append(args, "-i", icon)
	}

	for id, label := range actions {
		args = append(args, "-A", fmt.Sprintf("%s=%s", id, label))
	}
	args = append(args, message)

	cmd := exec.Command("notify-send", args...) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

// CaptureDelay sends a countdown notification if the delay is more than 2 seconds.
func CaptureDelay(waitSeconds int, label, icon string) error {
	if waitSeconds > 2 {
		msg := fmt.Sprintf("Capturing %s in %d seconds", label, waitSeconds)
		return Send((waitSeconds-1)*1000, icon, msg)
	}
	return nil
}
