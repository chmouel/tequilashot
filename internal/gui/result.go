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

	setupTitle  = "Record screen"
	resultTitle = "Recording saved"
)

// resultView holds the widgets and state of the result page.
type resultView struct {
	frame   *gtk.Picture
	name    *gtk.Label
	caption *gtk.Label

	file string
	info MediaInfo
	// gen identifies the shown result so a late preview never lands on a
	// newer one; it is unaffected by renames.
	gen int
	// dialogOpen stops Escape leaving the page whilst a dialogue is on show.
	dialogOpen bool
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
	row.Append(action("Play", "media-playback-start-symbolic", a.playResult))
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

	page := gtk.NewBox(gtk.OrientationVertical, 10)
	page.SetMarginTop(8)
	page.SetMarginBottom(18)
	page.SetMarginStart(18)
	page.SetMarginEnd(18)
	page.Append(r.frame)
	page.Append(r.name)
	page.Append(r.caption)
	page.Append(row)
	page.Append(row2)
	page.Append(again)
	return page
}

func (a *app) onSetupPage() bool {
	return a.pages.VisibleChildName() == pageSetup
}

func (a *app) showSetup() {
	a.pages.SetVisibleChildName(pageSetup)
	a.win.SetTitle(setupTitle)
	a.refresh()
	a.updateThumbTimer()
}

// showResult presents the finished recording and fetches its frame in the background.
func (a *app) showResult(file string, info MediaInfo) {
	r := &a.res
	r.file, r.info = file, info
	r.gen++
	gen := r.gen
	r.frame.SetPaintable(nil)
	a.updateResultLabels()
	a.stopThumbTimer()
	a.pages.SetVisibleChildName(pageResult)
	a.win.SetTitle(resultTitle)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		data, err := ExtractFrame(ctx, file, info.Duration)
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
	body := "The .mp4 extension is kept."
	if problem != "" {
		body = "Regrettably, " + problem + "."
	}
	dlg := adw.NewAlertDialog("Rename recording", body)
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
	dlg := adw.NewAlertDialog("Delete this recording?", filepath.Base(a.res.file)+" will be moved to the bin.")
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
			msg := "Recording moved to the bin"
			if err := gio.NewFileForPath(file).Trash(context.Background()); err != nil {
				log.Printf("trash %s: %v", file, err)
				if rerr := os.Remove(file); rerr != nil {
					msg = fmt.Sprintf("Regrettably, the recording could not be deleted: %v", rerr)
				} else {
					msg = "The bin was unavailable; the recording was deleted permanently"
				}
			}
			glib.IdleAdd(func() {
				a.showSetup()
				a.toast(msg)
			})
		}()
	})
}
