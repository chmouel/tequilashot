//go:build !nogui

package gui

import (
	"context"
	"fmt"
	"image"
	"log"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/segment"
)

var (
	listCameraModes = external.ListCameraModes
	cameraName      = external.CameraName
	startPreview    = external.StartPreview
)

const (
	previewWidth  = 460
	previewHeight = 259
	previewFPS    = 15
	// previewRelease bounds the wait for a stopped preview to free the camera.
	previewRelease = 2 * time.Second
)

type choice struct{ value, label string }

var cameraSizes = []choice{
	{external.CameraSmall, "Small"},
	{external.CameraMedium, "Medium"},
	{external.CameraLarge, "Large"},
}

var cameraShapes = []choice{
	{external.CameraSquare, "Square"},
	{external.CameraCircle, "Circle"},
}

// linkedToggles builds a row of exclusive toggle buttons with current active,
// calling onPick whenever another one is chosen.
func linkedToggles(choices []choice, current string, onPick func(string)) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("linked")
	box.SetVAlign(gtk.AlignCenter)
	var first *gtk.ToggleButton
	for _, c := range choices {
		b := gtk.NewToggleButtonWithLabel(c.label)
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		b.SetActive(c.value == current)
		value := c.value
		b.ConnectToggled(func() {
			if b.Active() {
				onPick(value)
			}
		})
		box.Append(b)
	}
	return box
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

	a.camShape = a.settings.CameraShape
	if !external.ValidCameraShape(a.camShape) {
		a.camShape = external.CameraSquare
	}

	sizes := linkedToggles(cameraSizes, a.camSize, func(size string) {
		a.camSize = size
		if a.prevOverlay != nil {
			a.prevOverlay.QueueResize()
		}
	})
	shapes := linkedToggles(cameraShapes, a.camShape, func(shape string) {
		a.camShape = shape
		a.syncShape()
	})

	a.camRow = adw.NewComboRow()
	a.camRow.SetTitle("Camera")
	a.camRow.SetSubtitleLines(1)
	a.camRow.NotifyProperty("selected", a.onCameraSelected)

	sizeRow := adw.NewActionRow()
	sizeRow.SetTitle("Size")
	sizeRow.AddSuffix(sizes)

	a.shapeRow = adw.NewActionRow()
	a.shapeRow.SetTitle("Shape")
	a.shapeRow.SetSubtitle("Circle floats over the screen and is captured with it")
	a.shapeRow.AddSuffix(shapes)

	a.mirror = adw.NewSwitchRow()
	a.mirror.SetTitle("Mirror")
	a.mirror.SetActive(a.settings.CameraMirror)
	a.mirror.NotifyProperty("active", a.syncMirror)

	a.cutout = adw.NewSwitchRow()
	a.cutout.SetTitle("Remove background")
	a.cutout.SetSubtitle("Shows only you; the screen behind shows through")
	a.cutout.SetActive(a.settings.CameraRemoveBackground)
	a.cutout.NotifyProperty("active", func() {
		a.syncShape()
		a.syncPreview()
	})

	a.modelRow = adw.NewActionRow()
	a.modelRow.SetTitle("Segmentation model")
	a.modelProgress = gtk.NewProgressBar()
	a.modelProgress.AddCSSClass("er-model-progress")
	a.modelProgress.SetVAlign(gtk.AlignCenter)
	a.modelProgress.SetVisible(false)
	a.modelBtn = gtk.NewButtonWithLabel("Download")
	a.modelBtn.SetVAlign(gtk.AlignCenter)
	a.modelBtn.AddCSSClass("suggested-action")
	a.modelBtn.ConnectClicked(a.downloadModel)
	a.modelRow.AddSuffix(a.modelProgress)
	a.modelRow.AddSuffix(a.modelBtn)

	a.noRing = adw.NewSwitchRow()
	a.noRing.SetTitle("Hide ring")
	a.noRing.SetSubtitle("Leaves out the white border around the circle")
	a.noRing.SetActive(a.settings.CameraNoRing)
	a.noRing.NotifyProperty("active", a.syncShape)

	a.advancedRow = adw.NewExpanderRow()
	a.advancedRow.SetTitle("Advanced")
	a.advancedRow.SetSubtitleLines(1)
	a.advancedRow.AddRow(a.shapeRow)
	a.advancedRow.AddRow(a.cutout)
	a.advancedRow.AddRow(a.modelRow)
	a.advancedRow.AddRow(a.noRing)
	if a.settings.CameraShape == external.CameraCircle || a.settings.CameraRemoveBackground || a.settings.CameraNoRing {
		a.advancedRow.SetExpanded(true)
	}

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(a.camRow)
	list.Append(sizeRow)
	list.Append(a.mirror)
	list.Append(a.advancedRow)

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
	a.syncShape()
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
		var x, y, w, h float64
		if a.round() {
			x, y, w = external.CircleLayout(cw, ch, a.camSize, a.camPos)
			h = w
		} else {
			x, y, w, h = external.CameraLayout(cw, ch, a.cameraAspect(), a.camSize, a.camPos)
		}
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

// round reports whether the camera is shown as a circle; the virtual camera
// always uses one.
func (a *app) round() bool {
	return a.camShape == external.CameraCircle || a.settings.Mode == ModeCamera
}

// syncShape rounds the camera tile in the layout preview to match the shape.
func (a *app) syncShape() {
	if a.camTile == nil {
		return
	}
	if a.round() {
		a.camTile.AddCSSClass("er-round")
	} else {
		a.camTile.RemoveCSSClass("er-round")
	}
	a.cutout.SetVisible(a.round())
	a.noRing.SetVisible(a.cutoutWanted())
	a.syncModelRow()
	if a.cutoutWanted() {
		a.camTile.AddCSSClass("er-cutout")
	} else {
		a.camTile.RemoveCSSClass("er-cutout")
	}
	if a.cutoutWanted() && a.noRing != nil && a.noRing.Active() {
		a.camTile.AddCSSClass("er-noring")
	} else {
		a.camTile.RemoveCSSClass("er-noring")
	}
	a.syncAdvancedSummary()
	a.prevOverlay.QueueResize()
}

// syncAdvancedSummary updates the subtitle on the Advanced expander row so
// active circle and background-removal settings remain visible when collapsed.
func (a *app) syncAdvancedSummary() {
	if a.advancedRow == nil {
		return
	}
	var parts []string
	if a.settings.Mode != ModeCamera {
		if a.round() {
			parts = append(parts, "Circle")
		} else {
			parts = append(parts, "Square")
		}
	}
	if a.cutoutWanted() {
		switch {
		case a.modelBusy:
			parts = append(parts, "Downloading model")
		case segment.RuntimeAvailable() != nil:
			parts = append(parts, "ONNX Runtime needed")
			a.advancedRow.SetExpanded(true)
		case !segment.ModelInstalled():
			parts = append(parts, "Model download needed")
			a.advancedRow.SetExpanded(true)
		default:
			parts = append(parts, "Background removed")
		}
		if a.noRing != nil && a.noRing.Active() {
			parts = append(parts, "No ring")
		}
	} else if a.settings.Mode == ModeCamera {
		parts = append(parts, "Standard circle")
	}
	a.advancedRow.SetSubtitle(strings.Join(parts, ", "))
}

// syncModelRow shows what removal still needs: the system's ONNX Runtime, or
// the model, which can be downloaded here.
func (a *app) syncModelRow() {
	if !a.cutoutWanted() || a.modelBusy {
		a.modelRow.SetVisible(a.modelBusy && a.cutoutWanted())
		return
	}
	a.modelProgress.SetVisible(false)
	switch {
	case segment.RuntimeAvailable() != nil:
		a.modelRow.SetVisible(true)
		a.modelRow.SetSubtitle("Install your distribution's onnxruntime package to enable")
		a.modelBtn.SetVisible(false)
	case !segment.ModelInstalled():
		a.modelRow.SetVisible(true)
		a.modelRow.SetSubtitle("One-off 176 MB download to " + segment.ModelPath())
		a.modelBtn.SetLabel("Download")
		a.modelBtn.SetVisible(true)
		a.modelBtn.SetSensitive(true)
	default:
		a.modelRow.SetVisible(false)
	}
}

// downloadModel fetches the model in the background, then restarts the preview.
func (a *app) downloadModel() {
	a.modelBusy = true
	a.modelBtn.SetVisible(false)
	a.modelProgress.SetFraction(0)
	a.modelProgress.SetVisible(true)
	a.modelRow.SetSubtitle("Downloading, 0%")
	a.syncAdvancedSummary()
	go func() {
		last := -1
		err := segment.DownloadModel(context.Background(), func(done, total int64) {
			if pct := int(done * 100 / total); pct != last {
				last = pct
				frac := float64(done) / float64(total)
				glib.IdleAdd(func() {
					a.modelProgress.SetFraction(min(1, max(0, frac)))
					a.modelRow.SetSubtitle(fmt.Sprintf("Downloading, %d%%", pct))
				})
			}
		})
		glib.IdleAdd(func() {
			a.modelBusy = false
			a.modelProgress.SetVisible(false)
			if err != nil {
				a.toast("The model could not be downloaded: " + err.Error())
			} else {
				a.toast("Background removal model installed")
			}
			a.syncModelRow()
			a.syncAdvancedSummary()
			a.syncPreview()
		})
	}()
}

// cutoutWanted reports whether the background is to be removed, which only
// the round camera supports.
func (a *app) cutoutWanted() bool {
	return a.round() && a.cutout != nil && a.cutout.Active()
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
	a.cameraRevealer.SetRevealChild((a.settings.Mode == ModeVideo || a.settings.Mode == ModeCamera) && a.camera.Active() && len(a.cameras) > 0)
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

	a.camera.SetVisible(a.settings.Mode != ModeReplay)
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
	cut := a.cutoutWanted()
	if cut && (segment.RuntimeAvailable() != nil || !segment.ModelInstalled()) {
		// The row explains what is missing; no raw picture is shown meanwhile.
		a.stopPreview()
		a.camPic.SetPaintable(nil)
		return
	}
	if a.preview != nil && a.previewDev == a.camSel && a.previewCut == cut {
		return
	}
	a.closePreviewCutter()

	a.gate.Retire(a.preview)
	a.previewGen++
	gen, device := a.previewGen, a.camSel
	a.previewDev, a.previewCut = device, cut
	a.camPic.SetPaintable(nil)
	a.camMsg.SetVisible(false)

	// Without the cut-out the picture must never show the raw frame, so a
	// failure here leaves the tile empty with its message.
	var onFrame func([]byte)
	failed := func() {
		glib.IdleAdd(func() {
			if gen == a.previewGen {
				a.camPic.SetPaintable(nil)
				a.camMsg.SetVisible(true)
			}
		})
	}
	latest := &external.LatestFrame{}
	w, h := external.PreviewMode(a.camModes, device)
	if cut {
		a.previewCutter = newCutter(
			nil, false,
			func(img *image.RGBA) {
				glib.IdleAdd(func() {
					if gen == a.previewGen {
						a.camPic.SetPaintable(rgbaTexture(img))
						a.camMsg.SetVisible(false)
					}
				})
			},
			func(err error) {
				log.Printf("background removal: %v", err)
				failed()
			},
		)
		onFrame = a.previewCutter.Put
	} else {
		onFrame = func(frame []byte) {
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
		}
	}
	a.preview = startPreview(&a.gate, device, w, h, previewFPS, onFrame, failed)
}

func (a *app) closePreviewCutter() {
	if a.previewCutter != nil {
		a.previewCutter.Close()
		a.previewCutter = nil
	}
}

// stopPreview ends the live preview at once. The camera stays claimed by the
// gate until the process has exited.
func (a *app) stopPreview() {
	a.gate.Retire(a.preview)
	a.closePreviewCutter()
	a.preview, a.previewDev = nil, ""
	a.previewGen++
}

// cameraSettings fills the camera fields of s from the controls.
func (a *app) cameraSettings(s *Settings) {
	s.Camera = a.camera.Active()
	s.CameraMirror = a.mirror.Active()
	s.CameraRemoveBackground = a.cutout.Active()
	s.CameraNoRing = a.noRing.Active()
	s.CameraPosition = a.camPos
	s.CameraSize = a.camSize
	s.CameraShape = a.camShape
	s.CameraDevice = a.settings.CameraDevice
	if a.camSel != "" {
		s.CameraDevice = a.camSel
	}
}
