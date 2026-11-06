package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"
)

const notificationIcon = "com.chmouel.Tequilashot.Recorder"

// Config holds all configuration for tequilashot.
type Config struct {
	// SaveLocation pins the save folder; when empty it follows the
	// environment and the preferences on every capture.
	SaveLocation string
	CacheFile    string
	// AIModelImage pins the naming model; when empty it follows the
	// environment and the preferences.
	AIModelImage       string
	ScreenshotIcon     string
	RecordingStartIcon string
	RecordingStopIcon  string
	RecordingPauseIcon string
	SocketPath         string
	WaybarPollInterval time.Duration
}

// Load loads the configuration from environment variables and defaults.
func Load() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	uid := os.Getuid()

	cfg := &Config{
		CacheFile:          filepath.Join(homeDir, ".cache", ".tequilashot-recording"),
		ScreenshotIcon:     notificationIcon,
		RecordingStartIcon: notificationIcon,
		RecordingStopIcon:  notificationIcon,
		RecordingPauseIcon: notificationIcon,
		SocketPath:         fmt.Sprintf("/run/user/%d/tequilashot.sock", uid),
		WaybarPollInterval: getPollInterval(),
	}

	_ = prefs.Reload()
	if err := os.MkdirAll(cfg.SaveDir(), 0o750); err != nil {
		return nil, fmt.Errorf("failed to create save location: %w", err)
	}

	return cfg, nil
}

// SaveDir returns the folder for new captures, creating it when missing.
func (c *Config) SaveDir() string {
	dir := c.SaveLocation
	if dir == "" {
		dir = prefs.Current().SaveDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Printf("failed to create save location %s: %v", dir, err)
	}
	return dir
}

// AIModel returns the aichat model used to name screenshots.
func (c *Config) AIModel() string {
	if c.AIModelImage != "" {
		return c.AIModelImage
	}
	return prefs.Current().AIModelName()
}

// GenerateFilename generates a unique filename for a screenshot in dir.
func GenerateFilename(dir string) string {
	return filepath.Join(dir, fmt.Sprintf("Screenshot_%s.png", time.Now().Format("2006-01-02-15:04.05")))
}

// GenerateFilename generates a unique filename for a screenshot.
func (c *Config) GenerateFilename() string {
	return GenerateFilename(c.SaveDir())
}

// GenerateRecordingBase generates a base filename for a recording.
func (c *Config) GenerateRecordingBase() string {
	return filepath.Join(c.SaveDir(), fmt.Sprintf("recording-%s", time.Now().Format("20060102-15h04")))
}

func getPollInterval() time.Duration {
	intervalStr := os.Getenv("TEQUILASHOT_WAYBAR_POLL_INTERVAL")
	if intervalStr == "" {
		return 1000 * time.Millisecond // Default: 1 second
	}

	duration, err := time.ParseDuration(intervalStr)
	if err != nil {
		return 1000 * time.Millisecond // Fallback to default on parse error
	}

	// Enforce minimum of 100ms to prevent excessive polling
	if duration < 100*time.Millisecond {
		return 100 * time.Millisecond
	}

	return duration
}
