package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/config"
	"github.com/chmouel/tequilashot/internal/state"
)

func screenshotTools(t *testing.T, grim, slurp string) (*ScreenshotHandler, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"grim": grim, "slurp": slurp} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	args := filepath.Join(dir, "args")
	t.Setenv("SCREENSHOT_TEST_ARGS", args)
	save := filepath.Join(dir, "saved shots")
	if err := os.Mkdir(save, 0o700); err != nil {
		t.Fatal(err)
	}
	return NewScreenshotHandler(&config.Config{SaveLocation: save}, state.NewState()), args
}

func TestCaptureScreenshot(t *testing.T) {
	for _, tc := range []struct{ name, output, geometry, want string }{
		{"monitor", "DP-1", "", "-o\nDP-1\n"},
		{"window", "", "-100,20 800x600", "-g\n-100,20 800x600\n"},
		{"region", "", "", "-g\n1,2 30x40\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, args := screenshotTools(t, "printf '%s\\n' \"$@\" > \"$SCREENSHOT_TEST_ARGS\"\nfor last; do :; done\nprintf png > \"$last\"\n", "echo '1,2 30x40'\n")
			file, err := h.Capture(context.Background(), 0, tc.output, tc.geometry)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(file) != h.cfg.SaveLocation || filepath.Ext(file) != ".png" {
				t.Fatalf("wrong saved path %q", file)
			}
			data, err := os.ReadFile(file) //nolint:gosec
			if err != nil || string(data) != "png" {
				t.Fatalf("screenshot not saved: %q, %v", data, err)
			}
			data, err = os.ReadFile(args) //nolint:gosec
			if err != nil || !strings.Contains(string(data), tc.want) {
				t.Fatalf("wrong grim arguments: %q, %v", data, err)
			}
		})
	}
}

func TestCaptureScreenshotFailures(t *testing.T) {
	for _, tc := range []struct{ name, output, geometry, grim, slurp string }{
		{"conflicting targets", "DP-1", "0,0 10x10", "exit 0", "exit 0"},
		{"invalid geometry", "", "0,0 0x10", "exit 0", "exit 0"},
		{"trailing geometry", "", "0,0 10x10 extra", "exit 0", "exit 0"},
		{"cancelled selection", "", "", "exit 0", "exit 1"},
		{"empty selection", "", "", "exit 0", "exit 0"},
		{"capture failure", "DP-1", "", "for last; do :; done\nprintf partial > \"$last\"\nexit 1", "exit 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := screenshotTools(t, tc.grim, tc.slurp)
			file, err := h.Capture(context.Background(), 0, tc.output, tc.geometry)
			if err == nil || file != "" {
				t.Fatalf("expected capture failure, got %q, %v", file, err)
			}
			entries, err := os.ReadDir(h.cfg.SaveLocation)
			if err != nil || len(entries) != 0 {
				t.Fatalf("partial screenshot left behind: %v, %v", entries, err)
			}
		})
	}
}

func TestCaptureScreenshotKeepsExistingFile(t *testing.T) {
	h, _ := screenshotTools(t, "for last; do :; done\nprintf new > \"$last\"\n", "exit 0")
	var existing []string
	for i := 0; i < 3; i++ {
		file := filepath.Join(h.cfg.SaveLocation, "Screenshot_"+time.Now().Add(time.Duration(i)*time.Second).Format("2006-01-02-15:04.05")+".png")
		if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		existing = append(existing, file)
	}
	file, err := h.Capture(context.Background(), 0, "DP-1", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range existing {
		data, err := os.ReadFile(old) //nolint:gosec
		if err != nil || string(data) != "original" || file == old {
			t.Fatalf("existing capture overwritten: %q, %q, %v", old, data, err)
		}
	}
}
