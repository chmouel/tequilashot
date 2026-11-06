//go:build !nogui

package gui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

const pageTrim = "trim"

type trimView struct {
	shortStart, shortEnd, shortCut               *gtk.Button
	previewSegments                              []trimSegment
	framePauseTimer                              glib.SourceHandle
	display                                      *gtk.Stack
	frameImage                                   *gtk.Picture
	frameImageAt                                 time.Duration
	frameImageLoading                            bool
	frameImageCancel                             context.CancelFunc
	frameImageGen                                int
	previousFrame, nextFrame, startHere, endHere *gtk.Button
	frameClock                                   *frameClock
	frames                                       frameWindow
	frameAnchor                                  time.Duration
	frameSteps, frameAction, frameGen            int
	frameLoading, framePending                   bool
	frameCancel                                  context.CancelFunc
	history                                      trimHistory
	undo, redo                                   *gtk.Button
	file, mode, returnPage                       string
	info                                         MediaInfo
	video                                        *gtk.Video
	media                                        *gtk.MediaFile
	timeline                                     *gtk.DrawingArea
	thumbs                                       []*gtk.Picture
	start, end                                   *gtk.SpinButton
	keep                                         *gtk.ToggleButton
	play                                         *gtk.Button
	preview                                      *gtk.Button
	save                                         *gtk.Button
	back                                         *gtk.Button
	reset                                        *gtk.Button
	controls                                     *gtk.Box
	progress                                     *gtk.ProgressBar
	caption                                      *gtk.Label
	position                                     *gtk.Label
	name                                         *gtk.Label
	status                                       *gtk.Label
	selection                                    trimSelection
	syncing                                      bool
	previewing                                   bool
	segment                                      int
	playhead                                     time.Duration
	seekTarget                                   time.Duration
	seekQueued                                   bool
	lastSeek                                     time.Time
	playOnSeek                                   bool
	timer                                        glib.SourceHandle
	cancel                                       context.CancelFunc
	export                                       context.CancelFunc
	done                                         chan struct{}
	gen                                          int
}

func (a *app) buildTrim() *gtk.Box {
	t := &a.trim
	page := gtk.NewBox(gtk.OrientationVertical, 10)
	page.SetMarginStart(18)
	page.SetMarginEnd(18)
	page.SetMarginBottom(18)
	t.name = label("", "heading")
	t.name.SetEllipsize(pango.EllipsizeMiddle)
	page.Append(t.name)
	t.video = gtk.NewVideo()
	t.video.SetSizeRequest(480, 270)
	t.video.SetFocusable(true)
	t.video.AddController(a.trimFrameKeys())
	t.video.AddCSSClass("er-frame")
	t.video.SetOverflow(gtk.OverflowHidden)
	t.display = gtk.NewStack()
	t.display.SetSizeRequest(480, 270)
	t.display.AddNamed(t.video, "video")
	t.frameImage = gtk.NewPicture()
	t.frameImage.SetCanShrink(true)
	t.frameImage.SetContentFit(gtk.ContentFitContain)
	t.frameImage.SetFocusable(true)
	t.frameImage.AddController(a.trimFrameKeys())
	t.display.AddNamed(t.frameImage, "frame")
	page.Append(t.display)

	t.controls = gtk.NewBox(gtk.OrientationVertical, 10)
	modes := gtk.NewBox(gtk.OrientationHorizontal, 0)
	modes.AddCSSClass("linked")
	modes.SetHAlign(gtk.AlignCenter)
	t.keep = gtk.NewToggleButtonWithLabel("Keep selection")
	remove := gtk.NewToggleButtonWithLabel("Remove selection")
	remove.SetGroup(t.keep)
	t.keep.SetActive(true)
	t.keep.ConnectToggled(func() {
		if t.syncing {
			return
		}
		a.commitTrimEdit()
		t.selection.remove = !t.keep.Active()
		a.updateTrim()
		a.commitTrimEdit()
	})
	modes.Append(t.keep)
	modes.Append(remove)
	t.controls.Append(modes)
	t.controls.Append(a.buildTrimTimeline())

	times := gtk.NewBox(gtk.OrientationHorizontal, 12)
	times.SetHomogeneous(true)
	for i, title := range []string{"Start (seconds)", "End (seconds)"} {
		col := gtk.NewBox(gtk.OrientationVertical, 4)
		caption := label(title, "caption")
		caption.SetXAlign(0)
		col.Append(caption)
		spin := gtk.NewSpinButtonWithRange(0, 1, 0.1)
		spin.SetDigits(3)
		spin.SetTooltipText(title + "; use the arrows or enter a precise time")
		spin.SetHExpand(true)
		col.Append(spin)
		times.Append(col)
		if i == 0 {
			t.start = spin
		} else {
			t.end = spin
		}
		spin.ConnectValueChanged(func() {
			if t.syncing {
				return
			}
			previous := t.selection.start
			if spin == t.end {
				previous = t.selection.end
			}
			at := time.Duration(spin.Value() * float64(time.Second))
			// SpinButton reparses its rounded display when focus leaves
			if at == previous.Round(time.Millisecond) && at != previous {
				t.syncing = true
				spin.SetValue(previous.Seconds())
				t.syncing = false
				return
			}
			if spin == t.start {
				t.selection.start = at
			} else {
				t.selection.end = at
			}
			a.updateTrim()
			at = t.selection.start
			if spin == t.end {
				at = t.selection.end
			}
			a.seekTrim(at)
			if spin.StateFlags()&gtk.StateFlagFocusWithin == 0 {
				a.commitTrimEdit()
			}
		})
		spin.ConnectActivate(func() { spin.Update(); a.commitTrimEdit() })
		focus := gtk.NewEventControllerFocus()
		focus.ConnectLeave(func() { spin.Update(); a.commitTrimEdit() })
		spin.AddController(focus)
	}
	t.controls.Append(times)

	playback := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t.play = gtk.NewButtonWithLabel("Play")
	t.play.SetTooltipText("Play or pause (Space)")
	t.play.ConnectClicked(a.toggleTrimPlayback)
	playback.Append(t.play)
	t.preview = gtk.NewButtonWithLabel("Preview edit")
	t.preview.ConnectClicked(a.previewTrim)
	playback.Append(t.preview)
	t.position = label("", "caption", "dim-label")
	t.position.SetHExpand(true)
	t.position.SetXAlign(1)
	playback.Append(t.position)
	t.controls.Append(playback)
	edges := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t.shortStart = gtk.NewButtonWithLabel("Preview start")
	t.shortStart.ConnectClicked(func() { a.startTrimPreview(trimPreviewStart) })
	edges.Append(t.shortStart)
	t.shortEnd = gtk.NewButtonWithLabel("Preview end")
	t.shortEnd.ConnectClicked(func() { a.startTrimPreview(trimPreviewEnd) })
	edges.Append(t.shortEnd)
	t.shortCut = gtk.NewButtonWithLabel("Preview cut")
	t.shortCut.ConnectClicked(func() { a.startTrimPreview(trimPreviewCut) })
	edges.Append(t.shortCut)
	t.controls.Append(edges)
	frames := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t.previousFrame = gtk.NewButtonWithLabel("Previous frame")
	t.previousFrame.SetTooltipText("Previous frame (Left)")
	t.previousFrame.ConnectClicked(func() { a.stepTrimFrame(-1) })
	frames.Append(t.previousFrame)
	t.nextFrame = gtk.NewButtonWithLabel("Next frame")
	t.nextFrame.SetTooltipText("Next frame (Right)")
	t.nextFrame.ConnectClicked(func() { a.stepTrimFrame(1) })
	frames.Append(t.nextFrame)
	t.startHere = gtk.NewButtonWithLabel("Start here")
	t.startHere.ConnectClicked(func() { a.markTrimFrame(false) })
	frames.Append(t.startHere)
	t.endHere = gtk.NewButtonWithLabel("End here")
	t.endHere.ConnectClicked(func() { a.markTrimFrame(true) })
	frames.Append(t.endHere)
	t.controls.Append(frames)
	t.caption = label("", "caption")
	t.caption.SetWrap(true)
	t.controls.Append(t.caption)
	page.Append(t.controls)

	t.status = label("", "caption", "dim-label")
	t.status.SetWrap(true)
	t.status.SetVisible(false)
	page.Append(t.status)
	t.progress = gtk.NewProgressBar()
	t.progress.SetShowText(true)
	t.progress.SetVisible(false)
	page.Append(t.progress)
	footer := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t.back = gtk.NewButtonWithLabel("Back")
	t.back.ConnectClicked(func() {
		if t.export != nil {
			t.export()
			t.back.SetSensitive(false)
			t.progress.SetText("Cancelling…")
			return
		}
		a.leaveTrim()
	})
	footer.Append(t.back)
	t.reset = gtk.NewButtonWithLabel("Reset")
	t.reset.ConnectClicked(func() {
		if t.export != nil {
			return
		}
		a.commitTrimEdit()
		t.selection.start, t.selection.end = 0, t.selection.duration
		a.updateTrim()
		a.seekTrim(0)
		a.commitTrimEdit()
	})
	footer.Append(t.reset)
	t.undo = gtk.NewButtonWithLabel("Undo")
	t.undo.SetTooltipText("Undo the last edit (Ctrl+Z)")
	t.undo.ConnectClicked(func() { a.restoreTrimEdit(false) })
	footer.Append(t.undo)
	t.redo = gtk.NewButtonWithLabel("Redo")
	t.redo.SetTooltipText("Redo the last edit (Ctrl+Shift+Z)")
	t.redo.ConnectClicked(func() { a.restoreTrimEdit(true) })
	footer.Append(t.redo)
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	footer.Append(spacer)
	t.save = gtk.NewButtonWithLabel("Save copy")
	t.save.AddCSSClass("suggested-action")
	t.save.SetTooltipText("Save beside the original, which is kept")
	t.save.ConnectClicked(a.saveTrim)
	footer.Append(t.save)
	page.Append(footer)
	return page
}

func (a *app) buildTrimTimeline() *gtk.Overlay {
	t := &a.trim
	strip := gtk.NewBox(gtk.OrientationHorizontal, 0)
	strip.SetHomogeneous(true)
	strip.SetMarginStart(10)
	strip.SetMarginEnd(10)
	strip.SetMarginTop(8)
	strip.SetMarginBottom(8)
	for range 8 {
		pic := gtk.NewPicture()
		pic.SetContentFit(gtk.ContentFitCover)
		pic.SetCanShrink(true)
		pic.SetSizeRequest(1, 56)
		strip.Append(pic)
		t.thumbs = append(t.thumbs, pic)
	}
	track := gtk.NewOverlay()
	track.AddCSSClass("er-frame")
	track.SetChild(strip)
	t.timeline = gtk.NewDrawingArea()
	t.timeline.SetContentHeight(76)
	t.timeline.SetFocusable(true)
	t.timeline.SetTooltipText("Drag the handles to select a section; click elsewhere to seek")
	t.timeline.SetDrawFunc(a.drawTrimTimeline)
	track.AddOverlay(t.timeline)

	drag := gtk.NewGestureDrag()
	drag.SetButton(1)
	var origin float64
	var handle int
	drag.ConnectDragBegin(func(x, _ float64) {
		a.commitTrimEdit()
		t.timeline.GrabFocus()
		origin, handle = x, 0
		d := float64(t.selection.duration)
		w := float64(t.timeline.Width() - 20)
		if d <= 0 || w <= 0 {
			return
		}
		x1, x2 := 10+w*float64(t.selection.start)/d, 10+w*float64(t.selection.end)/d
		if math.Abs(x-x1) <= 14 && math.Abs(x-x1) <= math.Abs(x-x2) {
			handle = 1
		} else if math.Abs(x-x2) <= 14 {
			handle = 2
		}
		if handle == 0 {
			a.seekTrim(a.trimTimeAt(x))
		} else {
			t.timeline.SetCursorFromName("ew-resize")
		}
	})
	drag.ConnectDragUpdate(func(dx, _ float64) {
		at := a.trimTimeAt(origin + dx)
		gap := min(time.Millisecond, t.selection.duration)
		switch handle {
		case 1:
			t.selection.start = max(0, min(at, t.selection.end-gap))
		case 2:
			t.selection.end = min(t.selection.duration, max(at, t.selection.start+gap))
		}
		if handle != 0 {
			a.updateTrim()
			if handle == 1 {
				at = t.selection.start
			} else {
				at = t.selection.end
			}
		}
		a.seekTrim(at)
	})
	finishDrag := func() { t.timeline.SetCursorFromName("default"); a.commitTrimEdit() }
	drag.ConnectDragEnd(func(_, _ float64) { finishDrag() })
	drag.ConnectCancel(func(_ *gdk.EventSequence) { finishDrag() })
	t.timeline.AddController(drag)
	t.timeline.AddController(a.trimFrameKeys())
	return track
}

func (a *app) trimTimeAt(x float64) time.Duration {
	w := float64(a.trim.timeline.Width() - 20)
	if w <= 0 {
		return 0
	}
	return time.Duration(max(0, min(1, (x-10)/w)) * float64(a.trim.selection.duration))
}

func (a *app) drawTrimTimeline(_ *gtk.DrawingArea, cr *cairo.Context, width, height int) {
	t := &a.trim
	d := float64(t.selection.duration)
	if d <= 0 {
		return
	}
	w, h := float64(width-20), float64(height)
	x1, x2 := 10+w*float64(t.selection.start)/d, 10+w*float64(t.selection.end)/d
	cr.SetSourceRGBA(0, 0, 0, 0.7)
	if t.selection.remove {
		cr.Rectangle(x1, 8, x2-x1, h-16)
	} else {
		cr.Rectangle(10, 8, x1-10, h-16)
		cr.Rectangle(x2, 8, float64(width)-10-x2, h-16)
	}
	cr.Fill()
	if t.selection.remove {
		cr.SetSourceRGBA(1, 0.54, 0.5, 1)
	} else {
		cr.SetSourceRGBA(0.48, 0.79, 1, 1)
	}
	cr.SetLineWidth(2)
	cr.Rectangle(x1, 7, x2-x1, h-14)
	cr.Stroke()
	for _, x := range []float64{x1, x2} {
		cr.Rectangle(x-5, 4, 10, h-8)
		cr.Fill()
	}
	cr.SetSourceRGBA(0.08, 0.12, 0.18, 1)
	for _, x := range []float64{x1, x2} {
		cr.Rectangle(x-1, h/2-8, 2, 16)
		cr.Fill()
	}
	if t.media != nil {
		x := 10 + w*float64(t.playhead)/d
		cr.SetSourceRGBA(1, 1, 1, 1)
		cr.SetLineWidth(2)
		cr.MoveTo(x, 0)
		cr.LineTo(x, h)
		cr.Stroke()
	}
}

func (a *app) openTrim() {
	if a.res.info.Duration <= 0 {
		a.toast("The recording's length could not be read. FFprobe is required for trimming.")
		return
	}
	a.openTrimSource(a.res.file, a.res.info, a.res.mode, pageResult, nil)
}

func (a *app) openTrimSource(file string, info MediaInfo, mode, returnPage string, prepared *gtk.MediaFile) {
	a.stopTrim()
	t := &a.trim
	t.file, t.info, t.mode, t.returnPage = file, info, mode, returnPage
	t.gen++
	t.playhead = 0
	t.seekQueued, t.playOnSeek = false, false
	t.lastSeek = time.Time{}
	t.selection = trimSelection{end: info.Duration, duration: info.Duration}
	t.history.reset(t.selection)
	t.syncing = true
	t.start.SetRange(0, t.selection.duration.Seconds())
	t.end.SetRange(0, t.selection.duration.Seconds())
	t.keep.SetActive(true)
	t.syncing = false
	t.status.SetVisible(false)
	t.name.SetText(filepath.Base(file))
	t.media = prepared
	if t.media == nil {
		t.media = gtk.NewMediaFileForFilename(file)
	}
	media := t.media
	showPreviewError := func() {
		if t.media == media && trimMediaError(media) != nil {
			t.status.SetText("The video preview could not be opened. Please check that GTK media support and the recording's codecs are installed.")
			t.status.SetVisible(true)
		}
	}
	media.NotifyProperty("error", showPreviewError)
	showPreviewError()
	t.video.SetMediaStream(t.media)
	a.pages.SetVisibleChildName(pageTrim)
	a.win.SetTitle("Trim video")
	a.updateTrim()
	t.timer = glib.TimeoutAdd(40, a.tickTrim)
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	a.loadTrimThumbnails(ctx)
	t.back.GrabFocus()
}

func (a *app) updateTrim() {
	a.cancelTrimFrameNavigation()
	t := &a.trim
	if t.media != nil {
		t.media.Pause()
	}
	t.syncing = true
	t.start.SetValue(t.selection.start.Seconds())
	t.end.SetValue(t.selection.end.Seconds())
	t.syncing = false
	t.previewing = false
	t.previewSegments = nil
	t.playOnSeek = false
	a.syncTrimShortPreviews()
	segments, err := t.selection.segments()
	valid := err == nil
	changed := t.selection.remove || t.selection.start > 0 || t.selection.end < t.selection.duration
	t.save.SetSensitive(valid && changed && t.export == nil)
	t.preview.SetSensitive(valid)
	if err != nil {
		t.caption.SetText(err.Error())
	} else {
		verb := "Keep"
		if t.selection.remove {
			verb = "Remove"
		}
		t.caption.SetText(fmt.Sprintf("%s %s–%s · Result %s", verb, trimClock(t.selection.start), trimClock(t.selection.end), trimClock(trimLength(segments))))
	}
	t.timeline.QueueDraw()
	a.syncTrimHistory()
}

func trimClock(d time.Duration) string {
	ms := max(int64(0), d.Milliseconds())
	return fmt.Sprintf("%d:%02d.%03d", ms/60000, ms/1000%60, ms%1000)
}

func (a *app) seekTrim(at time.Duration) {
	a.cancelTrimFrameImage()
	a.cancelTrimFrameNavigation()
	t := &a.trim
	if t.media == nil || !t.media.IsSeekable() {
		return
	}
	t.previewing = false
	t.playOnSeek = false
	t.media.Pause()
	t.seekTarget = min(max(0, at), max(0, t.selection.duration-time.Millisecond))
	t.playhead = t.seekTarget
	t.seekQueued = true
}

func (a *app) toggleTrimPlayback() {
	a.cancelTrimFrameImage()
	a.cancelTrimFrameNavigation()
	t := &a.trim
	if t.media == nil || t.export != nil || !t.media.IsPrepared() {
		return
	}
	switch {
	case t.media.Playing() || t.playOnSeek:
		t.playOnSeek = false
		t.media.Pause()
	case t.seekQueued || t.media.IsSeeking():
		t.playOnSeek = true
	case t.media.Ended() || t.playhead >= t.selection.duration-time.Millisecond:
		a.seekTrim(0)
		t.playOnSeek = true
	default:
		t.media.Play()
	}
}

func (a *app) previewTrim() { a.startTrimPreview(trimPreviewFull) }

func (a *app) startTrimPreview(kind trimPreviewKind) {
	t := &a.trim
	segments, err := t.selection.previewSegments(kind)
	if err != nil || t.export != nil || t.media == nil || !t.media.IsSeekable() {
		return
	}
	t.media.Pause()
	a.seekTrim(segments[0].start)
	t.previewing = true
	t.previewSegments = segments
	t.segment = 0
	t.lastSeek = time.Time{}
	t.playOnSeek = true
}

func (a *app) tickTrim() bool {
	t := &a.trim
	if a.closing || a.pages.VisibleChildName() != pageTrim || t.media == nil {
		t.timer = 0
		return false
	}
	if t.export == nil && t.seekQueued && !t.media.IsSeeking() && time.Since(t.lastSeek) >= 80*time.Millisecond {
		t.seekQueued = false
		t.lastSeek = time.Now()
		// A seek on an exact PTS can display the preceding VFR frame
		t.media.Seek(t.seekTarget.Microseconds() + 1)
	}
	if t.playOnSeek && !t.seekQueued && !t.media.IsSeeking() {
		t.playOnSeek = false
		t.media.Play()
	}
	seeking := t.seekQueued || t.media.IsSeeking()
	if !seeking {
		t.playhead = time.Duration(t.media.Timestamp()) * time.Microsecond
	}
	if t.frameImageLoading || (t.display != nil && t.display.VisibleChildName() == "frame") {
		t.playhead = t.frameImageAt
	}
	if !t.previewing && !seeking && t.media.Playing() && t.playhead >= t.selection.duration {
		t.media.Pause()
	}
	if t.media.Ended() && !seeking {
		t.previewing = false
	}
	if t.previewing && t.media.Playing() && !seeking {
		segments := t.previewSegments
		if t.playhead >= segments[t.segment].end {
			t.segment++
			if t.segment == len(segments) {
				t.media.Pause()
				t.previewing = false
			} else {
				a.seekTrim(segments[t.segment].start)
				t.previewing = true
				t.playOnSeek = true
			}
		}
	}
	t.play.SetLabel("Play")
	if t.media.Playing() || t.playOnSeek {
		t.play.SetLabel("Pause")
	}
	t.play.SetSensitive(t.media.IsPrepared() && trimMediaError(t.media) == nil)
	segments, err := t.selection.segments()
	t.preview.SetSensitive(err == nil && len(segments) > 0 && t.media.IsSeekable() && trimMediaError(t.media) == nil)
	a.syncTrimShortPreviews()
	a.syncTrimFrameControls()
	t.position.SetText(trimClock(t.playhead) + " / " + trimClock(t.selection.duration))
	t.timeline.QueueDraw()
	return true
}

func (a *app) loadTrimThumbnails(ctx context.Context) {
	t := &a.trim
	gen, file, duration := t.gen, t.file, t.selection.duration
	for _, pic := range t.thumbs {
		pic.SetPaintable(nil)
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		for i := range 8 {
			at := duration * time.Duration(2*i+1) / 16
			data, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-ss", trimSeconds(at), //nolint:gosec
				"-i", file, "-frames:v", "1", "-vf", "scale=120:-2", "-f", "image2pipe", "-c:v", "mjpeg", "-").Output()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				continue
			}
			glib.IdleAdd(func() {
				if a.closing || t.gen != gen {
					return
				}
				if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data)); err == nil {
					t.thumbs[i].SetPaintable(tex)
				}
			})
		}
	}()
}

func (a *app) stopTrim() {
	a.cancelTrimFrameImage()
	a.cancelTrimFrameNavigation()
	t := &a.trim
	t.frameClock, t.frames = nil, frameWindow{}
	t.gen++
	t.seekQueued, t.playOnSeek = false, false
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.export != nil {
		t.export()
	}
	if t.timer != 0 {
		glib.SourceRemove(t.timer)
		t.timer = 0
	}
	if t.media != nil {
		t.media.Pause()
		t.video.SetMediaStream(nil)
		t.media.Clear()
		t.media = nil
	}
}

func (a *app) leaveTrim() {
	if a.trim.export != nil {
		return
	}
	a.cancelTrimImport()
	page := a.trim.returnPage
	a.stopTrim()
	if page == pageResult {
		a.showResult(a.res.file, a.res.info, a.res.mode)
	} else {
		a.showSetup()
	}
}

func (a *app) saveTrim() {
	a.cancelTrimFrameNavigation()
	t := &a.trim
	if t.export != nil {
		return
	}
	if _, err := t.selection.segments(); err != nil {
		return
	}
	if t.media != nil {
		t.media.Pause()
	}
	t.previewing = false
	t.seekQueued, t.playOnSeek = false, false
	ctx, cancel := context.WithCancel(context.Background())
	t.export = cancel
	done := make(chan struct{})
	t.done = done
	t.controls.SetSensitive(false)
	t.undo.SetSensitive(false)
	t.redo.SetSensitive(false)
	t.video.SetSensitive(false)
	t.reset.SetSensitive(false)
	t.save.SetSensitive(false)
	t.back.SetLabel("Cancel")
	t.progress.SetFraction(0)
	t.progress.SetText("Saving copy…")
	t.progress.SetVisible(true)
	t.status.SetVisible(false)
	file, selection, mode := t.file, t.selection, t.mode
	go func() {
		defer close(done)
		defer cancel()
		target, info, err := exportTrim(ctx, file, selection, func(fraction float64) {
			glib.IdleAdd(func() {
				if !a.closing && t.done == done {
					t.progress.SetFraction(fraction)
					t.progress.SetText(fmt.Sprintf("Saving copy… %.0f%%", 100*fraction))
				}
			})
		})
		glib.IdleAdd(func() {
			t.export = nil
			t.done = nil
			if a.closing {
				return
			}
			t.controls.SetSensitive(true)
			t.video.SetSensitive(true)
			t.reset.SetSensitive(true)
			t.back.SetSensitive(true)
			t.back.SetLabel("Back")
			t.progress.SetVisible(false)
			a.updateTrim()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					t.status.SetText("The copy could not be saved: " + err.Error())
					t.status.SetVisible(true)
				} else {
					a.toast("Saving cancelled; the original is kept")
				}
				return
			}
			a.stopTrim()
			a.showResult(target, info, mode)
			a.toast("Trimmed copy saved; the original is kept")
		})
	}()
}

func (a *app) commitTrimEdit() {
	t := &a.trim
	if t.syncing || t.export != nil || t.selection.duration <= 0 {
		return
	}
	t.history.record(t.selection)
	a.syncTrimHistory()
}

func (a *app) syncTrimHistory() {
	t := &a.trim
	pending := len(t.history.states) > 0 && t.selection != t.history.states[t.history.index]
	t.undo.SetSensitive(t.export == nil && (t.history.index > 0 || pending))
	t.redo.SetSensitive(t.export == nil && !pending && t.history.index+1 < len(t.history.states))
}

func (a *app) restoreTrimEdit(redo bool) {
	t := &a.trim
	if t.export != nil {
		return
	}
	a.commitTrimEdit()
	s, ok := t.history.restore(redo)
	if !ok {
		return
	}
	t.selection = s
	t.syncing = true
	t.keep.SetActive(!s.remove)
	t.syncing = false
	a.updateTrim()
	a.seekTrim(s.start)
	if len(t.frames.times) > 0 {
		i := t.frames.index(s.start)
		if t.frames.times[i] == s.start && (i+1 < len(t.frames.times) || t.frames.last) {
			a.loadTrimFrameImage(s.start)
		}
	}
}

func (a *app) trimHistoryShortcut(key uint, mods gdk.ModifierType) bool {
	t := &a.trim
	if a.res.dialogOpen || a.aboutOpen || a.pages.VisibleChildName() != pageTrim || t.export != nil ||
		mods&gdk.ControlMask == 0 || mods&(gdk.AltMask|gdk.SuperMask) != 0 ||
		(key != gdk.KEY_z && key != gdk.KEY_Z) || (t.start.StateFlags()|t.end.StateFlags())&gtk.StateFlagFocusWithin != 0 {
		return false
	}
	a.restoreTrimEdit(mods&gdk.ShiftMask != 0)
	return true
}

func (a *app) syncTrimShortPreviews() {
	t := &a.trim
	if t.shortStart == nil {
		return
	}
	t.shortStart.SetVisible(!t.selection.remove)
	t.shortEnd.SetVisible(!t.selection.remove)
	t.shortCut.SetVisible(t.selection.remove)
	_, err := t.selection.segments()
	ready := err == nil && t.media != nil && t.media.IsSeekable() && trimMediaError(t.media) == nil && t.export == nil
	t.shortStart.SetSensitive(ready)
	t.shortEnd.SetSensitive(ready)
	t.shortCut.SetSensitive(ready)
}
