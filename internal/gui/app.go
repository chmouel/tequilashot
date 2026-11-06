//go:build !nogui

// Package gui implements the GTK4/libadwaita capture dialogue.
package gui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chmouel/tequilashot/internal/prefs"

	"github.com/chmouel/tequilashot/internal/external"
	"github.com/chmouel/tequilashot/internal/sway"
	"github.com/chmouel/tequilashot/pkg/protocol"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const appID = "com.chmouel.Tequilashot.Recorder"

// hideSettle gives the compositor time to unmap the dialogue before capture.
const hideSettle = 300 * time.Millisecond

// Backend connects the dialogue to the daemon.
type Backend struct {
	SaveLocation string
	EnsureDaemon func() error
	// Send carries short requests and must apply a bounded timeout.
	Send func(protocol.Request) (*protocol.Response, error)
	// SendStart carries start requests, which may wait on the user indefinitely.
	SendStart func(protocol.Request) (*protocol.Response, error)
}

// thumbInterval is how often visible thumbnails are refreshed.
const thumbInterval = 2 * time.Second

const css = `
.er-tile { border-radius: 12px; padding: 8px; }
.er-thumb { border-radius: 8px; background: alpha(currentColor, 0.08); }
flowboxchild.er-tilechild { border-radius: 14px; padding: 2px; background: none; outline: none; }
flowboxchild.er-tilechild:selected { background: alpha(@accent_bg_color, 0.14); box-shadow: inset 0 0 0 2px @accent_bg_color; }
.er-pill { border-radius: 999px; padding: 10px 20px; opacity: 0.65; }
.er-pill:checked { opacity: 1; background: @accent_bg_color; color: @accent_fg_color; }
.er-layout { border-radius: 12px; background: #1e293b; }
.er-layout:focus-visible { outline: 2px solid @accent_bg_color; outline-offset: 2px; }
.er-prevholder { background: #1e293b; color: #94a3b8; }
.er-camtile { border-radius: 6px; background: #334155; box-shadow: 0 1px 6px rgba(0, 0, 0, 0.5), 0 0 0 1.5px @accent_bg_color; }
.er-camtile.er-round { border-radius: 9999px; box-shadow: 0 1px 6px rgba(0, 0, 0, 0.5), 0 0 0 1.5px rgba(255, 255, 255, 0.92); }
.er-camtile.er-cutout { background: transparent; box-shadow: 0 0 0 1.5px rgba(255, 255, 255, 0.92); }
.er-camtile.er-noring { box-shadow: none; }
.er-model-progress { min-width: 110px; }
.er-cammsg { font-size: 0.7em; color: #cbd5e1; }
.er-mirror { transform: scale(-1, 1); }
.er-frame { border-radius: 12px; background: alpha(currentColor, 0.08); }
.er-selection button { padding-left: 6px; padding-right: 6px; }
.er-selection spinbutton > button { min-width: 18px; padding-left: 0; padding-right: 0; }
.er-section { font-weight: 700; font-size: 0.85em; opacity: 0.7; letter-spacing: 0.04em; }
.er-sponsor { color: #ff69b4; }
`

type app struct {
	b            Backend
	gtkApp       *adw.Application
	win          *adw.ApplicationWindow
	toasts       *adw.ToastOverlay
	pages        *gtk.Stack
	res          resultView
	trim         trimView
	importVideo  trimImport
	settings     Settings
	settingsPath string
	startButton  *gtk.Button
	audioSection *gtk.Box
	audioTitle   *gtk.Label
	countdownBox *gtk.Box
	cropRow      *gtk.Box
	regionHint   *gtk.Label
	windowHint   *gtk.Label

	replayDurationBox *gtk.Box
	replayDuration    map[int]*gtk.ToggleButton
	replayCaption     *gtk.Label
	replayTimer       glib.SourceHandle
	replayActive      bool
	replayBusy        bool
	replayPolling     bool
	replayShowPending bool

	srcButtons  map[string]*gtk.ToggleButton
	stack       *gtk.Stack
	flow        *gtk.FlowBox
	crop        *gtk.SpinButton
	winList     *gtk.ListBox
	desktop     *gtk.ToggleButton
	mic         *gtk.ToggleButton
	micRow      *adw.ComboRow
	micRevealer *gtk.Revealer
	mics        []external.AudioDevice
	micSel      string
	// micsKnown is set once the first microphone probe has finished, so a saved
	// device is never sent before its fallback has had a chance to apply.
	micsKnown  bool
	micDefault string
	// micPopulating suppresses microphone selection handling whilst the list is filled.
	micPopulating bool
	countdown     map[int]*gtk.ToggleButton

	camera         *gtk.ToggleButton
	cameraRevealer *gtk.Revealer
	cameraBox      *gtk.Box
	advancedRow    *adw.ExpanderRow
	mirror         *adw.SwitchRow
	cutout         *adw.SwitchRow
	noRing         *adw.SwitchRow
	modelRow       *adw.ActionRow
	modelBtn       *gtk.Button
	modelProgress  *gtk.ProgressBar
	modelBusy      bool
	previewCut     bool
	previewCutter  *cutter
	camRow         *adw.ComboRow
	cameras        []string
	camModes       []external.CameraMode
	camSel         string
	camPos         string
	camSize        string
	camShape       string
	shapeRow       *adw.ActionRow

	prevOverlay *gtk.Overlay
	prevBase    *gtk.Stack
	prevThumb   *gtk.Picture
	prevHolder  *gtk.Label
	camTile     *gtk.Overlay
	camPic      *gtk.Picture
	camMsg      *gtk.Label
	preview     *external.Preview
	gate        external.PreviewGate
	// camPopulating suppresses camera selection handling whilst the list is filled.
	camPopulating bool
	closing       bool
	previewDev    string
	previewGen    int

	monitors []sway.Monitor
	windows  []sway.Window
	winIdx   int
	monName  string

	thumbs           map[string]*gtk.Picture
	inflight         map[string]bool
	thumbTimer       glib.SourceHandle
	windowThumbs     map[string]windowThumbnail
	windowRefreshing bool

	busy    bool
	started bool
	// aboutOpen and prefsOpen keep Escape and Return from acting on the
	// setup page whilst a dialogue covers it.
	aboutOpen bool
	prefsOpen bool

	// mu guards pending and stopLatched, shared with the start goroutine.
	mu          sync.Mutex
	pending     bool
	stopLatched bool
}

type windowThumbnail struct {
	frame   *gtk.Stack
	picture *gtk.Picture
}

// Data sources and staging hooks; the demo build swaps them for staged
// content so documentation screenshots never show a real desktop.
var (
	listMonitors = sway.ListMonitors
	listWindows  = func(ctx context.Context) ([]sway.Window, error) {
		wins, err := sway.ListWindows(ctx)
		return slices.DeleteFunc(wins, func(w sway.Window) bool { return w.Class == appID }), err
	}
	grabThumbnail = func(ctx context.Context, output string) ([]byte, error) {
		return exec.CommandContext(ctx, prefs.Bin("grim"), "-o", output, "-s", "0.1", "-t", "jpeg", "-q", "70", "-").Output() //nolint:gosec
	}
	grabWindowThumbnail = captureWindowPreview
	// The demo build must not capture real desktop content
	captureDisabled bool
	demoSettings    func(*Settings) bool
	demoStage       func(*app)
)

// Run opens the dialogue and blocks until the application exits.
func Run(b Backend) error {
	a := &app{b: b, winIdx: -1, thumbs: map[string]*gtk.Picture{}, inflight: map[string]bool{}}
	a.settingsPath, _ = settingsPath()
	a.settings = LoadSettings(a.settingsPath)
	if demoSettings != nil && demoSettings(&a.settings) {
		a.settingsPath = ""
	}
	a.monName = a.settings.Monitor

	a.gtkApp = adw.NewApplication(appID, gio.ApplicationDefaultFlags)
	a.gtkApp.ConnectActivate(a.activate)
	code := a.gtkApp.Run([]string{os.Args[0]})
	a.stopPreview()
	if !a.gate.Wait(previewRelease) {
		log.Print("camera preview did not exit in time")
	}
	if code != 0 {
		return fmt.Errorf("capture dialogue exited with status %d", code)
	}
	return nil
}

func (a *app) activate() {
	if !a.started {
		a.started = true
		a.build()
		if demoStage != nil {
			defer demoStage(a)
		}
		a.gtkApp.Hold()
	}

	if a.replayBusy {
		return
	}
	if a.busy {
		if a.settings.Mode == ModeReplay {
			a.replayShowPending = true
			return
		}
		if a.settings.Mode == ModeScreenshot {
			return
		}
		// A second launch acts as a stop key. Whilst the start is still
		// pending there is nothing to stop yet, so the request is latched.
		a.mu.Lock()
		if a.pending {
			a.stopLatched = true
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		if a.settings.Mode == ModeCamera {
			go a.stopVirtualCamera()
		} else {
			go a.stopRecording()
		}
		return
	}
	if a.res.editing || a.pages.VisibleChildName() == pageTrim {
		a.win.Present()
		return
	}

	go func() {
		if st := a.state(); st != nil && st.Replay {
			glib.IdleAdd(func() {
				a.showReplay(st)
				a.win.Present()
			})
			return
		} else if st != nil && st.Recording {
			a.stopRecording()
			for i := 0; i < 100; i++ {
				if st := a.state(); st == nil || !st.Recording {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		} else if st != nil && st.VirtualCamera {
			a.stopVirtualCamera()
		}
		glib.IdleAdd(func() {
			a.win.Present()
			a.probeMics()
			a.refresh()
			a.updateThumbTimer()
		})
	}()
}

func (a *app) stopRecording() {
	resp, err := a.b.Send(protocol.Request{Command: "execute", Action: "stop-recording"})
	if err == nil && !resp.Success {
		err = errors.New(resp.Message)
	}
	if err != nil {
		log.Printf("stop-recording: %v", err)
	}
}

// stopVirtualCamera stops the virtual camera; the daemon clears its state
// before replying.
func (a *app) stopVirtualCamera() {
	resp, err := a.b.Send(protocol.Request{Command: "execute", Action: "virtual-camera-stop"})
	if err == nil && !resp.Success {
		err = errors.New(resp.Message)
	}
	if err != nil {
		log.Printf("virtual-camera-stop: %v", err)
	}
}

func (a *app) state() *protocol.State {
	if captureDisabled {
		return nil
	}
	resp, err := a.b.Send(protocol.Request{Command: "execute", Action: "waybar-status"})
	if err != nil || resp == nil {
		return nil
	}
	return resp.State
}

func label(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

func section(title string) *gtk.Label { return label(strings.ToUpper(title), "er-section") }

func (a *app) build() {
	if display := gdk.DisplayGetDefault(); display != nil {
		p := gtk.NewCSSProvider()
		p.LoadFromString(css)
		gtk.StyleContextAddProviderForDisplay(display, p, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}

	a.win = adw.NewApplicationWindow(&a.gtkApp.Application)
	a.win.SetTitle(a.setupTitle())
	a.win.SetDefaultSize(520, -1)
	// A fixed-size window is floated by tiling compositors, keeping the dialogue compact.
	a.win.SetResizable(false)
	a.win.ConnectMap(a.syncPreview)
	a.win.ConnectUnmap(a.stopPreview)
	a.win.ConnectCloseRequest(func() bool {
		if a.closing {
			return true
		}
		a.closing = true
		a.cancelTrimImport()
		a.stopThumbTimer()
		a.stopPreview()
		trimDone := a.trim.done
		a.stopTrim()
		go func() {
			if trimDone != nil {
				<-trimDone
			}
			if !a.gate.Wait(previewRelease) {
				log.Print("camera preview did not exit in time")
			}
			glib.IdleAdd(a.gtkApp.Quit)
		}()
		return true
	})

	key := gtk.NewEventControllerKey()
	key.SetPropagationPhase(gtk.PhaseCapture)
	key.ConnectKeyPressed(func(keyval, _ uint, mods gdk.ModifierType) bool {
		if a.trimHistoryShortcut(keyval, mods) {
			return true
		}
		if a.res.dialogOpen {
			return false
		}
		switch keyval {
		case gdk.KEY_space:
			if !a.modalOpen() && a.pages.VisibleChildName() == pageTrim && a.trim.export == nil {
				if (a.trim.start.StateFlags()|a.trim.end.StateFlags())&gtk.StateFlagFocusWithin == 0 {
					a.toggleTrimPlayback()
					return true
				}
			}
		case gdk.KEY_Escape:
			if a.modalOpen() {
				return false
			}
			if a.pages.VisibleChildName() == pageTrim {
				if a.trim.export == nil {
					a.leaveTrim()
				}
				return true
			}
			if a.res.editing {
				return true
			}
			if a.res.dialogOpen {
				return false
			}
			if a.pages.VisibleChildName() == pageReplay {
				a.win.Close()
				return true
			}
			if !a.onSetupPage() {
				a.showSetup()
				return true
			}
			a.win.Close()
			return true
		case gdk.KEY_Return, gdk.KEY_KP_Enter:
			if a.modalOpen() || !a.onSetupPage() {
				return false
			}
			a.startCapture()
			return true
		}
		return false
	})
	a.win.AddController(key)

	body := gtk.NewBox(gtk.OrientationVertical, 14)
	body.SetHExpand(true)
	body.SetMarginTop(8)
	body.SetMarginBottom(18)
	body.SetMarginStart(18)
	body.SetMarginEnd(18)

	body.Append(section("Source"))
	body.Append(a.buildSource())
	a.audioSection = gtk.NewBox(gtk.OrientationVertical, 14)
	a.audioTitle = section("Audio & camera")
	a.audioSection.Append(a.audioTitle)
	a.audioSection.Append(a.buildAudio())
	body.Append(a.audioSection)
	a.countdownBox = gtk.NewBox(gtk.OrientationVertical, 14)
	a.countdownBox.Append(section("Countdown"))
	a.countdownBox.Append(a.buildCountdown())
	body.Append(a.countdownBox)
	a.replayDurationBox = a.buildReplayDuration()
	body.Append(a.replayDurationBox)

	rec := gtk.NewButtonWithLabel("Record")
	rec.AddCSSClass("suggested-action")
	rec.AddCSSClass("pill")
	rec.SetHAlign(gtk.AlignCenter)
	rec.SetMarginTop(6)
	rec.SetSizeRequest(200, -1)
	rec.ConnectClicked(a.startCapture)
	a.startButton = rec
	body.Append(rec)

	a.pages = gtk.NewStack()
	a.pages.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	a.pages.SetVhomogeneous(false)
	setup := gtk.NewBox(gtk.OrientationHorizontal, 0)
	setup.Append(body)
	setup.Append(a.cameraRevealer)
	a.pages.AddNamed(setup, pageSetup)
	a.pages.AddNamed(a.buildResult(), pageResult)
	a.pages.AddNamed(a.buildTrim(), pageTrim)
	a.pages.AddNamed(a.buildReplayControls(), pageReplay)
	a.pages.SetVisibleChildName(pageSetup)
	a.pages.NotifyProperty("visible-child-name", a.syncPreview)

	a.toasts = adw.NewToastOverlay()
	a.toasts.SetChild(a.pages)

	root := gtk.NewBox(gtk.OrientationVertical, 0)
	header := adw.NewHeaderBar()
	a.win.AddController(a.trimDropTarget())
	header.PackEnd(a.primaryMenu())
	header.PackEnd(a.headerAction("emote-love-symbolic", "Sponsor Tequilashot", "er-sponsor", a.sponsor))
	root.Append(header)
	root.Append(a.toasts)
	a.win.SetContent(root)
	body.Prepend(a.buildMode())
	a.updateMode()
}

func (a *app) setupTitle() string {
	switch a.settings.Mode {
	case ModeScreenshot:
		return "Capture screen"
	case ModeReplay:
		return "Instant replay"
	case ModeCamera:
		return "Virtual camera"
	}
	return "Record screen"
}

func (a *app) buildMode() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("linked")
	box.SetHAlign(gtk.AlignCenter)
	var first *gtk.ToggleButton
	for _, mode := range []struct{ id, title string }{{ModeScreenshot, "Screenshot"}, {ModeVideo, "Video"}, {ModeReplay, "Replay"}, {ModeCamera, "Camera"}} {
		b := gtk.NewToggleButtonWithLabel(mode.title)
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		b.SetActive(a.settings.Mode == mode.id)
		b.ConnectToggled(func() {
			if b.Active() {
				a.settings.Mode = mode.id
				a.updateMode()
				a.saveSettings(a.collect())
			}
		})
		box.Append(b)
	}
	return box
}

func (a *app) updateMode() {
	video := a.settings.Mode == ModeVideo
	live := a.settings.Mode == ModeCamera
	replay := a.settings.Mode == ModeReplay
	a.replayDurationBox.SetVisible(replay)
	for id, b := range a.srcButtons {
		b.SetVisible(!replay || id == SourceMonitor)
		if !replay && b.Active() {
			a.stack.SetVisibleChildName(id)
		}
	}
	if replay {
		a.stack.SetVisibleChildName(SourceMonitor)
	}
	a.camera.SetVisible(!replay && len(a.cameras) > 0)
	a.audioSection.SetVisible(video || live || replay)
	a.desktop.SetVisible(video || replay)
	a.mic.SetVisible(video || replay)
	a.micRevealer.SetVisible(video || replay)
	a.cropRow.SetVisible(video)
	a.countdownBox.SetVisible(!live && !replay)
	a.cameraRevealer.SetVisible(video || live)
	a.shapeRow.SetVisible(!live)
	switch {
	case replay:
		a.audioTitle.SetText(strings.ToUpper("Audio"))
		a.startButton.SetLabel("Start buffering")
	case video:
		a.audioTitle.SetText(strings.ToUpper("Audio & camera"))
		a.startButton.SetLabel("Record")
		a.regionHint.SetText("You will be asked to drag out an area once you press Record.")
		a.windowHint.SetText("A window is recorded as its present on-screen area; moving it will not be followed.")
	case live:
		a.audioTitle.SetText(strings.ToUpper("Camera"))
		a.startButton.SetLabel("Go live")
		a.regionHint.SetText("You will be asked to drag out an area once you press Go live.")
		a.windowHint.SetText("A window is shown as its present on-screen area; moving it will not be followed.")
	default:
		a.startButton.SetLabel("Capture")
		a.regionHint.SetText("You will be asked to drag out an area once you press Capture.")
		a.windowHint.SetText("The selected window's present on-screen area will be captured.")
	}
	a.win.SetTitle(a.setupTitle())
	a.syncShape()
	a.syncCameraRevealer()
	a.syncPreview()
	a.updateThumbTimer()
}

func (a *app) saveSettings(s Settings) {
	a.settings = s
	if a.settingsPath != "" {
		if err := SaveSettings(a.settingsPath, s); err != nil {
			log.Printf("saving settings: %v", err)
		}
	}
}

func (a *app) buildSource() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 10)

	seg := gtk.NewBox(gtk.OrientationHorizontal, 0)
	seg.AddCSSClass("linked")
	seg.SetHAlign(gtk.AlignCenter)
	a.srcButtons = map[string]*gtk.ToggleButton{}
	var first *gtk.ToggleButton
	for _, s := range []struct{ id, text string }{{SourceMonitor, "Monitor"}, {SourceWindow, "Window"}, {SourceRegion, "Region"}} {
		b := gtk.NewToggleButtonWithLabel(s.text)
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		id := s.id
		b.ConnectToggled(func() {
			if b.Active() {
				a.stack.SetVisibleChildName(id)
				a.updateThumbTimer()
				if id == SourceWindow {
					a.refreshWindowThumbs()
				}
				a.updatePreviewBase()
			}
		})
		a.srcButtons[id] = b
		seg.Append(b)
	}
	box.Append(seg)

	a.stack = gtk.NewStack()
	a.stack.AddNamed(a.buildMonitorPage(), SourceMonitor)
	a.stack.AddNamed(a.buildWindowPage(), SourceWindow)
	hint := label("You will be asked to drag out an area once you press Record.", "dim-label")
	hint.SetWrap(true)
	hint.SetMarginTop(12)
	hint.SetMarginBottom(12)
	a.regionHint = hint
	a.stack.AddNamed(hint, SourceRegion)
	box.Append(a.stack)

	a.srcButtons[a.settings.Source].SetActive(true)
	a.stack.SetVisibleChildName(a.settings.Source)
	return box
}

func (a *app) buildMonitorPage() *gtk.Box {
	page := gtk.NewBox(gtk.OrientationVertical, 8)

	a.flow = gtk.NewFlowBox()
	a.flow.SetSelectionMode(gtk.SelectionSingle)
	a.flow.SetHomogeneous(true)
	a.flow.SetMaxChildrenPerLine(3)
	a.flow.SetColumnSpacing(8)
	a.flow.SetRowSpacing(8)
	a.flow.SetHAlign(gtk.AlignCenter)
	a.flow.ConnectSelectedChildrenChanged(func() {
		if sel := a.flow.SelectedChildren(); len(sel) > 0 {
			if i := sel[0].Index(); i >= 0 && i < len(a.monitors) {
				a.monName = a.monitors[i].Name
				a.updatePreviewBase()
			}
		}
	})
	page.Append(a.flow)

	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetHAlign(gtk.AlignCenter)
	row.Append(label("Skip top bar (pixels)"))
	a.crop = gtk.NewSpinButtonWithRange(0, 500, 1)
	a.crop.SetValue(float64(a.settings.CropTop))
	row.Append(a.crop)
	a.cropRow = row
	page.Append(row)
	return page
}

func (a *app) buildWindowPage() *gtk.Box {
	page := gtk.NewBox(gtk.OrientationVertical, 6)

	a.winList = gtk.NewListBox()
	a.winList.SetSelectionMode(gtk.SelectionSingle)
	a.winList.AddCSSClass("boxed-list")
	a.winList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		a.winIdx = -1
		if row != nil {
			a.winIdx = row.Index()
		}
		a.updatePreviewBase()
	})

	sc := gtk.NewScrolledWindow()
	sc.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sc.SetMinContentHeight(190)
	sc.SetMaxContentHeight(260)
	sc.SetPropagateNaturalHeight(true)
	sc.SetChild(a.winList)
	page.Append(sc)

	a.windowHint = label("", "dim-label", "caption")
	a.windowHint.SetWrap(true)
	page.Append(a.windowHint)
	return page
}

func (a *app) pill(text string, active bool, onIcon, offIcon string) *gtk.ToggleButton {
	img := gtk.NewImageFromIconName(onIcon)
	lbl := gtk.NewLabel(text)
	inner := gtk.NewBox(gtk.OrientationHorizontal, 8)
	inner.SetHAlign(gtk.AlignCenter)
	inner.Append(img)
	inner.Append(lbl)

	b := gtk.NewToggleButton()
	b.AddCSSClass("er-pill")
	b.AddCSSClass("flat")
	b.SetHExpand(true)
	b.SetChild(inner)
	b.ConnectToggled(func() {
		if b.Active() {
			img.SetFromIconName(onIcon)
		} else {
			img.SetFromIconName(offIcon)
		}
	})
	b.SetActive(active)
	if !active {
		img.SetFromIconName(offIcon)
	}
	return b
}

func (a *app) buildAudio() *gtk.Box {
	outer := gtk.NewBox(gtk.OrientationVertical, 10)
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.SetHomogeneous(true)
	a.desktop = a.pill("Desktop sound", a.settings.Desktop, "audio-speakers-symbolic", "audio-volume-muted-symbolic")
	a.mic = a.pill("Microphone", a.settings.Mic, "audio-input-microphone-symbolic", "microphone-disabled-symbolic")
	box.Append(a.desktop)
	box.Append(a.mic)
	box.Append(a.buildCamera())
	outer.Append(box)
	a.micSel = a.settings.MicDevice
	outer.Append(a.buildMicRow())
	a.probeCameras()
	a.probeMics()
	return outer
}

func (a *app) buildCountdown() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("linked")
	box.SetHAlign(gtk.AlignCenter)
	a.countdown = map[int]*gtk.ToggleButton{}
	var first *gtk.ToggleButton
	for _, n := range []int{0, 3, 5, 10} {
		b := gtk.NewToggleButtonWithLabel(fmt.Sprintf("%d s", n))
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		a.countdown[n] = b
		box.Append(b)
	}
	a.countdown[a.settings.Countdown].SetActive(true)
	return box
}

func (a *app) collect() Settings {
	s := Settings{
		Mode:          a.settings.Mode,
		ReplaySeconds: a.settings.ReplaySeconds,
		Source:        SourceMonitor,
		Monitor:       a.monName,
		CropTop:       a.crop.ValueAsInt(),
		Desktop:       a.desktop.Active(),
		Mic:           a.mic.Active(),

		MicDevice: a.micSel,
	}
	for n, b := range a.replayDuration {
		if b.Active() {
			s.ReplaySeconds = n
		}
	}
	a.cameraSettings(&s)
	for id, b := range a.srcButtons {
		if b.Active() {
			s.Source = id
		}
	}
	for n, b := range a.countdown {
		if b.Active() {
			s.Countdown = n
		}
	}
	return s
}

func (a *app) toast(msg string) {
	t := adw.NewToast(glib.MarkupEscapeText(msg))
	t.SetTimeout(6)
	a.toasts.AddToast(t)
}

// refresh reloads monitors and windows off the main thread.
func (a *app) refresh() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mons, merr := listMonitors(ctx)
		wins, werr := listWindows(ctx)
		glib.IdleAdd(func() {
			if merr != nil {
				log.Printf("monitors: %v", merr)
			} else {
				a.setMonitors(mons)
			}
			if werr != nil {
				log.Printf("windows: %v", werr)
			} else {
				a.setWindows(wins)
			}
		})
	}()
}

func (a *app) setMonitors(mons []sway.Monitor) {
	a.monitors = mons
	a.flow.RemoveAll()
	a.thumbs = map[string]*gtk.Picture{}

	selected := -1
	for i, m := range mons {
		if m.Name == a.monName {
			selected = i
		}
	}
	if selected < 0 {
		for i, m := range mons {
			if m.Focused {
				selected = i
			}
		}
	}
	if selected < 0 && len(mons) > 0 {
		selected = 0
	}

	// Pictures request their texture's natural size, which would push the
	// tiles onto one column; keep the monitors side by side instead.
	a.flow.SetMinChildrenPerLine(uint(max(1, min(len(mons), 3))))

	for i, m := range mons {
		pic := gtk.NewPicture()
		pic.AddCSSClass("er-thumb")
		pic.SetContentFit(gtk.ContentFitCover)
		pic.SetCanShrink(true)
		pic.SetSizeRequest(176, 99)
		pic.SetOverflow(gtk.OverflowHidden)

		tile := gtk.NewBox(gtk.OrientationVertical, 4)
		tile.AddCSSClass("er-tile")
		tile.Append(pic)
		name := label(m.Name)
		name.AddCSSClass("heading")
		tile.Append(name)
		tile.Append(label(fmt.Sprintf("%d × %d", m.Width, m.Height), "dim-label", "caption"))

		child := gtk.NewFlowBoxChild()
		child.AddCSSClass("er-tilechild")
		child.SetChild(tile)
		a.flow.Append(child)
		if i == selected {
			a.flow.SelectChild(child)
		}
		a.thumbs[m.Name] = pic
		a.captureThumb(m.Name)
	}
	a.updatePreviewBase()
}

// captureThumb grabs one thumbnail off the main thread, never overlapping
// another capture of the same output. It is skipped once the dialogue is
// hidden so a late refresh cannot run grim during a recording.
func (a *app) captureThumb(output string) {
	if a.inflight[output] || !a.thumbsWanted() || a.stack.VisibleChildName() != SourceMonitor {
		return
	}
	a.inflight[output] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		data, err := grabThumbnail(ctx, output)
		glib.IdleAdd(func() {
			delete(a.inflight, output)
			pic := a.thumbs[output]
			if err != nil || len(data) == 0 || pic == nil {
				return
			}
			if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data)); err == nil {
				pic.SetPaintable(tex)
				if output == a.monName {
					a.updatePreviewBase()
				}
			}
		})
	}()
}

func (a *app) thumbsWanted() bool {
	return !a.busy && !a.closing && a.win != nil && a.win.IsVisible() && a.onSetupPage() &&
		(a.stack.VisibleChildName() == SourceMonitor || a.stack.VisibleChildName() == SourceWindow)
}

// updateThumbTimer runs the refresh timer only whilst thumbnails are on show.
func (a *app) updateThumbTimer() {
	if a.thumbTimer != 0 || !a.thumbsWanted() {
		return
	}
	a.thumbTimer = glib.TimeoutAdd(uint(thumbInterval/time.Millisecond), func() bool {
		if !a.thumbsWanted() {
			a.thumbTimer = 0
			return false
		}
		if a.stack.VisibleChildName() == SourceWindow {
			a.refreshWindowThumbs()
		} else {
			for name := range a.thumbs {
				a.captureThumb(name)
			}
		}
		return true
	})
}

func (a *app) refreshWindowThumbs() {
	if a.windowRefreshing || !a.thumbsWanted() || a.stack.VisibleChildName() != SourceWindow {
		return
	}
	a.windowRefreshing = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		wins, err := listWindows(ctx)
		glib.IdleAdd(func() {
			a.windowRefreshing = false
			if err != nil || !a.thumbsWanted() || a.stack.VisibleChildName() != SourceWindow {
				return
			}
			changed := len(wins) != len(a.windows)
			if !changed {
				for i, w := range wins {
					if w.ID != a.windows[i].ID {
						changed = true
						break
					}
				}
			}
			if changed {
				a.setWindows(wins)
			}
			for _, w := range wins {
				a.captureWindowThumb(w)
			}
			a.updatePreviewBase()
		})
	}()
}

func (a *app) captureWindowThumb(w sway.Window) {
	thumb, ok := a.windowThumbs[w.ID]
	key := "window:" + w.ID
	if !ok || a.inflight[key] || !a.thumbsWanted() || a.stack.VisibleChildName() != SourceWindow {
		return
	}
	a.inflight[key] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		data, err := grabWindowThumbnail(ctx, w)
		glib.IdleAdd(func() {
			delete(a.inflight, key)
			// A list refresh replaces the row, even when the window ID is unchanged
			if a.windowThumbs[w.ID].picture != thumb.picture || !a.thumbsWanted() || a.stack.VisibleChildName() != SourceWindow {
				return
			}
			if err == nil {
				if tex, texErr := gdk.NewTextureFromBytes(glib.NewBytes(data)); texErr == nil {
					thumb.picture.SetPaintable(tex)
					thumb.frame.SetVisibleChildName("image")
					a.updatePreviewBase()
					return
				}
			}
			thumb.picture.SetPaintable(nil)
			thumb.frame.SetVisibleChildName("icon")
			a.updatePreviewBase()
		})
	}()
}

func (a *app) stopThumbTimer() {
	if a.thumbTimer != 0 {
		glib.SourceRemove(a.thumbTimer)
		a.thumbTimer = 0
	}
}

func (a *app) setWindows(wins []sway.Window) {
	selected := ""
	if a.winIdx >= 0 && a.winIdx < len(a.windows) {
		selected = a.windows[a.winIdx].ID
	}
	a.windows = wins
	a.winIdx = -1
	a.winList.RemoveAll()
	a.windowThumbs = make(map[string]windowThumbnail, len(wins))

	var theme *gtk.IconTheme
	if display := gdk.DisplayGetDefault(); display != nil {
		theme = gtk.IconThemeGetForDisplay(display)
	}

	for _, w := range wins {
		title := w.Title
		if title == "" {
			title = w.Class
		}
		row := adw.NewActionRow()
		row.SetTitle(glib.MarkupEscapeText(title))
		row.SetTitleLines(1)
		row.SetSubtitle(glib.MarkupEscapeText(fmt.Sprintf("%s · %d × %d", w.Class, w.Width, w.Height)))
		row.SetSubtitleLines(1)
		img := gtk.NewImageFromIconName(iconFor(theme, w.Class))
		img.SetPixelSize(32)
		pic := gtk.NewPicture()
		pic.SetContentFit(gtk.ContentFitContain)
		pic.SetCanShrink(true)
		pic.SetSizeRequest(windowPreviewWidth, windowPreviewHeight)
		frame := gtk.NewStack()
		frame.AddCSSClass("er-thumb")
		frame.SetOverflow(gtk.OverflowHidden)
		frame.SetSizeRequest(windowPreviewWidth, windowPreviewHeight)
		frame.SetVAlign(gtk.AlignCenter)
		frame.AddNamed(img, "icon")
		frame.AddNamed(pic, "image")
		frame.SetVisibleChildName("icon")
		row.AddPrefix(frame)
		a.winList.Append(row)
		a.windowThumbs[w.ID] = windowThumbnail{frame: frame, picture: pic}
		if w.ID == selected {
			a.winList.SelectRow(&row.ListBoxRow)
		}
		a.captureWindowThumb(w)
	}
}

// iconFor resolves an application icon from a window class, with a generic fallback.
func iconFor(theme *gtk.IconTheme, class string) string {
	if theme != nil && class != "" {
		for _, c := range []string{class, strings.ToLower(class), strings.ToLower(filepath.Ext(class)), lastDotted(class)} {
			if c != "" && theme.HasIcon(c) {
				return c
			}
		}
	}
	return "application-x-executable"
}

func lastDotted(class string) string {
	return strings.ToLower(class[strings.LastIndex(class, ".")+1:])
}

func (a *app) startCapture() {
	if a.res.dialogOpen {
		return
	}
	a.cancelTrimImport()
	if a.busy || a.closing {
		return
	}
	if (a.settings.Mode == ModeVideo || a.settings.Mode == ModeReplay) && a.mic.Active() && a.micSel != "" && !a.micsKnown {
		a.toast("Checking the microphones; please press Record again in a moment")
		return
	}
	if captureDisabled {
		a.toast("Demo mode: capture is disabled")
		return
	}
	s := a.collect()
	var sel Selection
	var chosen *sway.Window
	switch s.Source {
	case SourceMonitor:
		sel.Monitor = a.monName
	case SourceWindow:
		if a.winIdx >= 0 && a.winIdx < len(a.windows) {
			chosen = &a.windows[a.winIdx]
			sel.Geometry = chosen.Geometry()
		}
	}
	if s.Mode == ModeReplay {
		sel.Monitor = a.monName
		chosen = nil
	}
	sel.CameraDevice = a.camSel
	req, err := BuildRequest(s, sel)
	if err != nil {
		a.toast("Pray select a " + map[string]string{SourceMonitor: "monitor", SourceWindow: "window"}[s.Source] + " first.")
		return
	}

	a.saveSettings(s)
	if s.Mode == ModeVideo {
		req.Options["quiet_finish"] = true
	}

	a.busy = true
	a.mu.Lock()
	a.pending = true
	a.stopLatched = false
	a.mu.Unlock()
	a.stopThumbTimer()
	a.stopPreview()
	a.win.SetVisible(false)
	go a.run(req, chosen)
}

func (a *app) run(req protocol.Request, chosen *sway.Window) {
	// The camera must be free before the recorder opens it.
	if !a.gate.Wait(previewRelease) {
		a.finish(nil, errors.New("the camera is still in use"))
		return
	}
	time.Sleep(hideSettle)

	if err := a.b.EnsureDaemon(); err != nil {
		a.finish(nil, err)
		return
	}

	// Hiding the dialogue may have reflowed tiled windows; locate it afresh
	// by its stable identifier.
	if chosen != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		w, err := sway.FindWindow(ctx, chosen.ID)
		cancel()
		if err != nil {
			if errors.Is(err, sway.ErrWindowGone) {
				err = errors.New("the selected window has since closed")
			}
			a.finish(nil, err)
			return
		}
		req.Options["geometry"] = w.Geometry()
	}

	resp, err := a.b.SendStart(req)
	if err == nil && resp == nil {
		err = errors.New("the daemon did not respond")
	} else if err == nil && !resp.Success {
		err = errors.New(resp.Message)
	}
	a.mu.Lock()
	a.pending = false
	latched := a.stopLatched
	a.stopLatched = false
	a.mu.Unlock()
	if err != nil {
		a.finish(nil, err)
		return
	}
	if req.Action == "replay-start" {
		glib.IdleAdd(func() {
			a.busy = false
			if resp.State != nil && resp.State.Replay {
				a.showReplay(resp.State)
				if a.replayShowPending {
					a.win.Present()
				}
			}
			a.replayShowPending = false
		})
		return
	}
	if req.Action == "virtual-camera-start" {
		a.waitVirtualCamera(latched)
		return
	}
	if req.Action == "screenshot" {
		if resp.File == "" {
			a.finish(nil, errors.New("the daemon did not report a screenshot file"))
			return
		}
		info, err := ProbeImage(resp.File)
		if err != nil {
			log.Printf("inspect %s: %v", resp.File, err)
		}
		a.finish(&savedCapture{file: resp.File, info: info, mode: ModeScreenshot}, nil)
		return
	}

	file := ""
	if resp.State != nil {
		file = resp.State.RecordingFile
	}
	if file == "" {
		a.finish(nil, errors.New("the daemon did not report a recording file"))
		return
	}

	if latched {
		a.stopRecording()
	}

	// The request returns once the recorder is running; wait for it to end.
	failures := 0
	for {
		time.Sleep(500 * time.Millisecond)
		st := a.state()
		if st == nil {
			failures++
			if failures >= 3 {
				a.finish(nil, errors.New("the daemon could not be reached to confirm the recording"))
				return
			}
			continue
		}
		failures = 0
		if !st.Recording {
			break
		}
	}

	if _, err := os.Stat(file); err != nil {
		a.finish(nil, fmt.Errorf("the recording %s could not be found", filepath.Base(file)))
		return
	}

	a.finish(&savedCapture{file: file, info: probeSavedVideo(file), mode: ModeVideo}, nil)
}

// waitVirtualCamera returns to the dialogue once the virtual camera stops.
func (a *app) waitVirtualCamera(latched bool) {
	if latched {
		a.stopVirtualCamera()
	}
	failures := 0
	for {
		st := a.state()
		if st == nil {
			failures++
			if failures >= 3 {
				a.finish(nil, errors.New("the daemon could not be reached to follow the virtual camera"))
				return
			}
		} else {
			failures = 0
			if !st.VirtualCamera {
				a.finish(nil, nil)
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

type savedCapture struct {
	file string
	info MediaInfo
	mode string
}

func (a *app) finish(saved *savedCapture, err error) {
	a.mu.Lock()
	a.pending = false
	a.mu.Unlock()
	glib.IdleAdd(func() {
		a.busy = false
		if a.settingsPath != "" {
			a.camPos = LoadSettings(a.settingsPath).CameraPosition
			a.settings.CameraPosition = a.camPos
			if a.prevOverlay != nil {
				a.prevOverlay.QueueResize()
			}
		}
		a.win.SetVisible(true)
		a.win.Present()
		if err != nil {
			a.showSetup()
			kind := map[string]string{ModeScreenshot: "screenshot", ModeCamera: "virtual camera", ModeReplay: "replay"}[a.settings.Mode]
			if kind == "" {
				kind = "recording"
			}
			a.toast("Regrettably, the " + kind + " failed: " + err.Error())
			return
		}
		if saved == nil {
			a.showSetup()
			a.toast("The virtual camera has stopped.")
			return
		}
		a.showResult(saved.file, saved.info, saved.mode)
	})
}
