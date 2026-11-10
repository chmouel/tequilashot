package commands

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/chmouel/tequilashot/internal/external"
)

// barClaims counts how often captures hold and release the status bar.
type barClaims struct {
	mu       sync.Mutex
	acquired int
	released int
}

func (c *barClaims) counts() (acquired, released int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acquired, c.released
}

type fakeClaim struct {
	claims *barClaims
	once   sync.Once
}

func (f *fakeClaim) Restore() {
	f.once.Do(func() {
		f.claims.mu.Lock()
		defer f.claims.mu.Unlock()
		f.claims.released++
	})
}

// stubStatusBar replaces the real Waybar with a counter for the test.
func stubStatusBar(t *testing.T) *barClaims {
	t.Helper()
	claims := &barClaims{}
	orig := hideStatusBar
	hideStatusBar = func() statusBarHider {
		claims.mu.Lock()
		defer claims.mu.Unlock()
		claims.acquired++
		return &fakeClaim{claims: claims}
	}
	t.Cleanup(func() { hideStatusBar = orig })
	return claims
}

// expectClaims waits for the counts to settle, as a recorder's claim is
// released from the goroutine that waits for it.
func expectClaims(t *testing.T, claims *barClaims, acquired, released int) {
	t.Helper()
	var gotAcquired, gotReleased int
	for range 300 {
		if gotAcquired, gotReleased = claims.counts(); gotAcquired == acquired && gotReleased == released {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status bar acquired %d and released %d times, want %d and %d", gotAcquired, gotReleased, acquired, released)
}

func TestHoldStatusBarOnlyForWholeScreen(t *testing.T) {
	claims := stubStatusBar(t)

	holdStatusBar(false).Restore()
	expectClaims(t, claims, 0, 0)

	holdStatusBar(true).Restore()
	expectClaims(t, claims, 1, 1)
}

func TestScreenshotHoldsStatusBarForWholeScreenOnly(t *testing.T) {
	grim := "for last; do :; done\nprintf png > \"$last\"\n"
	for _, tc := range []struct {
		name, output, geometry string
		acquired               int
	}{
		{"monitor", "DP-1", "", 1},
		{"window", "", "-100,20 800x600", 0},
		{"region", "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := stubStatusBar(t)
			h, _ := screenshotTools(t, grim, "echo '1,2 30x40'\n")
			if _, err := h.Capture(context.Background(), 0, tc.output, tc.geometry); err != nil {
				t.Fatal(err)
			}
			expectClaims(t, claims, tc.acquired, tc.acquired)
		})
	}
}

func TestScreenshotReleasesStatusBarWhenGrimFails(t *testing.T) {
	claims := stubStatusBar(t)
	h, _ := screenshotTools(t, "exit 1\n", "echo '1,2 30x40'\n")
	if _, err := h.Capture(context.Background(), 0, "DP-1", ""); err == nil {
		t.Fatal("capture succeeded")
	}
	expectClaims(t, claims, 1, 1)
}

func TestRecordingHoldsStatusBarForWholeScreenOnly(t *testing.T) {
	for _, hideBar := range []bool{true, false} {
		claims := stubStatusBar(t)
		stubRecorder(t, "exec sleep 30\n")
		h, st := newTestHandler(t)
		if err := h.startRecording(context.Background(), "", "DP-1", RecordOptions{}, hideBar); err != nil {
			t.Fatal(err)
		}
		want := 0
		if hideBar {
			want = 1
		}
		expectClaims(t, claims, want, 0)

		_ = syscall.Kill(st.GetRecordingPID(), syscall.SIGKILL)
		expectClaims(t, claims, want, want)
	}
}

func TestRecordingReleasesStatusBarWhenStartFails(t *testing.T) {
	t.Run("recorder exits at once", func(t *testing.T) {
		claims := stubStatusBar(t)
		stubRecorder(t, "touch \"$out\"\necho 'camera busy' >&2\nexit 1\n")
		h, _ := newTestHandler(t)
		if err := h.startRecording(context.Background(), "", "DP-1", RecordOptions{}, true); err == nil {
			t.Fatal("start succeeded")
		}
		expectClaims(t, claims, 1, 1)
	})

	t.Run("camera cannot be opened", func(t *testing.T) {
		claims := stubStatusBar(t)
		stubRecorder(t, "exec sleep 30\n")
		h, _ := newTestHandler(t)
		orig := startBubble
		startBubble = func(external.CameraOverlay, int, int, int, image.Rectangle) (stopper, error) {
			return nil, errors.New("camera /dev/video0 could not be opened")
		}
		t.Cleanup(func() { startBubble = orig })
		opts := RecordOptions{Camera: external.CameraOverlay{Device: "/dev/video0", Shape: external.CameraCircle}}
		if err := h.startRecording(context.Background(), "10,20 800x600", "", opts, true); err == nil {
			t.Fatal("start succeeded")
		}
		expectClaims(t, claims, 1, 1)
	})

	t.Run("cache file cannot be written", func(t *testing.T) {
		claims := stubStatusBar(t)
		stubRecorder(t, "exec sleep 30\n")
		h, _ := newTestHandler(t)
		h.cfg.CacheFile = filepath.Join(h.cfg.SaveLocation, "missing", "cache")
		if err := h.startRecording(context.Background(), "", "DP-1", RecordOptions{}, true); err == nil {
			t.Fatal("start succeeded")
		}
		if _, err := os.Stat(h.cfg.CacheFile); err == nil {
			t.Fatal("cache file written")
		}
		expectClaims(t, claims, 1, 1)
	})
}

func TestVirtualCameraHoldsStatusBarForWholeScreenOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts VirtualCameraOptions
		want int
	}{
		{"screen", VirtualCameraOptions{Output: "DP-2"}, 1},
		{"focused screen", VirtualCameraOptions{}, 1},
		{"region", VirtualCameraOptions{Geometry: "1,2 300x200"}, 0},
		{"window", VirtualCameraOptions{CurrentWindow: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := stubStatusBar(t)
			stubVirtualCamera(t, newFakeLive(), nil)
			h, _ := newVirtualHandler()
			if err := h.Start(context.Background(), tc.opts); err != nil {
				t.Fatal(err)
			}
			expectClaims(t, claims, tc.want, 0)
			if err := h.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			expectClaims(t, claims, tc.want, tc.want)
		})
	}
}

func TestVirtualCameraReleasesStatusBarWhenStartFails(t *testing.T) {
	claims := stubStatusBar(t)
	stubVirtualCamera(t, nil, errors.New("the virtual camera stopped at once: no output"))
	h, _ := newVirtualHandler()
	if err := h.Start(context.Background(), VirtualCameraOptions{Output: "DP-2"}); err == nil {
		t.Fatal("start succeeded")
	}
	expectClaims(t, claims, 1, 1)
}
