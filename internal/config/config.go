package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config holds all configuration for tequilashot.
type Config struct {
	SaveLocation       string
	CacheFile          string
	CleanupTime        time.Duration
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
		SaveLocation:       getEnv("TEQUILASHOT_SAVE_LOCATION", filepath.Join(homeDir, "Downloads", "Screenshots")),
		CacheFile:          filepath.Join(homeDir, ".cache", ".tequilashot-recording"),
		CleanupTime:        3 * 24 * time.Hour, // 3 days
		AIModelImage:       getEnv("TEQUILASHOT_AI_MODEL", "gemini:gemini-2.5-flash-image"),
		ScreenshotIcon:     filepath.Join(homeDir, ".local", "share", "icons", "screenshot.svg"),
		RecordingStartIcon: filepath.Join(homeDir, ".local", "share", "icons", "record-start.svg"),
		RecordingStopIcon:  filepath.Join(homeDir, ".local", "share", "icons", "record-stop.svg"),
		RecordingPauseIcon: filepath.Join(homeDir, ".local", "share", "icons", "record-pause.svg"),
		SocketPath:         fmt.Sprintf("/run/user/%d/tequilashot.sock", uid),
		WaybarPollInterval: getPollInterval(),
	}

	// Ensure save location exists
	if err := os.MkdirAll(cfg.SaveLocation, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create save location: %w", err)
	}

	return cfg, nil
}

// GenerateFilename generates a unique filename for a screenshot.
func (c *Config) GenerateFilename() string {
	return filepath.Join(c.SaveLocation, fmt.Sprintf("Screenshot_%s.png", time.Now().Format("2006-01-02-15:04.05")))
}

// GenerateRecordingBase generates a base filename for a recording.
func (c *Config) GenerateRecordingBase() string {
	return filepath.Join(c.SaveLocation, fmt.Sprintf("recording-%s", time.Now().Format("20060102-15h04")))
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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
