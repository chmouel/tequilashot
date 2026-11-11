//go:build !nogui

package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/chmouel/tequilashot/internal/denoise"
	"github.com/chmouel/tequilashot/internal/prefs"
	"github.com/chmouel/tequilashot/internal/segment"
	"github.com/chmouel/tequilashot/pkg/protocol"
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// prefsDialog edits the preferences file. Every change is saved at once and
// published to this process; the daemon reads the file before each command.
type prefsDialog struct {
	a       *app
	dlg     *adw.PreferencesDialog
	p       prefs.Prefs
	persist bool
	// status is the daemon's view of its environment, nil until it replies.
	status *protocol.PreferencesStatus
	// daemonWarning names why the daemon's settings could not be used, empty
	// once it answers; the window warns instead of claiming they apply.
	daemonWarning [2]string
	// refresh re-renders rows that depend on the save folder or the
	// environment.
	refresh []func()

	cleanupRow     *adw.ExpanderRow
	syncingCleanup bool
	downloading    bool
}

// showPreferences presents the preferences dialogue over the capture window.
func (a *app) showPreferences() {
	a.openPreferences()
}

func (a *app) openPreferences() *prefsDialog {
	if a.prefsOpen {
		return nil
	}
	d := &prefsDialog{a: a, p: prefs.Current().Clone(), persist: demoSettings == nil}
	d.dlg = adw.NewPreferencesDialog()
	d.dlg.SetSearchEnabled(true)
	d.dlg.SetContentWidth(720)
	d.dlg.Add(d.generalPage())
	d.dlg.Add(d.recordingPage())
	d.dlg.Add(d.screenshotsPage())
	d.dlg.Add(d.advancedPage())
	d.rerender()

	a.prefsOpen = true
	d.dlg.ConnectClosed(func() { a.prefsOpen = false })
	d.dlg.Present(a.win)
	go d.fetchStatus()
	return d
}

// fetchStatus asks the daemon how its environment resolves the settings, as
// the daemon may run under systemd with variables the dialogue cannot see.
func (d *prefsDialog) fetchStatus() {
	if d.a.b.Send == nil {
		return
	}
	resp, err := d.a.b.Send(protocol.Request{Command: "execute", Action: "preferences-status"})
	if err != nil || resp == nil {
		d.warnDaemon("The Daemon Could Not Be Asked",
			"Its settings could not be read, so the marks in this window may not match it. Check that the daemon is running, by taking a capture, and open this window again.")
		return
	}
	if !resp.Success {
		if strings.Contains(resp.Message, "Unknown action") {
			d.warnDaemon("The Running Daemon Is Older Than This Window",
				"It does not read these preferences, so a capture it starts keeps the settings it was already given. Restart the daemon, once no recording or replay is under way, then open this window again.")
			return
		}
		d.warnDaemon("The Daemon Refused the Query", resp.Message)
		return
	}
	var st protocol.PreferencesStatus
	if json.Unmarshal([]byte(resp.Message), &st) != nil {
		d.warnDaemon("The Daemon Gave an Answer This Window Does Not Understand",
			"Its settings are probably too old or too new for this window. Restart the daemon once no recording or replay is under way.")
		return
	}
	glib.IdleAdd(func() {
		d.status = &st
		d.rerender()
	})
}

// warnDaemon records why the daemon's settings could not be used, so the
// window says so instead of pretending they have taken effect there.
func (d *prefsDialog) warnDaemon(title, body string) {
	glib.IdleAdd(func() {
		d.daemonWarning = [2]string{title, body}
		d.rerender()
	})
}

func (d *prefsDialog) rerender() {
	for _, f := range d.refresh {
		f()
	}
}

// update applies a change, saves it and only then makes it current for this
// process, so a write failure can never leave the window and the daemon with
// different settings. It reports whether the change was kept.
func (d *prefsDialog) update(change func(*prefs.Prefs)) bool {
	candidate := d.p.Clone()
	change(&candidate)
	if d.persist {
		if err := prefs.Save(prefs.Path(), candidate); err != nil {
			d.restore("The preferences could not be saved, so nothing was changed: " + err.Error())
			return false
		}
	}
	d.p = candidate
	prefs.Set(d.p.Clone())
	return true
}

// restore reopens the dialogue from the saved preferences after a change that
// could not be written, so no control shows a value the daemon cannot see, and
// carries the reason across to the dialogue that replaces this one.
func (d *prefsDialog) restore(msg string) {
	glib.IdleAdd(func() {
		d.a.prefsOpen = false
		d.dlg.Close()
		if next := d.a.openPreferences(); next != nil {
			next.toast(msg)
		}
	})
}

func (d *prefsDialog) toast(msg string) {
	t := adw.NewToast(glib.MarkupEscapeText(msg))
	t.SetTimeout(5)
	d.dlg.AddToast(t)
}

// envSet reports whether an environment variable overrides a preference,
// preferring the daemon's answer to the dialogue's own environment.
func (d *prefsDialog) envSet(name string) bool {
	if st := d.status; st != nil {
		switch name {
		case prefs.EnvSaveLocation:
			return st.SaveDirEnv
		case prefs.EnvAIModel:
			return st.AIModelEnv
		case prefs.EnvModnetModel:
			return st.ModnetModelEnv
		case prefs.EnvOnnxRuntimeLib:
			return st.OnnxRuntimeLibEnv
		}
	}
	return os.Getenv(name) != ""
}

// modelPath is the MODNet file the daemon's camera bubble loads, resolved like
// the daemon does but with the preferences being edited.
func (d *prefsDialog) modelPath() string {
	if d.envSet(prefs.EnvModnetModel) {
		if d.status != nil && d.status.ModnetModel != "" {
			return d.status.ModnetModel
		}
		return os.Getenv(prefs.EnvModnetModel)
	}
	if d.p.ModnetModel != "" {
		return d.p.ModnetModel
	}
	if d.status != nil && d.status.ModnetModelDefault != "" {
		return d.status.ModnetModelDefault
	}
	return segment.DefaultModnetModelPath()
}

// legacySaveDir is the pre-preferences save folder as the daemon sees it.
func (d *prefsDialog) legacySaveDir() string {
	if d.status != nil && d.status.LegacySaveDir != "" {
		return d.status.LegacySaveDir
	}
	return prefs.LegacySaveDir()
}

// saveDir is the folder new captures go to, as the daemon resolves it.
func (d *prefsDialog) saveDir() string {
	if d.envSet(prefs.EnvSaveLocation) {
		if d.status != nil {
			return d.status.SaveDir
		}
		return os.Getenv(prefs.EnvSaveLocation)
	}
	if d.p.SaveLocation != "" {
		return d.p.SaveLocation
	}
	return d.legacySaveDir()
}

func overriddenBy(env string) string {
	return "Overridden by $" + env
}

// ---- General ----

func (d *prefsDialog) generalPage() *adw.PreferencesPage {
	page := newPage("General", "preferences-system-symbolic")

	daemon := newGroup("Daemon", "")
	stale := adw.NewActionRow()
	stale.SetTitle("The Daemon Could Not Be Asked")
	stale.SetSubtitle("Its settings could not be read, so the marks in this window may not match it.")
	stale.AddPrefix(gtk.NewImageFromIconName("dialog-warning-symbolic"))
	stale.SetVisible(false)
	daemon.Add(stale)
	page.Add(daemon)
	d.refresh = append(d.refresh, func() {
		if d.daemonWarning[0] == "" {
			stale.SetVisible(false)
			return
		}
		stale.SetTitle(d.daemonWarning[0])
		stale.SetSubtitle(d.daemonWarning[1])
		stale.SetVisible(true)
	})

	storage := newGroup("Storage", "")
	folder := adw.NewActionRow()
	folder.SetTitle("Save Folder")
	folder.SetSubtitleSelectable(true)
	choose := iconButton("folder-open-symbolic", "Choose Folder", d.chooseFolder)
	reset := iconButton("edit-undo-symbolic", "Use the Default Folder", func() {
		d.update(func(p *prefs.Prefs) {
			p.SaveLocation = ""
			p.SaveLocationChanged = true
			p.CleanupFolder = ""
		})
		d.rerender()
	})
	open := iconButton("adw-external-link-symbolic", "Open in File Manager", func() {
		dir := d.saveDir()
		if err := os.MkdirAll(dir, 0o750); err != nil {
			d.toast("The folder could not be created: " + err.Error())
			return
		}
		detach("xdg-open", dir)
	})
	folder.AddSuffix(reset)
	folder.AddSuffix(choose)
	folder.AddSuffix(open)
	storage.Add(folder)
	d.refresh = append(d.refresh, func() {
		env := d.envSet(prefs.EnvSaveLocation)
		sub := d.saveDir()
		if env {
			sub += "\n" + overriddenBy(prefs.EnvSaveLocation)
		}
		folder.SetSubtitle(sub)
		choose.SetSensitive(!env)
		reset.SetVisible(!env && d.p.SaveLocation != "")
	})
	page.Add(storage)

	cleanup := newGroup("Tidying Up", "Remove old captures automatically so the save folder does not grow without end.")
	d.cleanupRow = adw.NewExpanderRow()
	d.cleanupRow.SetTitle("Delete Old Files")
	d.cleanupRow.SetSubtitle("Every file in the save folder older than the period below is deleted")
	d.cleanupRow.SetShowEnableSwitch(true)
	keep := adw.NewSpinRowWithRange(prefs.MinCleanupDays, prefs.MaxCleanupDays, 1)
	keep.SetTitle("Keep For")
	keep.SetSubtitle("Days")
	keep.SetValue(float64(d.p.CleanupDays))
	keep.NotifyProperty("value", func() {
		d.update(func(p *prefs.Prefs) { p.CleanupDays = int(keep.Value()) })
	})
	d.cleanupRow.AddRow(keep)
	d.cleanupRow.NotifyProperty("enable-expansion", d.onCleanupToggled)
	cleanup.Add(d.cleanupRow)
	d.refresh = append(d.refresh, func() {
		d.syncingCleanup = true
		d.cleanupRow.SetEnableExpansion(d.p.CleanupActiveWith(d.saveDir(), d.legacySaveDir()))
		d.syncingCleanup = false
	})
	page.Add(cleanup)

	statusBar := newGroup("Status Bar", "")
	statusBar.Add(switchRow("Hide Waybar", "Hides Waybar whilst a whole screen is captured, recorded or shared as a virtual camera", d.p.HideWaybar, func(on bool) {
		d.update(func(p *prefs.Prefs) { p.HideWaybar = on })
	}))
	page.Add(statusBar)
	return page
}

func (d *prefsDialog) chooseFolder() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Choose Save Folder")
	if dir := d.saveDir(); dir != "" {
		dialog.SetInitialFolder(gio.NewFileForPath(dir))
	}
	dialog.SelectFolder(context.Background(), &d.a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.SelectFolderFinish(res)
		if err != nil || file == nil || file.Path() == "" {
			return
		}
		path := file.Path()
		d.update(func(p *prefs.Prefs) {
			p.SaveLocation = path
			p.SaveLocationChanged = true
			p.CleanupFolder = ""
		})
		d.rerender()
	})
}

// onCleanupToggled asks for consent before deleting anything, as the folder
// may hold files that Tequilashot did not create.
func (d *prefsDialog) onCleanupToggled() {
	if d.syncingCleanup {
		return
	}
	if !d.cleanupRow.EnableExpansion() {
		d.update(func(p *prefs.Prefs) { p.CleanupEnabled = false })
		return
	}
	dir := d.saveDir()
	alert := adw.NewAlertDialog("Delete Old Files?",
		fmt.Sprintf("Every file in %s older than %d days will be deleted, including files Tequilashot did not create.", dir, d.p.CleanupDays))
	alert.AddResponse("cancel", "_Cancel")
	alert.AddResponse("enable", "_Delete Old Files")
	alert.SetResponseAppearance("enable", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response == "enable" {
			d.update(func(p *prefs.Prefs) {
				p.CleanupEnabled = true
				p.CleanupFolder = dir
			})
		}
		d.rerender()
	})
	alert.Present(d.dlg)
}

// ---- Recording ----

func (d *prefsDialog) recordingPage() *adw.PreferencesPage {
	page := newPage("Recording", "media-record-symbolic")

	video := newGroup("Video", "Used for screen recordings made with gpu-screen-recorder.")
	video.Add(comboRow("Frame Rate", "", mapLabels(prefs.FrameRates, func(v int) string { return fmt.Sprintf("%d fps", v) }),
		slices.Index(prefs.FrameRates, d.p.FrameRate), func(i int) {
			d.update(func(p *prefs.Prefs) { p.FrameRate = prefs.FrameRates[i] })
		}))
	video.Add(comboRow("Maximum Width", "Larger sources are scaled down", mapLabels(prefs.MaxWidths, func(v int) string {
		if v == 0 {
			return "Original Size"
		}
		return fmt.Sprintf("%d pixels", v)
	}), slices.Index(prefs.MaxWidths, d.p.MaxWidth), func(i int) {
		d.update(func(p *prefs.Prefs) { p.MaxWidth = prefs.MaxWidths[i] })
	}))
	video.Add(comboRow("Quality", "", mapLabels(prefs.Qualities, qualityLabel),
		slices.Index(prefs.Qualities, d.p.Quality), func(i int) {
			d.update(func(p *prefs.Prefs) { p.Quality = prefs.Qualities[i] })
		}))
	video.Add(comboRow("Codec", "", mapLabels(prefs.VideoCodecs, codecLabel),
		slices.Index(prefs.VideoCodecs, d.p.VideoCodec), func(i int) {
			d.update(func(p *prefs.Prefs) { p.VideoCodec = prefs.VideoCodecs[i] })
		}))
	video.Add(switchRow("Show Mouse Pointer", "", d.p.RecordCursor, func(on bool) {
		d.update(func(p *prefs.Prefs) { p.RecordCursor = on })
	}))
	page.Add(video)

	replay := newGroup("Instant Replay", "The replay buffer is always recorded in H.264 at a constant bitrate.")
	bitrate := adw.NewSpinRowWithRange(prefs.MinReplayBitrate, prefs.MaxReplayBitrate, 1000)
	bitrate.SetTitle("Bitrate")
	bitrate.SetSubtitle("Kilobits per second; applies from the next replay")
	bitrate.SetValue(float64(d.p.ReplayBitrate))
	bitrate.NotifyProperty("value", func() {
		d.update(func(p *prefs.Prefs) { p.ReplayBitrate = int(bitrate.Value()) })
	})
	replay.Add(bitrate)
	page.Add(replay)
	return page
}

func qualityLabel(q string) string {
	if q == "" {
		return "Automatic"
	}
	return strings.ToUpper(q[:1]) + strings.ReplaceAll(q[1:], "_", " ")
}

func codecLabel(c string) string {
	return map[string]string{"": "Automatic", "h264": "H.264", "hevc": "HEVC (H.265)", "av1": "AV1"}[c]
}

// ---- Screenshots ----

func (d *prefsDialog) screenshotsPage() *adw.PreferencesPage {
	page := newPage("Screenshots", "camera-photo-symbolic")

	capture := newGroup("Capture", "")
	capture.Add(switchRow("Include Mouse Pointer", "", d.p.ScreenshotCursor, func(on bool) {
		d.update(func(p *prefs.Prefs) { p.ScreenshotCursor = on })
	}))

	colour := adw.NewActionRow()
	colour.SetTitle("Selection Border Colour")
	colour.SetSubtitle("Used when choosing a region")
	cd := gtk.NewColorDialog()
	cd.SetWithAlpha(true)
	btn := gtk.NewColorDialogButton(cd)
	btn.SetVAlign(gtk.AlignCenter)
	rgba := gdk.NewRGBA(0, 0, 0, 1)
	if d.p.SelectionColour != "" && !rgba.Parse(d.p.SelectionColour) {
		rgba = gdk.NewRGBA(0, 0, 0, 1)
	}
	btn.SetRGBA(&rgba)
	resetColour := iconButton("edit-undo-symbolic", "Use the Default Colour", nil)
	resetColour.SetVisible(d.p.SelectionColour != "")
	syncing := false
	btn.NotifyProperty("rgba", func() {
		if syncing {
			return
		}
		hex := rgbaHex(btn.RGBA())
		d.update(func(p *prefs.Prefs) { p.SelectionColour = hex })
		resetColour.SetVisible(true)
	})
	resetColour.ConnectClicked(func() {
		d.update(func(p *prefs.Prefs) { p.SelectionColour = "" })
		syncing = true
		black := gdk.NewRGBA(0, 0, 0, 1)
		btn.SetRGBA(&black)
		syncing = false
		resetColour.SetVisible(false)
	})
	colour.AddSuffix(resetColour)
	colour.AddSuffix(btn)
	capture.Add(colour)
	page.Add(capture)

	ai := newGroup("Naming", "Selections copied to the clipboard can be named by aichat. Leave the model empty to use "+prefs.DefaultAIModel+".")
	model := entryRow("aichat Model", d.p.AIModel, func(text string) error {
		d.update(func(p *prefs.Prefs) { p.AIModel = text })
		return nil
	})
	ai.Add(model)
	d.refresh = append(d.refresh, func() {
		env := d.envSet(prefs.EnvAIModel)
		model.SetSensitive(!env)
		if env {
			model.SetTooltipText(overriddenBy(prefs.EnvAIModel))
		}
	})
	page.Add(ai)
	return page
}

func rgbaHex(c *gdk.RGBA) string {
	b := func(v float32) int { return int(max(0, min(1, v))*255 + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x%02x", b(c.Red()), b(c.Green()), b(c.Blue()), b(c.Alpha()))
}

// ---- Advanced ----

func (d *prefsDialog) advancedPage() *adw.PreferencesPage {
	page := newPage("Advanced", "applications-engineering-symbolic")

	group := newGroup("", "Settings for those who know what they are about. The defaults suit most systems.")
	group.Add(d.navRow("Encoding", "Encoder and extra gpu-screen-recorder options", "applications-multimedia-symbolic", d.encodingPage))
	group.Add(d.navRow("Programs", "Locations of the helper programs", "utilities-terminal-symbolic", d.programsPage))
	group.Add(d.navRow("Background Removal", "Camera cut-out model and ONNX Runtime", "avatar-default-symbolic", d.backgroundPage))
	group.Add(d.navRow("Noise Removal", "Speech model for cleaning recorded sound", "audio-input-microphone-symbolic", d.noisePage))
	page.Add(group)

	danger := newGroup("", "")
	reset := adw.NewButtonRow()
	reset.SetTitle("Reset All Preferences")
	reset.AddCSSClass("destructive-action")
	reset.ConnectActivated(d.confirmReset)
	danger.Add(reset)
	page.Add(danger)
	return page
}

func (d *prefsDialog) confirmReset() {
	alert := adw.NewAlertDialog("Reset All Preferences?",
		"Every preference returns to its default. Deleting old files is switched off until you enable it again.")
	alert.AddResponse("cancel", "_Cancel")
	alert.AddResponse("reset", "_Reset")
	alert.SetResponseAppearance("reset", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response != "reset" {
			return
		}
		if !d.update(func(p *prefs.Prefs) {
			*p = prefs.Default()
			p.SaveLocationChanged = true
		}) {
			return
		}
		d.dlg.ForceClose()
		glib.IdleAdd(func() {
			d.a.showPreferences()
			d.a.toast("Preferences reset")
		})
	})
	alert.Present(d.dlg)
}

func (d *prefsDialog) encodingPage() *adw.NavigationPage {
	page := adw.NewPreferencesPage()

	hw := newGroup("Encoder", "")
	hw.Add(comboRow("Video Encoder", "The processor encodes when the graphics card cannot",
		[]string{"Graphics Card", "Processor"}, slices.Index(prefs.Encoders, d.p.Encoder), func(i int) {
			d.update(func(p *prefs.Prefs) { p.Encoder = prefs.Encoders[i] })
		}))
	page.Add(hw)

	extra := newGroup("Extra Arguments", "Added to gpu-screen-recorder after Tequilashot's own options. Write each as option and value, for example “-ac opus -cr full”.")
	extra.Add(entryRow("Recordings", d.p.RecordingArgs, func(text string) error {
		if _, err := prefs.RecorderArgs(text, false); err != nil {
			return err
		}
		d.update(func(p *prefs.Prefs) { p.RecordingArgs = text })
		return nil
	}))
	extra.Add(entryRow("Instant Replay", d.p.ReplayArgs, func(text string) error {
		if _, err := prefs.RecorderArgs(text, true); err != nil {
			return err
		}
		d.update(func(p *prefs.Prefs) { p.ReplayArgs = text })
		return nil
	}))
	page.Add(extra)

	allowed := newGroup("", "Accepted options: "+strings.Join(prefs.RecorderFlags(false), " ")+
		". Instant replay accepts the same, except -bm.")
	page.Add(allowed)
	return navPage("Encoding", page)
}

func (d *prefsDialog) programsPage() *adw.NavigationPage {
	page := adw.NewPreferencesPage()
	group := newGroup("Helper Programs", "Leave a field empty to find the program on your PATH. Compositor and system tools such as swaymsg, hyprctl and pactl are always taken from PATH.")
	for _, name := range prefs.Programs {
		status := gtk.NewImage()
		status.SetVAlign(gtk.AlignCenter)
		check := func() {
			path, err := exec.LookPath(d.p.Program(name))
			if err != nil {
				status.SetFromIconName("dialog-warning-symbolic")
				status.SetTooltipText("Not found")
				status.AddCSSClass("warning")
				return
			}
			status.SetFromIconName("object-select-symbolic")
			status.SetTooltipText(path)
			status.RemoveCSSClass("warning")
		}
		row := entryRow(name, d.p.ProgramPaths[name], func(text string) error {
			d.update(func(p *prefs.Prefs) {
				if text == "" {
					delete(p.ProgramPaths, name)
					return
				}
				if p.ProgramPaths == nil {
					p.ProgramPaths = map[string]string{}
				}
				p.ProgramPaths[name] = text
			})
			check()
			return nil
		})
		row.AddSuffix(status)
		check()
		group.Add(row)
	}
	page.Add(group)
	return navPage("Programs", page)
}

func (d *prefsDialog) backgroundPage() *adw.NavigationPage {
	page := adw.NewPreferencesPage()

	model := newGroup("Model", "Removes the background behind the round camera. Leave the file empty to use "+prefs.DefaultModnetModelPath()+".")
	state := adw.NewActionRow()
	state.SetTitle("Status")
	model.Add(state)
	file := entryRow("Model File", d.p.ModnetModel, func(text string) error {
		d.update(func(p *prefs.Prefs) { p.ModnetModel = text })
		d.rerender()
		return nil
	})
	model.Add(file)
	mirror := entryRow("Download Mirror", d.p.ModelMirror, func(text string) error {
		if text != "" {
			if err := prefs.ValidateMirror(text); err != nil {
				return err
			}
		}
		d.update(func(p *prefs.Prefs) { p.ModelMirror = text })
		return nil
	})
	mirror.SetInputPurpose(gtk.InputPurposeURL)
	model.Add(mirror)
	download := adw.NewButtonRow()
	download.SetStartIconName("folder-download-symbolic")
	download.ConnectActivated(func() { d.downloadModel(download) })
	model.Add(download)
	page.Add(model)

	runtime := newGroup("ONNX Runtime", "Takes effect when Tequilashot is next started. Leave empty to search the usual system locations.")
	lib := entryRow("Library", d.p.OnnxRuntimeLib, func(text string) error {
		d.update(func(p *prefs.Prefs) { p.OnnxRuntimeLib = text })
		return nil
	})
	runtime.Add(lib)
	loaded := adw.NewActionRow()
	loaded.SetTitle("Loaded Library")
	loaded.SetSubtitleSelectable(true)
	runtime.Add(loaded)
	page.Add(runtime)

	d.refresh = append(d.refresh, func() {
		modelEnv := d.envSet(prefs.EnvModnetModel)
		file.SetSensitive(!modelEnv)
		if modelEnv {
			file.SetTooltipText(overriddenBy(prefs.EnvModnetModel))
		}
		libEnv := d.envSet(prefs.EnvOnnxRuntimeLib)
		lib.SetSensitive(!libEnv)
		if libEnv {
			lib.SetTooltipText(overriddenBy(prefs.EnvOnnxRuntimeLib))
		}
		if l := segment.LoadedLibrary(); l != "" {
			loaded.SetSubtitle(l)
		} else {
			loaded.SetSubtitle("Not loaded yet in this window")
		}
		modelPath := d.modelPath()
		_, statErr := os.Stat(modelPath)
		modelInstalled := statErr == nil
		switch {
		case segment.RuntimeAvailable() != nil:
			state.SetSubtitle("ONNX Runtime is missing; install your distribution's onnxruntime package")
		case modelInstalled:
			state.SetSubtitle("Ready, using " + modelPath)
		default:
			state.SetSubtitle("The model is not installed at " + modelPath)
		}
		if !d.downloading {
			download.SetTitle(map[bool]string{true: "Download Model Again", false: "Download Model"}[modelInstalled])
			download.SetSensitive(!d.a.modelBusy)
		}
	})
	return navPage("Background Removal", page)
}

// downloadModel fetches the model to the configured location; the checksum
// is verified whichever mirror serves it.
func (d *prefsDialog) downloadModel(row *adw.ButtonRow) {
	if d.a.modelBusy {
		return
	}
	d.downloading = true
	d.a.modelBusy = true
	d.a.syncModelRow()
	row.SetSensitive(false)
	row.SetTitle("Downloading, 0%")
	dest := d.modelPath()
	go func() {
		last := -1
		err := segment.DownloadModelTo(context.Background(), dest, func(done, total int64) {
			if pct := int(done * 100 / max(total, 1)); pct != last {
				last = pct
				glib.IdleAdd(func() { row.SetTitle(fmt.Sprintf("Downloading, %d%%", pct)) })
			}
		})
		glib.IdleAdd(func() {
			d.downloading = false
			d.a.modelBusy = false
			if err != nil {
				d.toast("The model could not be downloaded: " + err.Error())
			} else {
				d.toast("Background removal model installed")
			}
			d.rerender()
			d.a.syncModelRow()
			d.a.syncAdvancedSummary()
			d.a.syncPreview()
		})
	}()
}

// denoiseModelPath resolves the noise-removal model with the preferences
// being edited.
func (d *prefsDialog) denoiseModelPath() string {
	if p := os.Getenv(prefs.EnvDenoiseModel); p != "" {
		return p
	}
	if d.p.DenoiseModel != "" {
		return d.p.DenoiseModel
	}
	return prefs.DefaultDenoiseModelPath()
}

func (d *prefsDialog) noisePage() *adw.NavigationPage {
	page := adw.NewPreferencesPage()
	model := newGroup("Model", "Removes background noise and keyboard clicks from speech when cleaning a recording. It also needs ONNX Runtime, as background removal does. Leave the file empty to use "+prefs.DefaultDenoiseModelPath()+".")
	state := adw.NewActionRow()
	state.SetTitle("Status")
	model.Add(state)
	file := entryRow("Model File", d.p.DenoiseModel, func(text string) error {
		d.update(func(p *prefs.Prefs) { p.DenoiseModel = text })
		d.rerender()
		return nil
	})
	model.Add(file)
	download := adw.NewButtonRow()
	download.SetStartIconName("folder-download-symbolic")
	busy := false
	download.ConnectActivated(func() {
		if busy {
			return
		}
		busy = true
		download.SetSensitive(false)
		download.SetTitle("Downloading…")
		dest := d.denoiseModelPath()
		go func() {
			err := denoise.DownloadModelTo(context.Background(), dest, nil)
			glib.IdleAdd(func() {
				busy = false
				if err != nil {
					d.toast("The model could not be downloaded: " + err.Error())
				} else {
					d.toast("Noise removal model installed")
				}
				d.rerender()
			})
		}()
	})
	model.Add(download)
	page.Add(model)

	behaviour := newGroup("Behaviour", "")
	always := switchRow("Always Remove Noise", "Cleans the sound of every new recording as soon as it is saved, keeping the original", d.p.AutoDenoise, func(on bool) {
		d.update(func(p *prefs.Prefs) { p.AutoDenoise = on })
	})
	behaviour.Add(always)
	page.Add(behaviour)

	d.refresh = append(d.refresh, func() {
		if os.Getenv(prefs.EnvAutoDenoise) != "" {
			always.SetSensitive(false)
			always.SetTooltipText(overriddenBy(prefs.EnvAutoDenoise))
		}
		env := d.envSet(prefs.EnvDenoiseModel)
		file.SetSensitive(!env)
		if env {
			file.SetTooltipText(overriddenBy(prefs.EnvDenoiseModel))
		}
		path := d.denoiseModelPath()
		_, err := os.Stat(path)
		installed := err == nil
		if installed {
			state.SetSubtitle("Ready, using " + path)
		} else {
			state.SetSubtitle("The model is not installed at " + path)
		}
		if !busy {
			download.SetTitle(map[bool]string{true: "Download Model Again", false: "Download Model"}[installed])
			download.SetSensitive(true)
		}
	})
	return navPage("Noise Removal", page)
}

// ---- Row helpers ----

func newPage(title, icon string) *adw.PreferencesPage {
	p := adw.NewPreferencesPage()
	p.SetName(strings.ToLower(title))
	p.SetTitle(title)
	p.SetIconName(icon)
	return p
}

func newGroup(title, description string) *adw.PreferencesGroup {
	g := adw.NewPreferencesGroup()
	if title != "" {
		g.SetTitle(title)
	}
	if description != "" {
		g.SetDescription(description)
	}
	return g
}

func navPage(title string, content *adw.PreferencesPage) *adw.NavigationPage {
	view := adw.NewToolbarView()
	view.AddTopBar(adw.NewHeaderBar())
	view.SetContent(content)
	return adw.NewNavigationPage(view, title)
}

// navRow opens a subpage, built on first use and kept for later visits.
func (d *prefsDialog) navRow(title, subtitle, icon string, build func() *adw.NavigationPage) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(title)
	row.SetSubtitle(subtitle)
	row.AddPrefix(gtk.NewImageFromIconName(icon))
	row.AddSuffix(gtk.NewImageFromIconName("go-next-symbolic"))
	row.SetActivatable(true)
	var page *adw.NavigationPage
	row.ConnectActivated(func() {
		if page == nil {
			page = build()
		}
		d.rerender()
		d.dlg.PushSubpage(page)
	})
	return row
}

func iconButton(icon, tooltip string, onClick func()) *gtk.Button {
	b := gtk.NewButtonFromIconName(icon)
	b.AddCSSClass("flat")
	b.SetVAlign(gtk.AlignCenter)
	b.SetTooltipText(tooltip)
	if onClick != nil {
		b.ConnectClicked(onClick)
	}
	return b
}

func mapLabels[T any](values []T, label func(T) string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = label(v)
	}
	return out
}

func comboRow(title, subtitle string, labels []string, selected int, onChange func(int)) *adw.ComboRow {
	row := adw.NewComboRow()
	row.SetTitle(title)
	if subtitle != "" {
		row.SetSubtitle(subtitle)
	}
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(max(selected, 0)))
	row.NotifyProperty("selected", func() {
		if i := int(row.Selected()); i >= 0 && i < len(labels) {
			onChange(i)
		}
	})
	return row
}

func switchRow(title, subtitle string, active bool, onChange func(bool)) *adw.SwitchRow {
	row := adw.NewSwitchRow()
	row.SetTitle(title)
	if subtitle != "" {
		row.SetSubtitle(subtitle)
	}
	row.SetActive(active)
	row.NotifyProperty("active", func() { onChange(row.Active()) })
	return row
}

// entryRow commits its text on apply; a validation error marks the row and
// is shown as its tooltip until corrected.
func entryRow(title, text string, apply func(string) error) *adw.EntryRow {
	row := adw.NewEntryRow()
	row.SetTitle(title)
	row.SetText(text)
	row.SetShowApplyButton(true)
	row.ConnectApply(func() {
		value := strings.TrimSpace(row.Text())
		if err := apply(value); err != nil {
			row.AddCSSClass("error")
			row.SetTooltipText(err.Error())
			return
		}
		row.RemoveCSSClass("error")
		row.SetTooltipText("")
	})
	return row
}
