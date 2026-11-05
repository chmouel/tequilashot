//go:build !nogui

package gui

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/chmouel/tequilashot/internal/external"
)

var (
	listCameraModes = external.ListCameraModes
	cameraName      = external.CameraName
	startPreview    = external.StartPreview
)

const (
	previewWidth  = 460
	previewHeight = 259
	// previewRelease bounds the wait for a stopped preview to free the camera.
	previewRelease = 2 * time.Second
)

var cameraSizes = []struct{ size, label string }{
	{external.CameraSmall, "Small"},
	{external.CameraMedium, "Medium"},
	{external.CameraLarge, "Large"},
}

// buildCamera creates the camera pill, hidden until a camera is found, and the
// column of options that slides in beside the setup page when it is on.
func (a *app) buildCamera() *gtk.ToggleButton {
	a.camera = a.pill("Camera", a.settings.Camera, "camera-web-symbolic", "camera-disabled-symbolic")
	a.camera.SetVisible(false)

	a.camPos = a.settings.CameraPosition
	if !external.ValidCameraPosition(a.camPos) {
		a.camPos = external.CameraBottomRight
	}
	a.camSize = a.settings.CameraSize
	if !external.ValidCameraSize(a.camSize) {
		a.camSize = external.CameraMedium
	}

	a.sizeBtns = map[string]*gtk.ToggleButton{}
	sizes := gtk.NewBox(gtk.OrientationHorizontal, 0)
	sizes.AddCSSClass("linked")
	sizes.SetVAlign(gtk.AlignCenter)
	var firstSize *gtk.ToggleButton
	for _, sz := range cameraSizes {
		b := gtk.NewToggleButtonWithLabel(sz.label)
		if firstSize == nil {
			firstSize = b
		} else {
			b.SetGroup(firstSize)
		}
		size := sz.size
		b.ConnectToggled(func() {
			if b.Active() {
				a.camSize = size
				if a.prevOverlay != nil {
					a.prevOverlay.QueueResize()
				}
			}
		})
		a.sizeBtns[size] = b
		sizes.Append(b)
	}
	a.sizeBtns[a.camSize].SetActive(true)

	a.camRow = adw.NewComboRow()
	a.camRow.SetTitle("Camera")
	a.camRow.SetSubtitleLines(1)
	a.camRow.NotifyProperty("selected", a.onCameraSelected)

	sizeRow := adw.NewActionRow()
	sizeRow.SetTitle("Size")
	sizeRow.AddSuffix(sizes)

	a.mirror = adw.NewSwitchRow()
	a.mirror.SetTitle("Mirror")
	a.mirror.SetActive(a.settings.CameraMirror)
	a.mirror.NotifyProperty("active", a.syncMirror)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(a.camRow)
	list.Append(sizeRow)
	list.Append(a.mirror)

	a.cameraBox = gtk.NewBox(gtk.OrientationVertical, 14)
	a.cameraBox.SetMarginTop(8)
	a.cameraBox.SetMarginBottom(18)
	a.cameraBox.SetMarginStart(18)
	a.cameraBox.SetMarginEnd(18)
	a.cameraBox.SetVAlign(gtk.AlignStart)
	a.cameraBox.Append(section("Camera"))
	a.cameraBox.Append(a.buildLayoutPreview())
	a.cameraBox.Append(list)

	column := gtk.NewBox(gtk.OrientationHorizontal, 0)
	column.Append(gtk.NewSeparator(gtk.OrientationVertical))
	column.Append(a.cameraBox)

	rev := gtk.NewRevealer()
	rev.SetTransitionType(gtk.RevealerTransitionTypeSlideLeft)
	rev.SetChild(column)
	a.camera.ConnectToggled(func() {
		a.syncCameraRevealer()
		a.syncPreview()
	})
	a.cameraRevealer = rev
	a.updatePreviewBase()
	a.syncMirror()
	return a.camera
}

// buildLayoutPreview builds the clickable frame showing where the camera lands.
func (a *app) buildLayoutPreview() *gtk.Box {
	a.prevThumb = gtk.NewPicture()
	a.prevThumb.SetContentFit(gtk.ContentFitContain)
	a.prevThumb.SetCanShrink(true)

	a.prevHolder = gtk.NewLabel("")
	a.prevHolder.AddCSSClass("dim-label")
	icon := gtk.NewImageFromIconName("video-display-symbolic")
	icon.SetPixelSize(32)
	icon.AddCSSClass("dim-label")
	holder := gtk.NewBox(gtk.OrientationVertical, 6)
	holder.AddCSSClass("er-prevholder")
	holder.SetVAlign(gtk.AlignCenter)
	holder.SetHAlign(gtk.AlignFill)
	holder.Append(icon)
	holder.Append(a.prevHolder)
	holderFrame := gtk.NewCenterBox()
	holderFrame.AddCSSClass("er-prevholder")
	holderFrame.SetCenterWidget(holder)

	a.prevBase = gtk.NewStack()
	a.prevBase.AddNamed(a.prevThumb, "thumb")
	a.prevBase.AddNamed(holderFrame, "placeholder")

	a.camPic = gtk.NewPicture()
	a.camPic.SetContentFit(gtk.ContentFitCover)
	a.camPic.SetCanShrink(true)
	a.camMsg = gtk.NewLabel("Camera unavailable")
	a.camMsg.AddCSSClass("er-cammsg")
	a.camMsg.SetWrap(true)
	a.camMsg.SetJustify(gtk.JustifyCenter)
	a.camMsg.SetHAlign(gtk.AlignCenter)
	a.camMsg.SetVAlign(gtk.AlignCenter)
	a.camMsg.SetVisible(false)

	a.camTile = gtk.NewOverlay()
	a.camTile.AddCSSClass("er-camtile")
	a.camTile.SetOverflow(gtk.OverflowHidden)
	a.camTile.SetChild(a.camPic)
	a.camTile.AddOverlay(a.camMsg)

	a.prevOverlay = gtk.NewOverlay()
	a.prevOverlay.AddCSSClass("er-layout")
	a.prevOverlay.SetOverflow(gtk.OverflowHidden)
	a.prevOverlay.SetSizeRequest(previewWidth, previewHeight)
	a.prevOverlay.SetHAlign(gtk.AlignCenter)
	a.prevOverlay.SetFocusable(true)
	a.prevOverlay.SetCursorFromName("pointer")
	a.prevOverlay.SetChild(a.prevBase)
	a.prevOverlay.AddOverlay(a.camTile)
	a.prevOverlay.ConnectGetChildPosition(func(gtk.Widgetter) (*gdk.Rectangle, bool) {
		fw, fh := float64(a.prevOverlay.Width()), float64(a.prevOverlay.Height())
		if fw <= 0 || fh <= 0 {
			return nil, false
		}
		cx, cy, cw, ch := external.ContainRect(fw, fh, a.previewAspect())
		x, y, w, h := external.CameraLayout(cw, ch, a.cameraAspect(), a.camSize, a.camPos)
		r := gdk.NewRectangle(int(cx+x+0.5), int(cy+y+0.5), int(w+0.5), int(h+0.5))
		return &r, true
	})

	click := gtk.NewGestureClick()
	click.SetButton(1)
	click.ConnectPressed(func(_ int, x, y float64) {
		a.prevOverlay.GrabFocus()
		w, h := float64(a.prevOverlay.Width()), float64(a.prevOverlay.Height())
		a.setCameraCorner(y < h/2, x < w/2)
	})
	a.prevOverlay.AddController(click)

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		top := strings.HasPrefix(a.camPos, "top")
		left := strings.HasSuffix(a.camPos, "left")
		switch keyval {
		case gdk.KEY_Left:
			left = true
		case gdk.KEY_Right:
			left = false
		case gdk.KEY_Up:
			top = true
		case gdk.KEY_Down:
			top = false
		default:
			return false
		}
		a.setCameraCorner(top, left)
		return true
	})
	a.prevOverlay.AddController(keys)

	hint := label("Click a corner to move the camera", "dim-label", "caption")
	hint.SetHAlign(gtk.AlignCenter)

	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.Append(a.prevOverlay)
	box.Append(hint)
	return box
}

func (a *app) setCameraCorner(top, left bool) {
	pos := map[[2]bool]string{
		{true, true}:   external.CameraTopLeft,
		{true, false}:  external.CameraTopRight,
		{false, true}:  external.CameraBottomLeft,
		{false, false}: external.CameraBottomRight,
	}[[2]bool{top, left}]
	if pos != a.camPos {
		a.camPos = pos
		a.prevOverlay.QueueResize()
	}
}

// cameraAspect is the picture aspect of the mode that will be recorded.
func (a *app) cameraAspect() float64 {
	if w, h, _ := external.BestCameraMode(a.camModes, a.camSel); w > 0 && h > 0 {
		return float64(w) / float64(h)
	}
	return 16.0 / 9.0
}

// previewAspect is the aspect of the picture shown behind the camera tile, or
// zero when it fills the whole frame.
func (a *app) previewAspect() float64 {
	if a.prevBase.VisibleChildName() != "thumb" {
		return 0
	}
	if p := a.prevThumb.Paintable(); p != nil {
		return p.IntrinsicAspectRatio()
	}
	return 0
}

func (a *app) syncMirror() {
	if a.mirror.Active() {
		a.camPic.AddCSSClass("er-mirror")
	} else {
		a.camPic.RemoveCSSClass("er-mirror")
	}
}

func (a *app) updatePreviewBase() {
	if a.prevBase == nil {
		return
	}
	src := a.settings.Source
	for id, b := range a.srcButtons {
		if b.Active() {
			src = id
		}
	}
	text := map[string]string{SourceWindow: "Selected window", SourceRegion: "Chosen region"}[src]
	var pic *gtk.Picture
	if src == SourceMonitor {
		pic = a.thumbs[a.monName]
		text = "Monitor"
	} else if src == SourceWindow && a.winIdx >= 0 && a.winIdx < len(a.windows) {
		pic = a.windowThumbs[a.windows[a.winIdx].ID].picture
	}
	if pic != nil {
		if p := pic.Paintable(); p != nil {
			a.prevThumb.SetPaintable(p)
			a.prevBase.SetVisibleChildName("thumb")
			return
		}
	}
	a.prevHolder.SetLabel(text)
	a.prevBase.SetVisibleChildName("placeholder")
}

func (a *app) syncCameraRevealer() {
	a.cameraRevealer.SetRevealChild(a.settings.Mode != ModeScreenshot && a.camera.Active() && len(a.cameras) > 0)
}

// probeCameras lists cameras off the main thread.
func (a *app) probeCameras() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		modes, err := listCameraModes(ctx)
		if err != nil {
			log.Printf("cameras: %v", err)
		}
		names := map[string]string{}
		for _, d := range external.CameraDevices(modes) {
			names[d] = cameraName(d)
		}
		glib.IdleAdd(func() { a.setCameras(modes, names) })
	}()
}

func (a *app) setCameras(modes []external.CameraMode, names map[string]string) {
	a.camModes = modes
	a.cameras = external.CameraDevices(modes)
	if len(a.cameras) == 0 {
		a.camera.SetVisible(false)
		a.syncCameraRevealer()
		a.syncPreview()
		return
	}

	idx := external.CameraIndex(a.cameras, a.settings.CameraDevice)
	want := a.cameras[idx]

	count := map[string]int{}
	for _, n := range names {
		count[n]++
	}
	labels := make([]string, len(a.cameras))
	for i, d := range a.cameras {
		labels[i] = names[d]
		if count[labels[i]] > 1 {
			labels[i] += " (" + d + ")"
		}
	}
	// Populating the row fires notify::selected; keep it from overwriting want.
	a.camPopulating = true
	a.camRow.SetModel(gtk.NewStringList(labels))
	a.camRow.SetSelected(uint(idx))
	a.camPopulating = false
	a.camSel = want
	a.camRow.SetSubtitle(glib.MarkupEscapeText(a.camSel))

	a.camera.SetVisible(true)
	a.syncCameraRevealer()
	a.prevOverlay.QueueResize()
	a.syncPreview()
}

func (a *app) onCameraSelected() {
	if a.camPopulating {
		return
	}
	i := int(a.camRow.Selected())
	if i < 0 || i >= len(a.cameras) || a.cameras[i] == a.camSel {
		return
	}
	a.camSel = a.cameras[i]
	a.camRow.SetSubtitle(glib.MarkupEscapeText(a.camSel))
	a.prevOverlay.QueueResize()
	a.syncPreview()
}

// previewWanted is true only whilst the camera picture is on show: camera on,
// dialogue visible, setup page showing, no recording under way and not closing.
func (a *app) previewWanted() bool {
	return a.settings.Mode != ModeScreenshot && a.camera != nil && a.camera.Active() && a.camera.Visible() && a.camSel != "" &&
		!a.busy && !a.closing && a.win != nil && a.win.Mapped() && a.pages != nil && a.onSetupPage()
}

// syncPreview starts, restarts or stops the live preview to match previewWanted.
func (a *app) syncPreview() {
	if !a.previewWanted() {
		if a.preview != nil {
			a.stopPreview()
		}
		return
	}
	if a.preview != nil && a.previewDev == a.camSel {
		return
	}

	a.gate.Retire(a.preview)
	a.previewGen++
	gen, device := a.previewGen, a.camSel
	a.previewDev = device
	a.camPic.SetPaintable(nil)
	a.camMsg.SetVisible(false)

	latest := &external.LatestFrame{}
	w, h := external.PreviewMode(a.camModes, device)
	a.preview = startPreview(&a.gate, device, w, h,
		func(frame []byte) {
			if !latest.Put(frame) {
				return
			}
			glib.IdleAdd(func() {
				data := latest.Take()
				if gen != a.previewGen || data == nil {
					return
				}
				if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data)); err == nil {
					a.camPic.SetPaintable(tex)
					a.camMsg.SetVisible(false)
				}
			})
		},
		func() {
			glib.IdleAdd(func() {
				if gen == a.previewGen {
					a.camPic.SetPaintable(nil)
					a.camMsg.SetVisible(true)
				}
			})
		})
}

// stopPreview ends the live preview at once. The camera stays claimed by the
// gate until the process has exited.
func (a *app) stopPreview() {
	a.gate.Retire(a.preview)
	a.preview, a.previewDev = nil, ""
	a.previewGen++
}

// cameraSettings fills the camera fields of s from the controls.
func (a *app) cameraSettings(s *Settings) {
	s.Camera = a.camera.Active()
	s.CameraMirror = a.mirror.Active()
	s.CameraPosition = a.camPos
	s.CameraSize = a.camSize
	s.CameraDevice = a.settings.CameraDevice
	if a.camSel != "" {
		s.CameraDevice = a.camSel
	}
}
