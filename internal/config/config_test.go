package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadUsesTequilashotIdentity(t *testing.T) {
	home := t.TempDir()
	saveLocation := filepath.Join(t.TempDir(), "captures")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TEQUILASHOT_SAVE_LOCATION", saveLocation)
	t.Setenv("TEQUILASHOT_AI_MODEL", "test-model")
	t.Setenv("TEQUILASHOT_WAYBAR_POLL_INTERVAL", "250ms")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.SaveDir() != saveLocation {
		t.Errorf("SaveLocation = %q, want %q", cfg.SaveLocation, saveLocation)
	}
	if cfg.CacheFile != filepath.Join(home, ".cache", ".tequilashot-recording") {
		t.Errorf("CacheFile = %q", cfg.CacheFile)
	}
	if cfg.SocketPath != fmt.Sprintf("/run/user/%d/tequilashot.sock", os.Getuid()) {
		t.Errorf("SocketPath = %q", cfg.SocketPath)
	}
	if cfg.AIModel() != "test-model" {
		t.Errorf("AIModelImage = %q, want test-model", cfg.AIModel())
	}
	if cfg.WaybarPollInterval != 250*time.Millisecond {
		t.Errorf("WaybarPollInterval = %s, want 250ms", cfg.WaybarPollInterval)
	}
	for _, icon := range []string{cfg.ScreenshotIcon, cfg.RecordingStartIcon, cfg.RecordingStopIcon, cfg.RecordingPauseIcon} {
		if icon != "com.chmouel.Tequilashot.Recorder" {
			t.Errorf("notification icon = %q, want Tequilashot logo", icon)
		}
	}
	if _, err := os.Stat(saveLocation); err != nil {
		t.Errorf("save location was not created: %v", err)
	}
}
