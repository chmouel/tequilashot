//go:build !nogui

package gui

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

type trimImport struct {
	gen    int
	cancel context.CancelFunc
	media  *gtk.MediaFile
	timer  glib.SourceHandle
}

func (a *app) trimImportBlocked() bool {
	return a.closing || a.busy || a.replayBusy || a.replayActive || a.trim.export != nil || a.res.editing || a.res.dialogOpen || a.modalOpen()
}

func (a *app) chooseTrimVideo() {
	if a.trimImportBlocked() {
		a.toast("Please finish the current capture, save or dialogue first")
		return
	}
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Open video")
	filter := gtk.NewFileFilter()
	filter.AddMIMEType("video/*")
	dialog.SetDefaultFilter(filter)
	a.res.dialogOpen = true
	dialog.Open(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.OpenFinish(res)
		a.res.dialogOpen = false
		if err == nil && !a.closing {
			a.importTrimFiles([]*gio.File{file})
		}
	})
}

func (a *app) trimDropTarget() *gtk.DropTarget {
	target := gtk.NewDropTarget(gdk.GTypeFileList, gdk.ActionCopy)
	target.ConnectDrop(func(value *glib.Value, _, _ float64) bool {
		files, ok := value.GoValue().(*gdk.FileList)
		if !ok {
			return false
		}
		return a.importTrimFiles(files.Files())
	})
	return target
}

func (a *app) cancelTrimImport() {
	p := &a.importVideo
	p.gen++
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	if p.timer != 0 {
		glib.SourceRemove(p.timer)
		p.timer = 0
	}
	if p.media != nil {
		p.media.Clear()
		p.media = nil
	}
}

func (a *app) importTrimFiles(files []*gio.File) bool {
	if a.trimImportBlocked() {
		a.toast("Please finish the current capture, save or dialogue first")
		return false
	}
	if len(files) != 1 {
		a.toast("Please open one video at a time")
		return false
	}
	file := files[0].Path()
	if file == "" {
		a.toast("Please choose a local video file")
		return false
	}
	a.cancelTrimImport()
	p := &a.importVideo
	gen, editGen, page := p.gen, a.trim.gen, a.pages.VisibleChildName()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	p.cancel = cancel
	a.toast("Opening video…")
	go func() {
		info, err := probeTrimSource(ctx, file)
		if err == nil {
			if state := a.state(); state != nil && (state.Recording || state.Replay || state.VirtualCamera) {
				err = errors.New("please finish the active capture first")
			}
		}
		glib.IdleAdd(func() {
			if gen != p.gen || a.closing {
				return
			}
			fail := func(message string) { a.cancelTrimImport(); a.toast("The video could not be opened: " + message) }
			if err != nil {
				fail(err.Error())
				return
			}
			if a.trimImportBlocked() || a.pages.VisibleChildName() != page || a.trim.gen != editGen {
				a.cancelTrimImport()
				return
			}
			media := gtk.NewMediaFileForFilename(file)
			p.media = media
			p.timer = glib.TimeoutAdd(40, func() bool {
				if trimMediaError(media) == nil && !media.IsPrepared() && ctx.Err() == nil {
					return true
				}
				p.timer = 0
				if trimMediaError(media) != nil {
					fail("GTK could not decode this video; please check the installed codecs")
					return false
				}
				if ctx.Err() != nil {
					fail("opening the video timed out")
					return false
				}
				if !media.HasVideo() || !media.IsSeekable() {
					fail("this video cannot be previewed and trimmed")
					return false
				}
				cancel()
				p.cancel = nil
				accept := func() {
					if a.closing || gen != p.gen {
						return
					}
					if a.trimImportBlocked() || a.pages.VisibleChildName() != page || a.trim.gen != editGen {
						a.cancelTrimImport()
						return
					}
					p.media = nil
					back := page
					if page == pageTrim {
						back = a.trim.returnPage
					}
					a.openTrimSource(file, info, ModeVideo, back, media)
				}
				if page == pageTrim && (a.trim.selection.remove || a.trim.selection.start > 0 || a.trim.selection.end < a.trim.selection.duration) {
					a.trim.media.Pause()
					a.trim.playOnSeek = false
					dlg := adw.NewAlertDialog("Open another video?", "The current selection has not been saved.")
					dlg.AddResponse("cancel", "Cancel")
					dlg.AddResponse("open", "Open video")
					dlg.SetDefaultResponse("cancel")
					dlg.SetCloseResponse("cancel")
					a.choose(dlg, func(response string) {
						if response == "open" {
							accept()
						} else {
							a.cancelTrimImport()
						}
					})
				} else {
					accept()
				}
				return false
			})
		})
	}()
	return true
}
