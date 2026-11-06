//go:build !nogui

package gui

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func TestTrimPlayback(t *testing.T) {
	if os.Getenv("TEQUILASHOT_TEST_GUI") != "1" {
		t.Skip("set TEQUILASHOT_TEST_GUI=1 to check GTK playback")
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	source := filepath.Join(t.TempDir(), "long-gop.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "testsrc2=s=640x360:r=30:d=12", "-f", "lavfi", "-i", "sine=frequency=440:duration=12",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "300", "-keyint_min", "300", "-sc_threshold", "0", "-c:a", "aac", source)
	if !gtk.InitCheck() {
		t.Fatal("GTK needs a display; run this check under xvfb-run")
	}
	media := gtk.NewMediaFileForFilename(source)
	media.SetMuted(true)
	video := gtk.NewVideoForMediaStream(media)
	a := &app{pages: gtk.NewStack()}
	a.trim.media = media
	a.trim.video = video
	a.trim.timeline = gtk.NewDrawingArea()
	a.trim.play = gtk.NewButton()
	a.trim.preview = gtk.NewButton()
	a.trim.position = gtk.NewLabel("")
	a.trim.selection.duration = 12 * time.Second
	a.pages.AddNamed(video, pageTrim)
	a.pages.SetVisibleChildName(pageTrim)
	win := gtk.NewWindow()
	win.SetChild(a.pages)
	win.Present()
	defer win.Destroy()
	defer media.Clear()
	defer media.Pause()
	loop := glib.NewMainLoop(nil, false)
	seeks := 0
	media.NotifyProperty("seeking", func() {
		if media.IsSeeking() {
			seeks++
		}
	})
	stage, requests := 0, 0
	deadline := time.Now().Add(12 * time.Second)
	glib.TimeoutAdd(10, func() bool {
		fail := func(message string) bool {
			t.Errorf("stage %d: %s (position %.6f, %d seeks)", stage, message, float64(media.Timestamp())/1e6, seeks)
			loop.Quit()
			return false
		}
		if time.Now().After(deadline) {
			return fail("playback did not advance")
		}
		if err := trimMediaError(media); err != nil {
			return fail(err.Error())
		}
		if !media.IsPrepared() {
			return true
		}
		a.tickTrim()
		switch stage {
		case 0:
			a.seekTrim(3*time.Second + time.Duration(requests)*100*time.Millisecond)
			requests++
			if requests == 40 {
				a.seekTrim(7250 * time.Millisecond)
				stage = 1
			}
		case 1:
			if a.trim.seekQueued || media.IsSeeking() {
				return true
			}
			if math.Abs(float64(media.Timestamp())/1e6-7.25) > 0.01 || media.Playing() || seeks > 10 {
				return fail("scrubbing did not settle at the latest requested position")
			}
			seeks = 0
			// Dragged boundaries can fall between the player's microsecond timestamps
			a.trim.selection = trimSelection{400 * time.Millisecond, 9700*time.Millisecond + 999*time.Nanosecond, 12 * time.Second, true}
			a.previewTrim()
			stage = 2
		case 2:
			if media.Timestamp() < 10100000 || a.trim.seekQueued || media.IsSeeking() {
				return true
			}
			if seeks != 2 {
				return fail("preview repeatedly sought the cut boundary")
			}
			seeks = 0
			a.trim.selection = trimSelection{2350 * time.Millisecond, 2750 * time.Millisecond, 12 * time.Second, false}
			a.previewTrim()
			stage = 3
		case 3:
			if a.trim.previewing || a.trim.seekQueued || media.IsSeeking() {
				return true
			}
			if media.Playing() || media.Timestamp() < 2750000 || media.Timestamp() > 2900000 || seeks != 1 {
				return fail("keep preview did not stop at the selected end")
			}
			stage = 4
			loop.Quit()
			return false
		}
		return true
	})
	loop.Run()
	if stage != 4 {
		t.Fatalf("playback check stopped at stage %d", stage)
	}
}
