//go:build !nogui

package gui

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func trimDisplayedFrame(a *app) int {
	image := a.trim.media.CurrentImage()
	if a.trim.display.VisibleChildName() == "frame" {
		image = a.trim.frameImage.Paintable()
	}
	texture, ok := image.Cast().(gdk.Texturer)
	if !ok {
		return -1
	}
	downloader := gdk.NewTextureDownloader(texture)
	downloader.SetFormat(gdk.MemoryR8G8B8A8)
	stride, data := downloader.DownloadBytes()
	pixels := data.Data()
	textureBase := gdk.BaseTexture(texture)
	marker := 0
	for bit := range 8 {
		if pixels[(textureBase.Height()/2)*int(stride)+(textureBase.Width()*(2*bit+1)/16)*4] > 127 {
			marker |= 1 << bit
		}
	}
	return marker
}

func TestTrimFrameInteraction(t *testing.T) {
	a := trimGUIApp(t)
	failed := gtk.NewMediaFile()
	failed.GError(errors.New("unavailable codec"))
	for range 3 {
		if err := trimMediaError(failed); err == nil || err.Error() != "unavailable codec" {
			t.Fatalf("borrowed media error was lost: %v", err)
		}
	}
	failed.Clear()

	for _, tc := range []struct {
		name             string
		variable, offset bool
	}{{"fractional", false, false}, {"variable", true, false}, {"offset", false, true}} {
		file := frameFixture(t, "30000/1001", tc.variable)
		if tc.offset {
			target := filepath.Join(t.TempDir(), "offset.mkv")
			trimTestCommand(t, "-i", file, "-c", "copy", "-output_ts_offset", "5", target)
			file = target
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		info, err := ProbeMedia(ctx, file)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		clock, err := probeFrameClock(ctx, file)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		frames, err := loadFrameWindow(ctx, file, clock, 0, info.Duration)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		decoded := trimTestCommand(t, "-i", file, "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "rgb24", "-")
		a.openTrimSource(file, info, ModeVideo, pageSetup, nil)
		tr := &a.trim
		tr.media.SetMuted(true)
		trimGUIWait(t, func() bool {
			return trimMediaError(tr.media) != nil || (tr.media.IsPrepared() && tr.media.IsSeekable())
		})
		if err := trimMediaError(tr.media); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		waitFrame := func(index int) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("%s frame %d: position %s, texture %d, pending %v, queued %v, seeking %v, steps %d, status %s", tc.name, index, tr.playhead, trimDisplayedFrame(a), tr.framePending, tr.seekQueued, tr.media.IsSeeking(), tr.frameSteps, tr.status.Text())
				}
			}()
			expected := 0
			for bit := range 8 {
				if decoded[index*160*96*3+(48*160+10+20*bit)*3] > 127 {
					expected |= 1 << bit
				}
			}
			trimGUIWait(t, func() bool {
				if trimMediaError(tr.media) != nil {
					t.Error(trimMediaError(tr.media))
					return true
				}
				return !tr.frameImageLoading && tr.display.VisibleChildName() == "frame" && !tr.framePending && !tr.seekQueued && !tr.media.IsSeeking() && trimDisplayedFrame(a) == expected
			})
			if math.Abs(float64(tr.playhead-frames.times[index])) > float64(time.Microsecond) {
				t.Fatalf("%s: displayed frame %d has position %s", tc.name, index, tr.playhead)
			}
		}
		for range 8 {
			tr.nextFrame.Emit("clicked")
		}
		waitFrame(8)
		for range 5 {
			tr.previousFrame.Emit("clicked")
		}
		waitFrame(3)
		tr.startHere.Emit("clicked")
		waitFrame(3)
		if tr.selection.start != frames.times[3] {
			t.Fatalf("start did not include displayed frame: %s", tr.selection.start)
		}
		tr.start.GrabFocus()
		tr.start.Emit("activate")
		tr.back.GrabFocus()
		if tr.selection.start != frames.times[3] {
			t.Fatal("time field rounded the exact frame boundary")
		}
		if !a.trimFrameKeyPressed(gdk.KEY_Right, 0) {
			t.Fatal("frame shortcut was not handled")
		}
		waitFrame(4)
		tr.endHere.Emit("clicked")
		waitFrame(4)
		if tr.selection.end != frames.times[5] {
			t.Fatalf("end excluded displayed frame: %s", tr.selection.end)
		}
		tr.undo.Emit("clicked")
		if tr.selection.end != info.Duration {
			t.Fatal("frame boundary was not undoable")
		}
		tr.redo.Emit("clicked")
		waitFrame(3)
		ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
		target, _, err := exportTrim(ctx, file, false, tr.selection, nil)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		assertTrimFrameRange(t, file, target, 3, 5)
		for range 60 {
			tr.previousFrame.Emit("clicked")
		}
		waitFrame(0)
		for range 80 {
			tr.nextFrame.Emit("clicked")
		}
		waitFrame(len(frames.times) - 1)
	}
}

func TestTrimSparseAndPlayingFrames(t *testing.T) {
	a := trimGUIApp(t)
	file := filepath.Join(t.TempDir(), "sparse.mp4")
	trimTestCommand(t, "-f", "lavfi", "-i", "nullsrc=s=160x96:r=1:d=3,geq=lum='16+219*mod(floor(N/pow(2,floor(X/20))),2)':cb=128:cr=128,setpts=PTS*10", "-c:v", "libx264", "-bf", "0", "-fps_mode", "vfr", file)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	info, err := ProbeMedia(ctx, file)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	a.openTrimSource(file, info, ModeVideo, pageSetup, nil)
	tr := &a.trim
	trimGUIWait(t, func() bool {
		return trimMediaError(tr.media) != nil || (tr.media.IsPrepared() && tr.media.IsSeekable())
	})
	if err := trimMediaError(tr.media); err != nil {
		t.Fatal(err)
	}
	wait := func(marker int) {
		t.Helper()
		defer func() {
			if t.Failed() {
				t.Logf("marker %d: current %d, pos %s, loading %v/%v, queued %v, seeking %v, error %v, status %s", marker, trimDisplayedFrame(a), tr.playhead, tr.frameLoading, tr.frameImageLoading, tr.seekQueued, tr.media.IsSeeking(), trimMediaError(tr.media), tr.status.Text())
			}
		}()
		trimGUIWait(t, func() bool {
			return !tr.framePending && !tr.frameImageLoading && !tr.seekQueued && !tr.media.IsSeeking() && tr.display.VisibleChildName() == "frame" && trimDisplayedFrame(a) == marker
		})
	}
	tr.nextFrame.Emit("clicked")
	wait(1)
	if tr.playhead != 10*time.Second {
		t.Fatalf("sparse next: %s", tr.playhead)
	}
	tr.endHere.Emit("clicked")
	wait(1)
	if tr.selection.end != 20*time.Second {
		t.Fatalf("sparse next boundary: %s", tr.selection.end)
	}
	tr.previousFrame.Emit("clicked")
	wait(0)
	for range 6 {
		tr.nextFrame.Emit("clicked")
	}
	wait(2)
	tr.endHere.Emit("clicked")
	wait(2)
	if tr.selection.end != info.Duration {
		t.Fatal("sparse last frame was not clamped")
	}
	a.seekTrim(0)
	a.stepTrimFrame(1)
	a.cancelTrimFrameNavigation()
	a.cancelTrimFrameImage()
	if tr.framePending || tr.frameLoading || tr.frameImageLoading {
		t.Fatal("frame navigation did not cancel")
	}

	file = frameFixture(t, "60", false)
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
	info, err = ProbeMedia(ctx, file)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	a.openTrimSource(file, info, ModeVideo, pageSetup, nil)
	tr.media.SetMuted(true)
	trimGUIWait(t, func() bool { return tr.media.IsPrepared() && tr.media.IsSeekable() })
	glib.SourceRemove(tr.timer)
	tr.timer = 0
	tr.media.Play()
	trimGUIWait(t, func() bool { return trimDisplayedFrame(a) >= 4 })
	before := trimDisplayedFrame(a)
	tr.playhead = 0
	a.stepTrimFrame(1)
	tick := glib.TimeoutAdd(10, a.tickTrim)
	wait(before + 1)
	glib.SourceRemove(tick)
	a.toggleTrimPlayback()
	trimGUIWait(t, func() bool { return trimDisplayedFrame(a) >= before+5 })
	before = trimDisplayedFrame(a)
	tr.playhead = 0
	a.stepTrimFrame(-1)
	tick = glib.TimeoutAdd(10, a.tickTrim)
	wait(before - 1)
	glib.SourceRemove(tick)
	a.seekTrim(400 * time.Millisecond)
	a.stepTrimFrame(1)
	a.stepTrimFrame(1)
	tick = glib.TimeoutAdd(10, a.tickTrim)
	wait(26)
	glib.SourceRemove(tick)
}
