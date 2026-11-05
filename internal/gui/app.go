//go:build !nogui

// Package gui implements the GTK4/libadwaita recording dialogue.
package gui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sway-easyshot/internal/sway"
	"sway-easyshot/pkg/protocol"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const appID = "com.chmouel.SwayEasyshot.Recorder"

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

// thumbInterval is how often visible monitor thumbnails are refreshed.
const thumbInterval = 2 * time.Second

const css = `
.er-tile { border-radius: 12px; padding: 8px; }
.er-thumb { border-radius: 8px; background: alpha(currentColor, 0.08); }
flowboxchild.er-tilechild { border-radius: 14px; padding: 2px; background: none; outline: none; }
flowboxchild.er-tilechild:selected { background: alpha(@accent_bg_color, 0.14); box-shadow: inset 0 0 0 2px @accent_bg_color; }
.er-pill { border-radius: 999px; padding: 10px 20px; opacity: 0.65; }
.er-pill:checked { opacity: 1; background: @accent_bg_color; color: @accent_fg_color; }
.er-frame { border-radius: 12px; background: alpha(currentColor, 0.08); }
.er-section { font-weight: 700; font-size: 0.85em; opacity: 0.7; letter-spacing: 0.04em; }
`

type app struct {
	b            Backend
	gtkApp       *adw.Application
	win          *adw.ApplicationWindow
	toasts       *adw.ToastOverlay
	pages        *gtk.Stack
	res          resultView
	settings     Settings
	settingsPath string

	srcButtons map[string]*gtk.ToggleButton
	stack      *gtk.Stack
	flow       *gtk.FlowBox
	crop       *gtk.SpinButton
	winList    *gtk.ListBox
	desktop    *gtk.ToggleButton
	mic        *gtk.ToggleButton
	countdown  map[int]*gtk.ToggleButton

	monitors []sway.Monitor
	windows  []sway.Window
	winIdx   int
	monName  string

	thumbs     map[string]*gtk.Picture
	inflight   map[string]bool
	thumbTimer glib.SourceHandle

	busy    bool
	started bool

	// mu guards pending and stopLatched, shared with the start goroutine.
	mu          sync.Mutex
	pending     bool
	stopLatched bool
}

// Run opens the dialogue and blocks until the application exits.
func Run(b Backend) error {
	a := &app{b: b, winIdx: -1, thumbs: map[string]*gtk.Picture{}, inflight: map[string]bool{}}
	a.settingsPath, _ = settingsPath()
	a.settings = LoadSettings(a.settingsPath)
	a.monName = a.settings.Monitor

	a.gtkApp = adw.NewApplication(appID, gio.ApplicationFlagsNone)
	a.gtkApp.ConnectActivate(a.activate)
	if code := a.gtkApp.Run([]string{os.Args[0]}); code != 0 {
		return fmt.Errorf("record-gui exited with status %d", code)
	}
	return nil
}

func (a *app) activate() {
	if !a.started {
		a.started = true
		a.build()
		a.gtkApp.Hold()
	}

	if a.busy {
		// A second launch acts as a stop key. Whilst the start is still
		// pending there is nothing to stop yet, so the request is latched.
		a.mu.Lock()
		if a.pending {
			a.stopLatched = true
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		go a.stopRecording()
		return
	}

	go func() {
		if st := a.state(); st != nil && st.Recording {
			a.stopRecording()
			for i := 0; i < 100; i++ {
				if st := a.state(); st == nil || !st.Recording {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
		glib.IdleAdd(func() {
			a.win.Present()
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

func (a *app) state() *protocol.State {
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
	a.win.SetTitle(setupTitle)
	a.win.SetDefaultSize(520, -1)
	// A fixed-size window is floated by tiling compositors, keeping the dialogue compact.
	a.win.SetResizable(false)
	a.win.ConnectCloseRequest(func() bool {
		a.stopThumbTimer()
		a.gtkApp.Quit()
		return false
	})

	key := gtk.NewEventControllerKey()
	key.SetPropagationPhase(gtk.PhaseCapture)
	key.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		switch keyval {
		case gdk.KEY_Escape:
			if a.res.dialogOpen {
				return false
			}
			if !a.onSetupPage() {
				a.showSetup()
				return true
			}
			a.win.Close()
			return true
		case gdk.KEY_Return, gdk.KEY_KP_Enter:
			if !a.onSetupPage() {
				return false
			}
			a.startRecording()
			return true
		}
		return false
	})
	a.win.AddController(key)

	body := gtk.NewBox(gtk.OrientationVertical, 14)
	body.SetMarginTop(8)
	body.SetMarginBottom(18)
	body.SetMarginStart(18)
	body.SetMarginEnd(18)

	body.Append(section("Source"))
	body.Append(a.buildSource())
	body.Append(section("Audio"))
	body.Append(a.buildAudio())
	body.Append(section("Countdown"))
	body.Append(a.buildCountdown())

	rec := gtk.NewButtonWithLabel("Record")
	rec.AddCSSClass("suggested-action")
	rec.AddCSSClass("pill")
	rec.SetHAlign(gtk.AlignCenter)
	rec.SetMarginTop(6)
	rec.SetSizeRequest(200, -1)
	rec.ConnectClicked(a.startRecording)
	body.Append(rec)

	a.pages = gtk.NewStack()
	a.pages.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	a.pages.SetVhomogeneous(false)
	a.pages.AddNamed(body, pageSetup)
	a.pages.AddNamed(a.buildResult(), pageResult)
	a.pages.SetVisibleChildName(pageSetup)

	a.toasts = adw.NewToastOverlay()
	a.toasts.SetChild(a.pages)

	root := gtk.NewBox(gtk.OrientationVertical, 0)
	root.Append(adw.NewHeaderBar())
	root.Append(a.toasts)
	a.win.SetContent(root)
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
	})

	sc := gtk.NewScrolledWindow()
	sc.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	sc.SetMinContentHeight(190)
	sc.SetMaxContentHeight(260)
	sc.SetPropagateNaturalHeight(true)
	sc.SetChild(a.winList)
	page.Append(sc)

	page.Append(label("A window is recorded as its present on-screen area; moving it will not be followed.", "dim-label", "caption"))
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
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.SetHomogeneous(true)
	a.desktop = a.pill("Desktop sound", a.settings.Desktop, "audio-speakers-symbolic", "audio-volume-muted-symbolic")
	a.mic = a.pill("Microphone", a.settings.Mic, "audio-input-microphone-symbolic", "microphone-disabled-symbolic")
	box.Append(a.desktop)
	box.Append(a.mic)
	return box
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
		Source:  SourceMonitor,
		Monitor: a.monName,
		CropTop: a.crop.ValueAsInt(),
		Desktop: a.desktop.Active(),
		Mic:     a.mic.Active(),
	}
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
		mons, merr := sway.ListMonitors(ctx)
		wins, werr := sway.ListWindows(ctx)
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
}

// captureThumb grabs one thumbnail off the main thread, never overlapping
// another capture of the same output. It is skipped once the dialogue is
// hidden so a late refresh cannot run grim during a recording.
func (a *app) captureThumb(output string) {
	if a.inflight[output] || !a.thumbsWanted() {
		return
	}
	a.inflight[output] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		data, err := exec.CommandContext(ctx, "grim", "-o", output, "-s", "0.1", "-t", "jpeg", "-q", "70", "-").Output() //nolint:gosec
		glib.IdleAdd(func() {
			delete(a.inflight, output)
			pic := a.thumbs[output]
			if err != nil || len(data) == 0 || pic == nil {
				return
			}
			if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data)); err == nil {
				pic.SetPaintable(tex)
			}
		})
	}()
}

func (a *app) thumbsWanted() bool {
	return a.win != nil && a.win.IsVisible() && a.onSetupPage() && a.stack.VisibleChildName() == SourceMonitor
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
		for name := range a.thumbs {
			a.captureThumb(name)
		}
		return true
	})
}

func (a *app) stopThumbTimer() {
	if a.thumbTimer != 0 {
		glib.SourceRemove(a.thumbTimer)
		a.thumbTimer = 0
	}
}

func (a *app) setWindows(wins []sway.Window) {
	a.windows = wins
	a.winIdx = -1
	a.winList.RemoveAll()

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
		img := gtk.NewImageFromIconName(iconFor(theme, w.Class))
		img.SetPixelSize(32)
		row.AddPrefix(img)
		a.winList.Append(row)
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

func (a *app) startRecording() {
	if a.busy {
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
	req, err := BuildRequest(s, sel)
	if err != nil {
		a.toast("Pray select a " + map[string]string{SourceMonitor: "monitor", SourceWindow: "window"}[s.Source] + " first.")
		return
	}

	if a.settingsPath != "" {
		if err := SaveSettings(a.settingsPath, s); err != nil {
			log.Printf("saving settings: %v", err)
		}
	}

	req.Options["quiet_finish"] = true

	a.busy = true
	a.mu.Lock()
	a.pending = true
	a.stopLatched = false
	a.mu.Unlock()
	a.stopThumbTimer()
	a.win.SetVisible(false)
	go a.run(req, chosen)
}

func (a *app) run(req protocol.Request, chosen *sway.Window) {
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
	if err == nil && !resp.Success {
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
			if failures++; failures >= 3 {
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	info, err := ProbeMedia(ctx, file)
	cancel()
	if err != nil {
		// The file is saved; show the card with whatever details are known.
		log.Printf("inspect %s: %v", file, err)
		info = MediaInfo{}
		if st, serr := os.Stat(file); serr == nil {
			info.Size = st.Size()
		}
	}
	a.finish(&savedRecording{file: file, info: info}, nil)
}

type savedRecording struct {
	file string
	info MediaInfo
}

func (a *app) finish(saved *savedRecording, err error) {
	a.mu.Lock()
	a.pending = false
	a.mu.Unlock()
	glib.IdleAdd(func() {
		a.busy = false
		a.win.SetVisible(true)
		a.win.Present()
		if err != nil {
			a.showSetup()
			a.toast("Regrettably, the recording failed: " + err.Error())
			return
		}
		a.showResult(saved.file, saved.info)
	})
}
