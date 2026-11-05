//go:build !nogui

package gui

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"sway-easyshot/internal/external"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

const (
	pageSetup  = "setup"
	pageResult = "result"
)

// resultView holds the widgets and state of the result page.
type resultView struct {
	frame   *gtk.Picture
	name    *gtk.Label
	caption *gtk.Label
	open    *adw.ButtonContent
	again   *gtk.Button
	images  *gtk.Box
	actions *gtk.Box

	file string
	info MediaInfo
	mode string
	// gen identifies the shown result so a late preview never lands on a
	// newer one; it is unaffected by renames.
	gen int
	// dialogOpen stops Escape leaving the page whilst a dialogue is on show.
	dialogOpen bool
	editing    bool
}

func (a *app) buildResult() *gtk.Box {
	r := &a.res

	r.frame = gtk.NewPicture()
	r.frame.AddCSSClass("er-frame")
	r.frame.SetContentFit(gtk.ContentFitCover)
	r.frame.SetCanShrink(true)
	r.frame.SetSizeRequest(480, 270)
	r.frame.SetOverflow(gtk.OverflowHidden)
	r.frame.SetHAlign(gtk.AlignCenter)

	r.name = label("", "heading")
	r.name.SetXAlign(0.5)
	r.name.SetEllipsize(pango.EllipsizeMiddle)

	r.caption = label("", "dim-label", "caption")
	r.caption.SetXAlign(0.5)

	action := func(text, icon string, fn func()) *gtk.Button {
		b := gtk.NewButton()
		content := adw.NewButtonContent()
		content.SetLabel(text)
		content.SetIconName(icon)
		b.SetChild(content)
		b.ConnectClicked(fn)
		return b
	}

	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetHomogeneous(true)
	open := gtk.NewButton()
	r.open = adw.NewButtonContent()
	open.SetChild(r.open)
	open.ConnectClicked(a.playResult)
	row.Append(open)
	row.Append(action("Folder", "folder-symbolic", a.showResultFolder))
	row.Append(action("Copy path", "edit-copy-symbolic", a.copyResultPath))

	del := action("Delete", "user-trash-symbolic", a.confirmDelete)
	del.AddCSSClass("destructive-action")
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	row2 := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row2.Append(action("Rename", "document-edit-symbolic", func() { a.renameDialog("") }))
	row2.Append(spacer)
	row2.Append(del)

	again := gtk.NewButtonWithLabel("New recording")
	again.AddCSSClass("suggested-action")
	again.AddCSSClass("pill")
	again.SetHAlign(gtk.AlignCenter)
	again.SetMarginTop(6)
	again.SetSizeRequest(200, -1)
	again.ConnectClicked(a.showSetup)
	r.again = again

	r.images = gtk.NewBox(gtk.OrientationHorizontal, 8)
	r.images.SetHomogeneous(true)
	r.images.Append(action("Copy image", "edit-copy-symbolic", a.copyResultImage))
	r.images.Append(action("Edit", "document-edit-symbolic", a.editResult))
	r.actions = gtk.NewBox(gtk.OrientationVertical, 10)
	r.actions.Append(r.images)
	r.actions.Append(row)
	r.actions.Append(row2)
	r.actions.Append(again)

	page := gtk.NewBox(gtk.OrientationVertical, 10)
	page.SetMarginTop(8)
	page.SetMarginBottom(18)
	page.SetMarginStart(18)
	page.SetMarginEnd(18)
	page.Append(r.frame)
	page.Append(r.name)
	page.Append(r.caption)
	page.Append(r.actions)
	return page
}

func (a *app) onSetupPage() bool {
	return a.pages.VisibleChildName() == pageSetup
}

func (a *app) showSetup() {
	if a.res.editing {
		return
	}
	a.pages.SetVisibleChildName(pageSetup)
	a.win.SetTitle(a.setupTitle())
	a.refresh()
	a.updateThumbTimer()
}

func (a *app) showResult(file string, info MediaInfo, mode string) {
	r := &a.res
	r.file, r.info, r.mode = file, info, mode
	r.gen++
	gen := r.gen
	r.frame.SetPaintable(nil)
	a.updateResultLabels()
	a.stopThumbTimer()
	a.pages.SetVisibleChildName(pageResult)
	image := mode == ModeScreenshot
	r.images.SetVisible(image)
	if image {
		a.win.SetTitle("Screenshot saved")
		r.frame.SetContentFit(gtk.ContentFitContain)
		r.open.SetLabel("Open")
		r.open.SetIconName("document-open-symbolic")
		r.again.SetLabel("New screenshot")
	} else {
		a.win.SetTitle("Recording saved")
		r.frame.SetContentFit(gtk.ContentFitCover)
		r.open.SetLabel("Play")
		r.open.SetIconName("media-playback-start-symbolic")
		r.again.SetLabel("New recording")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var data []byte
		var err error
		if image {
			data, err = os.ReadFile(file) //nolint:gosec
		} else {
			data, err = ExtractFrame(ctx, file, info.Duration)
		}
		if err != nil {
			log.Printf("preview frame: %v", err)
			return
		}
		glib.IdleAdd(func() {
			if a.res.gen != gen {
				return
			}
			if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data)); err == nil {
				a.res.frame.SetPaintable(tex)
			}
		})
	}()
}

func (a *app) copyResultImage() {
	file := a.res.file
	go func() {
		data, err := os.ReadFile(file) //nolint:gosec
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = external.WlCopy(ctx, data, "image/png")
			cancel()
		}
		glib.IdleAdd(func() {
			if err != nil {
				a.toast("The image could not be copied: " + err.Error())
			} else {
				a.toast("Image copied")
			}
		})
	}()
}

func (a *app) editResult() {
	if a.res.editing {
		return
	}
	a.res.editing = true
	a.res.actions.SetSensitive(false)
	file := a.res.file
	go func() {
		err := external.Satty(context.Background(), file, file, true)
		info, probeErr := ProbeImage(file)
		glib.IdleAdd(func() {
			a.res.editing = false
			a.res.actions.SetSensitive(true)
			if probeErr == nil {
				a.showResult(file, info, ModeScreenshot)
			}
			a.win.Present()
			if err != nil {
				a.toast("The editor could not finish: " + err.Error())
			} else if probeErr != nil {
				a.toast("The screenshot could not be refreshed: " + probeErr.Error())
			}
		})
	}()
}

func (a *app) resultNoun() string {
	if a.res.mode == ModeScreenshot {
		return "screenshot"
	}
	return "recording"
}

func (a *app) updateResultLabels() {
	a.res.name.SetText(filepath.Base(a.res.file))
	a.res.caption.SetText(a.res.info.Caption())
}

func (a *app) playResult() {
	detach("xdg-open", a.res.file)
}

func (a *app) showResultFolder() {
	file := a.res.file
	uri := gio.NewFileForPath(file).URI()
	go func() {
		if !strings.Contains(uri, "'") {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := exec.CommandContext(ctx, "gdbus", "call", "--session", //nolint:gosec
				"--dest", "org.freedesktop.FileManager1", "--object-path", "/org/freedesktop/FileManager1",
				"--method", "org.freedesktop.FileManager1.ShowItems", "['"+uri+"']", "").Run()
			if err == nil {
				return
			}
		}
		detach("xdg-open", filepath.Dir(file))
	}()
}

// detach starts a helper without waiting for it, reaping it in the background.
func detach(name string, args ...string) {
	cmd := exec.Command(name, args...) //nolint:gosec
	if err := cmd.Start(); err != nil {
		log.Printf("%s: %v", name, err)
		return
	}
	go func() { _ = cmd.Wait() }()
}

func (a *app) copyResultPath() {
	if display := gdk.DisplayGetDefault(); display != nil {
		display.Clipboard().SetText(a.res.file)
		a.toast("Path copied")
	}
}

func (a *app) choose(dlg *adw.AlertDialog, done func(response string)) {
	a.res.dialogOpen = true
	dlg.Choose(context.Background(), a.win, func(res gio.AsyncResulter) {
		a.res.dialogOpen = false
		done(dlg.ChooseFinish(res))
	})
}

// renameDialog asks for a new name; problem, when set, explains a previous refusal.
func (a *app) renameDialog(problem string) {
	body := "The " + filepath.Ext(a.res.file) + " extension is kept."
	if problem != "" {
		body = "Regrettably, " + problem + "."
	}
	dlg := adw.NewAlertDialog("Rename "+a.resultNoun(), body)
	dlg.AddResponse("cancel", "Cancel")
	dlg.AddResponse("rename", "Rename")
	dlg.SetResponseAppearance("rename", adw.ResponseSuggested)
	dlg.SetDefaultResponse("rename")
	dlg.SetCloseResponse("cancel")

	entry := gtk.NewEntry()
	entry.SetText(strings.TrimSuffix(filepath.Base(a.res.file), filepath.Ext(a.res.file)))
	entry.SetActivatesDefault(true)
	dlg.SetExtraChild(entry)

	a.choose(dlg, func(response string) {
		if response != "rename" {
			return
		}
		target, err := RenameTarget(a.res.file, entry.Text())
		if err == nil && target != a.res.file {
			err = os.Rename(a.res.file, target)
		}
		if err != nil {
			a.renameDialog(err.Error())
			return
		}
		a.res.file = target
		a.updateResultLabels()
	})
	entry.GrabFocus()
}

func (a *app) confirmDelete() {
	noun := a.resultNoun()
	dlg := adw.NewAlertDialog("Delete this "+noun+"?", filepath.Base(a.res.file)+" will be moved to the bin.")
	dlg.AddResponse("cancel", "Cancel")
	dlg.AddResponse("delete", "Delete")
	dlg.SetResponseAppearance("delete", adw.ResponseDestructive)
	dlg.SetDefaultResponse("cancel")
	dlg.SetCloseResponse("cancel")

	a.choose(dlg, func(response string) {
		if response != "delete" {
			return
		}
		file := a.res.file
		go func() {
			msg := "The " + noun + " was moved to the bin"
			if err := gio.NewFileForPath(file).Trash(context.Background()); err != nil {
				log.Printf("trash %s: %v", file, err)
				if rerr := os.Remove(file); rerr != nil {
					msg = fmt.Sprintf("Regrettably, the %s could not be deleted: %v", noun, rerr)
				} else {
					msg = "The bin was unavailable; the " + noun + " was deleted permanently"
				}
			}
			glib.IdleAdd(func() {
				a.showSetup()
				a.toast(msg)
			})
		}()
	})
}
