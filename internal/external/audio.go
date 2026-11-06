package external

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/chmouel/tequilashot/internal/prefs"
)

// AudioDevice is a capture device reported by gpu-screen-recorder.
type AudioDevice struct {
	ID   string
	Name string
}

const maxMicDeviceLen = 200

var micDevicePattern = regexp.MustCompile(`^[A-Za-z0-9._:@+-]+$`)

// ValidateMicDevice accepts an empty id (the system default) or one made of
// characters that cannot alter the gpu-screen-recorder -a syntax.
func ValidateMicDevice(id string) error {
	if id == "" {
		return nil
	}
	if len(id) > maxMicDeviceLen || !micDevicePattern.MatchString(id) {
		return fmt.Errorf("invalid microphone device %q", id)
	}
	return nil
}

// ListMicrophones returns the input devices gpu-screen-recorder can capture.
func ListMicrophones(ctx context.Context) ([]AudioDevice, error) {
	out, err := exec.CommandContext(ctx, prefs.Bin("gpu-screen-recorder"), "--list-audio-devices").Output() //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("listing audio devices: %w", err)
	}
	return ParseMicrophones(string(out)), nil
}

// ParseMicrophones parses "id|description" lines, keeping real inputs only.
func ParseMicrophones(listing string) []AudioDevice {
	var devices []AudioDevice
	for _, line := range strings.Split(listing, "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "|")
		id, name = strings.TrimSpace(id), strings.TrimSpace(name)
		if !ok || id == "" || name == "" {
			continue
		}
		if id == "default_output" || id == "default_input" || strings.HasSuffix(id, ".monitor") {
			continue
		}
		devices = append(devices, AudioDevice{ID: id, Name: name})
	}
	return devices
}

// DefaultMicrophone returns the id of the default input, or "" if unknown.
func DefaultMicrophone(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "pactl", "get-default-source").Output() //nolint:gosec
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
