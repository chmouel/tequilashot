//go:build !nogui

package gui

import (
	"context"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func (a *app) cancelTrimFrameNavigation() {
	t := &a.trim
	t.frameGen++
	if t.framePauseTimer != 0 {
		glib.SourceRemove(t.framePauseTimer)
		t.framePauseTimer = 0
	}
	if t.frameCancel != nil {
		t.frameCancel()
		t.frameCancel = nil
	}
	t.frameLoading, t.framePending = false, false
	t.frameSteps, t.frameAction = 0, 0
}

func (a *app) stepTrimFrame(step int) {
	t := &a.trim
	if t.export != nil || t.media == nil || !t.media.IsSeekable() {
		return
	}
	wasPlaying := t.media.Playing()
	t.media.Pause()
	t.previewing, t.playOnSeek = false, false
	if !t.framePending {
		switch {
		case t.frameImageLoading || t.display.VisibleChildName() == "frame":
			t.frameAnchor = t.frameImageAt
		case t.seekQueued || t.media.IsSeeking():
			t.frameAnchor = t.playhead
		default:
			t.frameAnchor = time.Duration(t.media.Timestamp()) * time.Microsecond
			if wasPlaying {
				gen, request := t.gen, t.frameGen
				before := t.media.Timestamp()
				deadline := time.Now().Add(200 * time.Millisecond)
				// The backend publishes its final position after Pause returns
				t.framePauseTimer = glib.TimeoutAdd(10, func() bool {
					if gen == t.gen && request == t.frameGen && !a.closing && t.media.Timestamp() == before && time.Now().Before(deadline) {
						return true
					}
					t.framePauseTimer = 0
					if a.closing || gen != t.gen || request != t.frameGen {
						return false
					}
					t.frameAnchor = time.Duration(t.media.Timestamp()) * time.Microsecond
					a.resolveTrimFrame()
					return false
				})
			}
		}
		t.framePending = true
	}
	t.frameSteps += step
	t.frameAction = 0
	a.resolveTrimFrame()
}

func (a *app) markTrimFrame(end bool) {
	t := &a.trim
	if t.export != nil || t.media == nil || !t.media.IsSeekable() || t.seekQueued || t.media.IsSeeking() || t.frameImageLoading {
		return
	}
	if t.media.Playing() {
		a.stepTrimFrame(0)
		t.frameAction = 1
		if end {
			t.frameAction = 2
		}
		return
	}
	t.media.Pause()
	t.previewing, t.playOnSeek = false, false
	if t.display.VisibleChildName() == "frame" {
		t.playhead = t.frameImageAt
	} else {
		t.playhead = time.Duration(t.media.Timestamp()) * time.Microsecond
	}
	t.frameAnchor = t.playhead
	t.framePending = true
	t.frameSteps = 0
	t.frameAction = 1
	if end {
		t.frameAction = 2
	}
	a.resolveTrimFrame()
}

func (a *app) resolveTrimFrame() {
	t := &a.trim
	if t.frameLoading || t.framePauseTimer != 0 || !t.framePending {
		return
	}
	w := t.frames
	if len(w.times) > 0 && (t.frameAnchor >= w.times[0] || w.first) && (t.frameAnchor <= w.times[len(w.times)-1] || w.last) {
		current := w.index(t.frameAnchor)
		next := current + t.frameSteps
		index := max(0, min(len(w.times)-1, next))
		if (next >= 0 || w.first) && (next < len(w.times)-1 || w.last) {
			at := w.times[index]
			end := t.selection.duration
			if index+1 < len(w.times) {
				end = w.times[index+1]
			}
			action := t.frameAction
			t.framePending = false
			t.frameSteps, t.frameAction = 0, 0
			if action != 0 {
				a.commitTrimEdit()
				if action == 1 {
					t.selection.start = at
					if t.selection.end <= at {
						t.selection.end = end
					}
				} else {
					t.selection.end = end
					if t.selection.start >= end {
						t.selection.start = at
					}
				}
				a.updateTrim()
				a.commitTrimEdit()
			}
			a.seekTrim(at)
			a.loadTrimFrameImage(at)
			return
		}
		t.frameAnchor = w.times[index]
		t.frameSteps -= index - current
	}
	t.frameLoading = true
	gen, request, file, at, duration := t.gen, t.frameGen, t.file, t.frameAnchor, t.selection.duration
	clock := t.frameClock
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.frameCancel = cancel
	go func() {
		defer cancel()
		var err error
		if clock == nil {
			clock, err = probeFrameClock(ctx, file)
		}
		var window frameWindow
		if err == nil {
			window, err = loadFrameWindow(ctx, file, clock, at, duration)
		}
		glib.IdleAdd(func() {
			if a.closing || gen != t.gen || request != t.frameGen {
				return
			}
			t.frameLoading = false
			t.frameCancel = nil
			if err != nil {
				t.framePending = false
				t.frameSteps, t.frameAction = 0, 0
				t.status.SetText("Frame stepping is unavailable: " + err.Error())
				t.status.SetVisible(true)
				return
			}
			t.frameClock, t.frames = clock, window
			a.resolveTrimFrame()
		})
	}()
}

func (a *app) trimFrameKeys() *gtk.EventControllerKey {
	key := gtk.NewEventControllerKey()
	key.ConnectKeyPressed(func(value, _ uint, mods gdk.ModifierType) bool { return a.trimFrameKeyPressed(value, mods) })
	return key
}

func (a *app) trimFrameKeyPressed(value uint, mods gdk.ModifierType) bool {
	t := &a.trim
	if t.media == nil || t.export != nil || mods&(gdk.ControlMask|gdk.AltMask|gdk.SuperMask) != 0 {
		return false
	}
	switch value {
	case gdk.KEY_Left, gdk.KEY_Right:
		direction := 1
		if value == gdk.KEY_Left {
			direction = -1
		}
		if mods&gdk.ShiftMask != 0 {
			a.seekTrim(t.playhead + time.Duration(direction)*time.Second)
		} else {
			a.stepTrimFrame(direction)
		}
	case gdk.KEY_Home:
		a.seekTrim(0)
	case gdk.KEY_End:
		a.seekTrim(t.selection.duration)
	default:
		return false
	}
	return true
}

func (a *app) syncTrimFrameControls() {
	t := &a.trim
	if t.previousFrame == nil {
		return
	}
	ready := t.media != nil && t.media.IsSeekable() && trimMediaError(t.media) == nil && t.export == nil
	t.previousFrame.SetSensitive(ready)
	t.nextFrame.SetSensitive(ready)
	t.startHere.SetSensitive(ready && !t.frameLoading && !t.frameImageLoading && !t.seekQueued && !t.media.IsSeeking())
	t.endHere.SetSensitive(ready && !t.frameLoading && !t.frameImageLoading && !t.seekQueued && !t.media.IsSeeking())
}

func (a *app) cancelTrimFrameImage() {
	t := &a.trim
	t.frameImageGen++
	if t.frameImageCancel != nil {
		t.frameImageCancel()
		t.frameImageCancel = nil
	}
	t.frameImageLoading = false
	if t.display != nil {
		focused := t.frameImage.StateFlags()&gtk.StateFlagFocusWithin != 0
		t.display.SetVisibleChildName("video")
		if focused {
			t.video.GrabFocus()
		}
		t.frameImage.SetPaintable(nil)
	}
}

func (a *app) loadTrimFrameImage(at time.Duration) {
	t := &a.trim
	a.cancelTrimFrameImage()
	t.frameImageLoading = true
	t.frameImageAt = at
	gen, request, file := t.gen, t.frameImageGen, t.file
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.frameImageCancel = cancel
	go func() {
		defer cancel()
		data, err := extractTrimFrame(ctx, file, at)
		glib.IdleAdd(func() {
			if a.closing || gen != t.gen || request != t.frameImageGen {
				return
			}
			t.frameImageLoading = false
			t.frameImageCancel = nil
			if err != nil {
				t.status.SetText("The exact frame could not be shown: " + err.Error())
				t.status.SetVisible(true)
				return
			}
			texture, err := gdk.NewTextureFromBytes(glib.NewBytes(data))
			if err != nil {
				t.status.SetText("The exact frame could not be shown: " + err.Error())
				t.status.SetVisible(true)
				return
			}
			focused := t.video.StateFlags()&gtk.StateFlagFocusWithin != 0
			t.frameImage.SetPaintable(texture)
			t.display.SetVisibleChildName("frame")
			if focused {
				t.frameImage.GrabFocus()
			}
			t.playhead = at
		})
	}()
}

func trimMediaError(media *gtk.MediaFile) error {
	// The generated Error getter frees GTK's borrowed GError
	err, _ := media.ObjectProperty("error").(error)
	return err
}
